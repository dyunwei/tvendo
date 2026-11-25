package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
)

type refundGateway interface {
	CreateRefund(context.Context, paymentOrder, string) (*refunddomestic.Refund, error)
	QueryRefund(context.Context, string) (*refunddomestic.Refund, error)
}

func (g *nativeGateway) CreateRefund(ctx context.Context, order paymentOrder, reason string) (*refunddomestic.Refund, error) {
	svc := refunddomestic.RefundsApiService{Client: g.svc.Client}
	refund, _, err := svc.Create(ctx, refunddomestic.CreateRequest{
		TransactionId: order.TransactionID,
		OutRefundNo:   &order.ID,
		Reason:        &reason,
		Amount: &refunddomestic.AmountReq{
			Refund: &order.AmountCents, Total: &order.AmountCents, Currency: core.String("CNY"),
		},
	})
	return refund, err
}

func (g *nativeGateway) QueryRefund(ctx context.Context, orderID string) (*refunddomestic.Refund, error) {
	svc := refunddomestic.RefundsApiService{Client: g.svc.Client}
	refund, _, err := svc.QueryByOutRefundNo(ctx, refunddomestic.QueryByOutRefundNoRequest{OutRefundNo: &orderID})
	return refund, err
}

func (a *App) requestPaymentRefund(w http.ResponseWriter, r *http.Request, actor User) {
	if a.payments == nil {
		fail(w, 503, "微信支付未启用")
		return
	}
	if _, ok := a.payments.gateway.(refundGateway); !ok {
		fail(w, 503, "当前支付配置未启用退款")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || len(input.Reason) > 80 {
		fail(w, 400, "请填写退款原因（最多 80 字节）")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		fail(w, 503, "暂时无法登记退款")
		return
	}
	defer tx.Rollback(ctx)
	order, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentColumns+` FROM payment_orders WHERE id=$1 FOR UPDATE`, r.PathValue("id")))
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "充值订单不存在")
		return
	}
	if err != nil {
		fail(w, 503, "暂时无法读取订单")
		return
	}
	if order.Status != "paid" || order.TransactionID == nil || order.AppID != a.payments.appID || order.MchID != a.payments.mchID {
		fail(w, 409, "该订单无法退款，请核对支付状态及商户配置")
		return
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM payment_refunds WHERE order_id=$1`, order.ID).Scan(&status)
	if err == nil {
		writeJSON(w, 200, map[string]string{"order_id": order.ID, "status": status})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		fail(w, 503, "暂时无法读取退款状态")
		return
	}
	reserved, err := tx.Exec(ctx, `UPDATE users SET balance=balance-$2 WHERE id=$1 AND balance>=$2`, order.UserID, order.AmountCents*10000)
	if err != nil {
		fail(w, 503, "暂时无法预留退款余额")
		return
	}
	if reserved.RowsAffected() == 0 {
		fail(w, 409, "账户可用余额不足以退回原订单全额")
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO payment_refunds(order_id,actor_id,reason) VALUES($1,$2,$3)`, order.ID, actor.ID, input.Reason); err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO audit_log(actor_id,action,target_id) VALUES($1,'request_wechat_refund',$2)`, actor.ID, order.UserID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		paymentLogFailure("refund_reservation_failed", order.ID, err)
		fail(w, 503, "登记结果暂无法确认，请刷新订单")
		return
	}
	writeJSON(w, 202, map[string]string{"order_id": order.ID, "status": "queued"})
}

func (a *App) applyPaymentRefund(ctx context.Context, order paymentOrder, refund *refunddomestic.Refund) error {
	if refund == nil || value(refund.RefundId) == "" || value(refund.OutRefundNo) != order.ID ||
		value(refund.OutTradeNo) != order.ID || value(refund.TransactionId) != value(order.TransactionID) ||
		refund.Amount == nil || value(refund.Amount.Total) != order.AmountCents ||
		value(refund.Amount.Refund) != order.AmountCents || value(refund.Amount.Currency) != "CNY" {
		return errors.New("refund identity or amount mismatch")
	}
	var status string
	var delay = "1 minute"
	switch value(refund.Status) {
	case refunddomestic.STATUS_SUCCESS:
		status = "succeeded"
		if refund.SuccessTime == nil {
			return errors.New("missing refund success time")
		}
	case refunddomestic.STATUS_CLOSED:
		status = "closed"
	case refunddomestic.STATUS_PROCESSING:
		status = "processing"
	case refunddomestic.STATUS_ABNORMAL:
		status = "abnormal"
		delay = "1 hour"
	default:
		return errors.New("unrecognized refund status")
	}

	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var actorID int64
	err = tx.QueryRow(ctx, `
		UPDATE payment_refunds
		SET status=$2,wechat_refund_id=$3,updated_at=now(),succeeded_at=$4,next_check_at=now()+$5::interval
		WHERE order_id=$1 AND status IN ('queued','processing','abnormal')
		  AND (wechat_refund_id IS NULL OR wechat_refund_id=$3)
		RETURNING actor_id`, order.ID, status, value(refund.RefundId), refund.SuccessTime, delay).Scan(&actorID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingID *string
		if err = tx.QueryRow(ctx, `SELECT wechat_refund_id FROM payment_refunds WHERE order_id=$1`, order.ID).Scan(&existingID); err == nil && value(existingID) != value(refund.RefundId) {
			return errors.New("refund identifier mismatch")
		}
		return err
	}
	if err != nil {
		return err
	}
	switch status {
	case "succeeded":
		_, err = tx.Exec(ctx, `INSERT INTO transactions(user_id,amount,kind,note,actor_id,refund_order_id)
			VALUES($1,$2,'wechat_refund','微信支付原路退款',$3,$4)`, order.UserID, -order.AmountCents*10000, actorID, order.ID)
	case "closed":
		_, err = tx.Exec(ctx, `UPDATE users SET balance=balance+$2 WHERE id=$1`, order.UserID, order.AmountCents*10000)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *App) recoverPaymentRefunds(ctx context.Context) {
	gateway, ok := a.payments.gateway.(refundGateway)
	if !ok {
		return
	}
	// Lease one row at a time. If a process stops after submitting a refund,
	// the next worker looks up the same merchant refund number.
	var orderID, status, reason string
	err := a.db.QueryRow(ctx, `UPDATE payment_refunds SET next_check_at=now()+interval '1 minute'
		WHERE order_id=(SELECT order_id FROM payment_refunds r JOIN payment_orders p ON p.id=r.order_id
			WHERE r.status IN ('queued','processing','abnormal') AND r.next_check_at<=now() AND p.appid=$1 AND p.mch_id=$2
			ORDER BY r.next_check_at LIMIT 1 FOR UPDATE OF r SKIP LOCKED)
		RETURNING order_id,status,reason`, a.payments.appID, a.payments.mchID).Scan(&orderID, &status, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		paymentLogFailure("refund_claim_failed", orderID, err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	order, err := scanPayment(a.db.QueryRow(ctx, `SELECT `+paymentColumns+` FROM payment_orders WHERE id=$1`, orderID))
	if err != nil {
		paymentLogFailure("refund_order_unavailable", orderID, err)
		return
	}
	refund, err := gateway.QueryRefund(ctx, orderID)
	var apiErr *core.APIError
	if status == "queued" && errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound && apiErr.Code == "RESOURCE_NOT_EXISTS" {
		refund, err = gateway.CreateRefund(ctx, order, reason)
	}
	if err == nil {
		err = a.applyPaymentRefund(ctx, order, refund)
	}
	if err != nil {
		paymentLogFailure("refund_check_deferred", orderID, err)
	}
}

func (a *App) runRefundRecovery(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		a.recoverPaymentRefunds(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
