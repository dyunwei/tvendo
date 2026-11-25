package main

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

var rechargeAmounts = []int64{1, 1000, 5000, 10000, 20000}
var paymentRequestKey = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,64}$`)

type paymentGateway interface {
	Prepay(context.Context, paymentOrder) (string, error)
	Query(context.Context, string) (*payments.Transaction, error)
	Close(context.Context, string) error
}
type wechatPayment struct {
	gateway      paymentGateway
	handler      *notify.Handler
	appID, mchID string
}
type nativeGateway struct {
	svc                     native.NativeApiService
	appID, mchID, notifyURL string
}
type paymentOrder struct {
	ID            string     `json:"id"`
	UserID        int64      `json:"user_id"`
	AmountCents   int64      `json:"amount_cents"`
	Status        string     `json:"status"`
	CodeURL       string     `json:"code_url"`
	RefundStatus  string     `json:"refund_status,omitempty"`
	TransactionID *string    `json:"transaction_id"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	PaidAt        *time.Time `json:"paid_at"`
	AppID         string     `json:"-"`
	MchID         string     `json:"-"`
}

const paymentColumns = `id,user_id,amount_cents,status,code_url,transaction_id,created_at,expires_at,paid_at,appid,mch_id`

func scanPayment(row pgx.Row) (o paymentOrder, err error) {
	err = row.Scan(&o.ID, &o.UserID, &o.AmountCents, &o.Status, &o.CodeURL, &o.TransactionID, &o.CreatedAt, &o.ExpiresAt, &o.PaidAt, &o.AppID, &o.MchID)
	return
}
func newWechatPayment(ctx context.Context) (*wechatPayment, error) {
	if env("WECHAT_PAY_ENABLED", "false") != "true" {
		return nil, nil
	}
	keys := []string{"WECHAT_PAY_APP_ID", "WECHAT_PAY_MCH_ID", "WECHAT_PAY_API_V3_KEY", "WECHAT_PAY_SERIAL_NO", "WECHAT_PAY_PRIVATE_KEY_PATH"}
	for _, k := range keys {
		if os.Getenv(k) == "" {
			return nil, fmt.Errorf("%s is required when WECHAT_PAY_ENABLED=true", k)
		}
	}
	if len(os.Getenv("WECHAT_PAY_API_V3_KEY")) != 32 {
		return nil, errors.New("WECHAT_PAY_API_V3_KEY must be 32 bytes")
	}
	notifyURL, e := configuredWechatNotifyURL()
	if e != nil {
		return nil, e
	}
	privatePEM, e := os.ReadFile(os.Getenv("WECHAT_PAY_PRIVATE_KEY_PATH"))
	if e != nil {
		return nil, fmt.Errorf("read WeChat merchant private key: %w", e)
	}
	privateKey, e := utils.LoadPrivateKey(string(privatePEM))
	if e != nil {
		return nil, errors.New("invalid WeChat merchant private key")
	}
	appID, mchID := os.Getenv(keys[0]), os.Getenv(keys[1])
	verifier, e := configuredPaymentVerifier()
	if e != nil {
		return nil, e
	}
	// Native order endpoints do not contain fields requiring RSA encryption.
	// Use the same verifier for signed API responses and encrypted notifications.
	client, e := core.NewClient(ctx,
		option.WithMerchantCredential(mchID, os.Getenv("WECHAT_PAY_SERIAL_NO"), privateKey),
		option.WithVerifier(verifier),
		option.WithHTTPClient(&http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}))
	if e != nil {
		return nil, e
	}
	handler, e := notify.NewRSANotifyHandler(os.Getenv("WECHAT_PAY_API_V3_KEY"), verifier)
	if e != nil {
		return nil, e
	}
	return &wechatPayment{gateway: &nativeGateway{native.NativeApiService{Client: client}, appID, mchID, notifyURL}, handler: handler, appID: appID, mchID: mchID}, nil
}

// Each payment provider owns its complete callback URL, independently of the website origin.
func configuredWechatNotifyURL() (string, error) {
	raw := os.Getenv("WECHAT_PAY_NOTIFY_URL")
	if raw == "" {
		return "", errors.New("WECHAT_PAY_NOTIFY_URL is required when WECHAT_PAY_ENABLED=true")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", errors.New("WECHAT_PAY_NOTIFY_URL must be an absolute HTTPS URL without credentials, query or fragment")
	}
	return raw, nil
}

// CertificateDownloader produces X.509 platform certificates, not PUB_KEY_ID public keys.
// Certificate mode pins the PEM bundle supplied by the operator; no unverified download occurs.
func configuredPaymentVerifier() (auth.Verifier, error) {
	switch env("WECHAT_PAY_VERIFY_MODE", "public_key") {
	case "public_key":
		keyID := os.Getenv("WECHAT_PAY_PUBLIC_KEY_ID")
		if !strings.HasPrefix(keyID, "PUB_KEY_ID_") || len(keyID) == len("PUB_KEY_ID_") {
			return nil, errors.New("WECHAT_PAY_PUBLIC_KEY_ID must start with PUB_KEY_ID_ in public_key mode")
		}
		if os.Getenv("WECHAT_PAY_PUBLIC_KEY_PATH") == "" {
			return nil, errors.New("WECHAT_PAY_PUBLIC_KEY_PATH is required in public_key mode")
		}
		data, e := os.ReadFile(os.Getenv("WECHAT_PAY_PUBLIC_KEY_PATH"))
		if e != nil {
			return nil, fmt.Errorf("read WeChat Pay public key: %w", e)
		}
		key, e := utils.LoadPublicKey(string(data))
		if e != nil {
			return nil, errors.New("invalid WeChat Pay public key; platform certificates require WECHAT_PAY_VERIFY_MODE=certificate")
		}
		if key.N.BitLen() < 2048 {
			return nil, errors.New("WeChat Pay public key must be RSA 2048 bits or stronger")
		}
		paymentLog("verifier_ready", map[string]any{"verify_mode": "public_key", "public_key_id": keyID})
		return verifiers.NewSHA256WithRSAPubkeyVerifier(keyID, *key), nil
	case "certificate":
		path := os.Getenv("WECHAT_PAY_PLATFORM_CERT_PATH")
		if path == "" {
			return nil, errors.New("WECHAT_PAY_PLATFORM_CERT_PATH is required in certificate mode")
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return nil, fmt.Errorf("read WeChat Pay platform certificate: %w", e)
		}
		certs := []*x509.Certificate{}
		valid := false
		for len(bytes.TrimSpace(data)) > 0 {
			if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("-----BEGIN CERTIFICATE-----")) {
				return nil, errors.New("platform certificate file must contain PEM CERTIFICATE blocks")
			}
			block, rest := pem.Decode(data)
			if block == nil || block.Type != "CERTIFICATE" {
				return nil, errors.New("platform certificate file must contain PEM CERTIFICATE blocks")
			}
			cert, e := x509.ParseCertificate(block.Bytes)
			if e != nil {
				return nil, errors.New("invalid WeChat Pay platform certificate")
			}
			if strings.EqualFold(utils.GetCertificateSerialNumber(*cert), strings.TrimSpace(os.Getenv("WECHAT_PAY_SERIAL_NO"))) {
				return nil, errors.New("WECHAT_PAY_PLATFORM_CERT_PATH contains the merchant API certificate (same serial as WECHAT_PAY_SERIAL_NO); use the WeChat Pay platform certificate downloaded by CertificateDownloader, not apiclient_cert.pem")
			}
			key, ok := cert.PublicKey.(*rsa.PublicKey)
			if !ok || key.N.BitLen() < 2048 {
				return nil, errors.New("platform certificate must contain an RSA key of at least 2048 bits")
			}
			now := time.Now()
			if !now.Before(cert.NotBefore) && now.Before(cert.NotAfter) {
				valid = true
			}
			certs = append(certs, cert)
			data = rest
		}
		if !valid {
			return nil, errors.New("platform certificate file must contain at least one currently valid certificate")
		}
		metadata := []map[string]string{}
		for _, cert := range certs {
			metadata = append(metadata, map[string]string{"serial": utils.GetCertificateSerialNumber(*cert), "not_before": cert.NotBefore.UTC().Format(time.RFC3339), "not_after": cert.NotAfter.UTC().Format(time.RFC3339)})
		}
		paymentLog("verifier_ready", map[string]any{"verify_mode": "certificate", "certificates": metadata})
		certMap := core.NewCertificateMapWithList(certs)
		return &platformCertificateVerifier{certMap, verifiers.NewSHA256WithRSAVerifier(certMap)}, nil
	default:
		return nil, errors.New("WECHAT_PAY_VERIFY_MODE must be public_key or certificate")
	}
}

type platformCertificateVerifier struct {
	certs    *core.CertificateMap
	verifier auth.Verifier
}

func (v *platformCertificateVerifier) Verify(ctx context.Context, serial, message, signature string) error {
	cert, ok := v.certs.Get(ctx, serial)
	if !ok {
		return errors.New("unknown WeChat Pay platform certificate serial")
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return errors.New("WeChat Pay platform certificate is outside its validity period; renew the PEM bundle and restart")
	}
	return v.verifier.Verify(ctx, serial, message, signature)
}
func (v *platformCertificateVerifier) GetSerial(ctx context.Context) (string, error) {
	return v.verifier.GetSerial(ctx)
}

func (g *nativeGateway) Prepay(ctx context.Context, o paymentOrder) (string, error) {
	started := time.Now()
	paymentLog("request", map[string]any{"operation": "prepay", "method": "POST", "path": "/v3/pay/transactions/native", "order_id": o.ID, "amount_cents": o.AmountCents, "currency": "CNY", "appid": g.appID, "mch_id": g.mchID, "notify_url": g.notifyURL})
	resp, result, e := g.svc.Prepay(ctx, native.PrepayRequest{Appid: core.String(g.appID), Mchid: core.String(g.mchID), Description: core.String("T vendo 账户充值"), OutTradeNo: &o.ID, TimeExpire: &o.ExpiresAt, NotifyUrl: &g.notifyURL, Amount: &native.Amount{Total: &o.AmountCents, Currency: core.String("CNY")}})
	if e == nil && (resp == nil || resp.CodeUrl == nil || !strings.HasPrefix(*resp.CodeUrl, "weixin://wxpay/") && !strings.HasPrefix(*resp.CodeUrl, "weixin://pay.weixin.qq.com/")) {
		e = errors.New("invalid WeChat payment URL")
	}
	paymentResponseLog("prepay", o.ID, started, result, e, "")
	if e != nil {
		return "", e
	}
	return *resp.CodeUrl, nil
}
func (g *nativeGateway) Query(ctx context.Context, id string) (*payments.Transaction, error) {
	started := time.Now()
	paymentLog("request", map[string]any{"operation": "query", "method": "GET", "path": "/v3/pay/transactions/out-trade-no/{order_id}", "order_id": id})
	v, result, e := g.svc.QueryOrderByOutTradeNo(ctx, native.QueryOrderByOutTradeNoRequest{OutTradeNo: &id, Mchid: &g.mchID})
	state := ""
	if v != nil {
		state = value(v.TradeState)
	}
	paymentResponseLog("query", id, started, result, e, state)
	return v, e
}
func (g *nativeGateway) Close(ctx context.Context, id string) error {
	started := time.Now()
	paymentLog("request", map[string]any{"operation": "close", "method": "POST", "path": "/v3/pay/transactions/out-trade-no/{order_id}/close", "order_id": id})
	result, e := g.svc.CloseOrder(ctx, native.CloseOrderRequest{OutTradeNo: &id, Mchid: &g.mchID})
	paymentResponseLog("close", id, started, result, e, "")
	return e
}
func (a *App) paymentConfig(w http.ResponseWriter, r *http.Request, u User) {
	writeJSON(w, 200, map[string]any{"enabled": a.payments != nil, "amounts_cents": rechargeAmounts})
}

// Trace before session/CSRF checks so a stalled authentication dependency is visible too.
func (a *App) paymentOrderRequest(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		paymentLog("order_request_received", map[string]any{"method": r.Method, "path": r.URL.Path})
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		defer func() {
			paymentLog("order_request_finished", map[string]any{"duration_ms": time.Since(started).Milliseconds(), "canceled": ctx.Err() != nil})
		}()
		next(w, r.WithContext(ctx))
	}
}

func (a *App) createPayment(w http.ResponseWriter, r *http.Request, u User) {
	paymentLog("order_authenticated", map[string]any{"user_id": u.ID})
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	stage := "validate"
	defer func() {
		if ctx.Err() != nil {
			paymentLog("order_timeout_or_canceled", map[string]any{"stage": stage, "user_id": u.ID})
		}
	}()
	if a.payments == nil {
		fail(w, 503, "微信支付暂未开通，请联系管理员充值")
		return
	}
	var b struct {
		AmountCents int64  `json:"amount_cents"`
		RequestKey  string `json:"request_key"`
	}
	if !decode(w, r, &b) {
		return
	}
	valid := false
	for _, v := range rechargeAmounts {
		if b.AmountCents == v {
			valid = true
		}
	}
	if !valid || !paymentRequestKey.MatchString(b.RequestKey) {
		fail(w, 400, "请选择充值金额并提供有效请求标识")
		return
	}
	stage = "db_begin"
	paymentLog("order_db_begin", map[string]any{"user_id": u.ID, "amount_cents": b.AmountCents})
	tx, e := a.db.Begin(r.Context())
	if e != nil {
		paymentLogFailure("order_"+stage+"_failed", "", e)
		fail(w, 500, "无法创建充值订单")
		return
	}
	defer tx.Rollback(r.Context())
	// Serialize creation for each user, including duplicate requests and the pending-order limit.
	var userID int64
	stage = "user_lock"
	paymentLog("order_user_lock", map[string]any{"user_id": u.ID})
	if e = tx.QueryRow(r.Context(), `SELECT id FROM users WHERE id=$1 FOR UPDATE`, u.ID).Scan(&userID); e != nil {
		paymentLogFailure("order_"+stage+"_failed", "", e)
		fail(w, 500, "无法创建充值订单")
		return
	}
	stage = "order_lookup"
	existing, e := scanPayment(tx.QueryRow(r.Context(), `SELECT `+paymentColumns+` FROM payment_orders WHERE user_id=$1 AND request_key=$2`, u.ID, b.RequestKey))
	if e == nil {
		if existing.AmountCents != b.AmountCents {
			fail(w, 409, "请求标识已用于其他充值金额")
			return
		}
		writeJSON(w, 200, existing)
		return
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		paymentLogFailure("order_"+stage+"_failed", "", e)
		fail(w, 500, "无法读取充值订单")
		return
	}
	stage = "pending_count"
	var pending int
	if e = tx.QueryRow(r.Context(), `SELECT count(*) FROM payment_orders WHERE user_id=$1 AND status='pending'`, u.ID).Scan(&pending); e != nil {
		paymentLogFailure("order_"+stage+"_failed", "", e)
		fail(w, 500, "无法读取充值订单")
		return
	}
	if pending >= 5 {
		fail(w, 429, "未完成订单过多，请完成已有订单或等待订单关闭")
		return
	}
	id := randomToken()[:32]
	expires := time.Now().UTC().Add(15 * time.Minute)
	stage = "order_insert"
	o, e := scanPayment(tx.QueryRow(r.Context(), `INSERT INTO payment_orders(id,user_id,request_key,amount_cents,appid,mch_id,expires_at,next_check_at) VALUES($1,$2,$3,$4,$5,$6,$7,now()+interval '20 seconds') RETURNING `+paymentColumns, id, u.ID, b.RequestKey, b.AmountCents, a.payments.appID, a.payments.mchID, expires))
	if e != nil {
		paymentLogFailure("order_"+stage+"_failed", "", e)
		fail(w, 500, "无法创建充值订单")
		return
	}
	stage = "order_commit"
	if e = tx.Commit(r.Context()); e != nil {
		fail(w, 500, "无法创建充值订单，请使用相同请求标识重试")
		return
	}
	paymentLog("order_created", map[string]any{"order_id": id, "user_id": u.ID, "amount_cents": b.AmountCents})
	stage = "wechat_prepay"
	// A timeout can still mean WeChat accepted the order. Keep it pending for reconciliation.
	code, e := a.payments.gateway.Prepay(r.Context(), o)
	if e == nil {
		stage = "save_code_url"
		_, e = a.db.Exec(r.Context(), `UPDATE payment_orders SET code_url=$2 WHERE id=$1`, id, code)
		o.CodeURL = code
	}
	if e != nil {
		paymentLogFailure("prepay_unresolved", id, e)
	}
	writeJSON(w, 201, o)
}
func (a *App) paymentOrders(w http.ResponseWriter, r *http.Request, u User) {
	a.rows(w, r, `SELECT row_to_json(x) FROM (
		SELECT p.id,p.user_id,p.amount_cents,p.status,p.code_url,p.transaction_id,p.created_at,p.expires_at,p.paid_at,
			r.status AS refund_status
		FROM payment_orders p LEFT JOIN payment_refunds r ON r.order_id=p.id
		WHERE p.user_id=$1 ORDER BY p.created_at DESC LIMIT 100) x`, u.ID)
}
func (a *App) adminPaymentOrders(w http.ResponseWriter, r *http.Request, u User) {
	a.rows(w, r, `SELECT row_to_json(x) FROM (
		SELECT p.id,p.user_id,u.email,p.amount_cents,p.status,p.transaction_id,p.created_at,p.expires_at,p.paid_at,
			r.status AS refund_status,r.wechat_refund_id
		FROM payment_orders p JOIN users u ON u.id=p.user_id LEFT JOIN payment_refunds r ON r.order_id=p.id
		ORDER BY p.created_at DESC LIMIT 200) x`)
}
func (a *App) getPayment(w http.ResponseWriter, r *http.Request, u User) {
	o, e := scanPayment(a.db.QueryRow(r.Context(), `SELECT `+paymentColumns+` FROM payment_orders WHERE id=$1 AND user_id=$2`, r.PathValue("id"), u.ID))
	if errors.Is(e, pgx.ErrNoRows) {
		fail(w, 404, "充值订单不存在")
		return
	}
	if e != nil {
		fail(w, 500, "无法读取充值订单")
		return
	}
	if o.Status == "paid" {
		e = a.db.QueryRow(r.Context(), `SELECT status FROM payment_refunds WHERE order_id=$1`, o.ID).Scan(&o.RefundStatus)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			fail(w, 500, "无法读取退款状态")
			return
		}
	}
	writeJSON(w, 200, o)
}
func value[T any](p *T) (v T) {
	if p != nil {
		return *p
	}
	return
}
func validatePaymentIdentity(o paymentOrder, t *payments.Transaction) error {
	// Query responses may omit trade_type and amount for unpaid/closed orders.
	if t == nil || value(t.OutTradeNo) != o.ID || value(t.Appid) != o.AppID || value(t.Mchid) != o.MchID || (value(t.TradeType) != "" && value(t.TradeType) != "NATIVE") {
		return errors.New("payment identity mismatch")
	}
	return nil
}
func validatePayment(o paymentOrder, t *payments.Transaction) error {
	if e := validatePaymentIdentity(o, t); e != nil {
		return e
	}
	if t.Amount == nil || value(t.Amount.Currency) != "CNY" || value(t.Amount.Total) != o.AmountCents {
		return errors.New("payment amount mismatch")
	}
	return nil
}
func (a *App) applyPayment(ctx context.Context, t *payments.Transaction) error {
	if t == nil || value(t.TradeState) != "SUCCESS" || value(t.TransactionId) == "" {
		return errors.New("payment is not successful")
	}
	paidAt, e := time.Parse(time.RFC3339, value(t.SuccessTime))
	if e != nil {
		return errors.New("invalid payment success time")
	}
	tx, e := a.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	o, e := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentColumns+` FROM payment_orders WHERE id=$1 FOR UPDATE`, value(t.OutTradeNo)))
	if e != nil {
		return e
	}
	if e = validatePayment(o, t); e != nil {
		return e
	}
	if o.Status == "paid" {
		if o.TransactionID == nil || *o.TransactionID != *t.TransactionId {
			return errors.New("payment transaction mismatch")
		}
		paymentLog("settlement_duplicate", map[string]any{"order_id": o.ID, "transaction_id": value(t.TransactionId)})
		return nil
	}
	// Accept verified late success even if a close notification raced with it.
	_, e = tx.Exec(ctx, `UPDATE payment_orders SET status='paid',transaction_id=$2,paid_at=$3 WHERE id=$1`, o.ID, *t.TransactionId, paidAt)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE users SET balance=balance+$2 WHERE id=$1`, o.UserID, o.AmountCents*10000)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO transactions(user_id,amount,kind,note,payment_order_id) VALUES($1,$2,'topup','微信支付充值',$3)`, o.UserID, o.AmountCents*10000, o.ID)
	if e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	paymentLog("settlement_succeeded", map[string]any{"order_id": o.ID, "user_id": o.UserID, "transaction_id": value(t.TransactionId), "amount_cents": o.AmountCents, "credited_micros": o.AmountCents * 10000})
	return nil
}
func (a *App) wechatNotify(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	paymentLog("notify_received", map[string]any{"method": r.Method, "serial": r.Header.Get("Wechatpay-Serial"), "request_id": r.Header.Get("Request-ID"), "content_length": r.ContentLength})
	if a.payments == nil || a.payments.handler == nil {
		paymentLog("notify_rejected", map[string]any{"http_status": 503, "reason": "payments_disabled"})
		fail(w, 503, "支付暂不可用")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var t payments.Transaction
	event, e := a.payments.handler.ParseNotifyRequest(r.Context(), r, &t)
	if e != nil || event == nil || event.EventType != "TRANSACTION.SUCCESS" || value(t.TradeType) != "NATIVE" {
		reason := "unexpected_event_or_trade_type"
		if e != nil {
			reason = paymentErrorFields(e)["error"].(string)
		}
		paymentLog("notify_rejected", map[string]any{"http_status": 400, "reason": reason, "duration_ms": time.Since(started).Milliseconds()})
		fail(w, 400, "无效支付通知")
		return
	}
	paymentLog("notify_verified", map[string]any{"event_id": event.ID, "event_type": event.EventType, "order_id": value(t.OutTradeNo), "transaction_id": value(t.TransactionId), "trade_state": value(t.TradeState)})
	if e = a.applyPayment(r.Context(), &t); e != nil {
		paymentLogFailure("notify_settlement_failed", value(t.OutTradeNo), e)
		fail(w, 500, "支付通知处理失败")
		return
	}
	paymentLog("notify_acknowledged", map[string]any{"order_id": value(t.OutTradeNo), "http_status": 204, "duration_ms": time.Since(started).Milliseconds()})
	w.WriteHeader(http.StatusNoContent)
}
func (a *App) reconcilePayment(ctx context.Context, o paymentOrder) error {
	// Do not send an old merchant's order to a different merchant after reconfiguration.
	if o.AppID != a.payments.appID || o.MchID != a.payments.mchID {
		return errors.New("payment merchant configuration changed")
	}
	t, e := a.payments.gateway.Query(ctx, o.ID)
	if e != nil {
		var apiErr *core.APIError
		if errors.As(e, &apiErr) && apiErr.Code == "ORDER_NOT_EXIST" && time.Now().After(o.ExpiresAt.Add(time.Minute)) {
			_, e = a.db.Exec(ctx, `UPDATE payment_orders SET status='closed' WHERE id=$1 AND status='pending'`, o.ID)
			return e
		}
		return e
	}
	if e = validatePaymentIdentity(o, t); e != nil {
		return e
	}
	switch value(t.TradeState) {
	case "SUCCESS":
		return a.applyPayment(ctx, t)
	case "CLOSED", "REVOKED", "PAYERROR":
		_, e = a.db.Exec(ctx, `UPDATE payment_orders SET status='closed' WHERE id=$1 AND status='pending'`, o.ID)
		return e
	case "NOTPAY":
		if time.Now().After(o.ExpiresAt) {
			if e = a.payments.gateway.Close(ctx, o.ID); e != nil {
				return e
			}
			_, e = a.db.Exec(ctx, `UPDATE payment_orders SET status='closed' WHERE id=$1 AND status='pending'`, o.ID)
			return e
		}
	}
	return nil
}
func (a *App) recoverPayments(ctx context.Context) {
	// Claim a small batch atomically, keeping multiple app instances from polling the same orders.
	rows, e := a.db.Query(ctx, `UPDATE payment_orders SET next_check_at=now()+interval '30 seconds' WHERE id IN (SELECT id FROM payment_orders WHERE status='pending' AND next_check_at<=now() ORDER BY next_check_at LIMIT 20 FOR UPDATE SKIP LOCKED) RETURNING `+paymentColumns)
	if e != nil {
		log.Print("payment recovery query failed")
		return
	}
	orders := []paymentOrder{}
	for rows.Next() {
		o, err := scanPayment(rows)
		if err != nil {
			rows.Close()
			return
		}
		orders = append(orders, o)
	}
	rows.Close()
	if rows.Err() != nil {
		return
	}
	for _, o := range orders {
		if ctx.Err() != nil {
			return
		}
		if e = a.reconcilePayment(ctx, o); e != nil {
			a.monitoring.orderCheckError.Add(1)
			paymentLogFailure("reconciliation_deferred", o.ID, e)
		}
	}
}
func (a *App) runPaymentRecovery(ctx context.Context) {
	a.recoverPayments(ctx)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.recoverPayments(ctx)
		}
	}
}
