package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/validators"
)

const wechatBillHost = "api.mch.weixin.qq.com"

var billingDayZone = time.FixedZone("UTC+8", 8*60*60)
var tvendoOrderID = regexp.MustCompile(`^[0-9a-f]{32}$`)

type billGateway interface {
	DownloadBill(context.Context, string) ([]byte, error)
}

func (g *nativeGateway) DownloadBill(ctx context.Context, billDate string) ([]byte, error) {
	query := url.Values{"bill_date": {billDate}, "bill_type": {"ALL"}}
	result, err := g.svc.Client.Get(ctx, "https://"+wechatBillHost+"/v3/bill/tradebill?"+query.Encode())
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound && apiErr.Code == "NO_BILL_EXIST" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ticket struct {
		URL      string `json:"download_url"`
		HashType string `json:"hash_type"`
		Hash     string `json:"hash_value"`
	}
	if err := core.UnMarshalResponse(result.Response, &ticket); err != nil {
		return nil, err
	}
	target, err := url.Parse(ticket.URL)
	if err != nil || target.Scheme != "https" || target.Host != wechatBillHost || target.User != nil ||
		target.Path != "/v3/billdownload/file" || target.Query().Get("token") == "" || target.Fragment != "" {
		return nil, errors.New("invalid WeChat bill download URL")
	}
	if !strings.EqualFold(ticket.HashType, "SHA1") {
		return nil, errors.New("unsupported WeChat bill hash")
	}
	// The file response is unsigned. Its SHA-1 is covered by the signed ticket.
	fileClient := core.NewClientWithValidator(g.svc.Client, &validators.NullValidator{})
	result, err = fileClient.Get(ctx, target.String())
	if err != nil {
		return nil, err
	}
	defer result.Response.Body.Close()
	const maxBillBytes = 32 << 20
	data, err := io.ReadAll(io.LimitReader(result.Response.Body, maxBillBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBillBytes || len(data) == 0 {
		return nil, errors.New("invalid WeChat bill file size")
	}
	digest := sha1.Sum(data)
	if !strings.EqualFold(ticket.Hash, hex.EncodeToString(digest[:])) {
		return nil, errors.New("WeChat bill hash mismatch")
	}
	return data, nil
}

type billEntry struct {
	Kind       string `json:"kind"`
	OrderID    string `json:"order_id"`
	WechatID   string `json:"wechat_id"`
	Cents      int64  `json:"amount_cents"`
	LedgerOkay bool   `json:"-"`
}

func (e billEntry) key() string { return e.Kind + ":" + e.OrderID }

func billCents(value string) (int64, error) {
	whole, fraction, hasFraction := strings.Cut(value, ".")
	if !hasFraction || len(fraction) != 2 || strings.ContainsAny(value, "+-") {
		return 0, errors.New("invalid bill amount")
	}
	cents, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil || cents < 1 {
		return 0, errors.New("invalid bill amount")
	}
	return cents, nil
}

func readWechatBill(content []byte, appID, merchantID string) ([]billEntry, error) {
	entries := []billEntry{}
	if content == nil {
		return entries, nil
	}
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	columns := make(map[string]int, len(header))
	for i, name := range header {
		columns[strings.TrimSpace(name)] = i
	}
	for _, name := range []string{"公众账号ID", "商户号", "微信订单号", "商户订单号", "交易状态", "货币种类", "订单金额", "微信退款单号", "退款状态", "申请退款金额"} {
		if _, exists := columns[name]; !exists {
			return nil, fmt.Errorf("bill column %q missing", name)
		}
	}
	var detailCount int
	for {
		row, err := reader.Read()
		if err != nil {
			return nil, fmt.Errorf("bill detail missing or malformed: %w", err)
		}
		if strings.TrimSpace(row[0]) == "总交易单数" {
			total, err := reader.Read()
			if err != nil {
				return nil, err
			}
			count, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(total[0]), "`"))
			if err != nil || count != detailCount {
				return nil, errors.New("bill row count does not match total")
			}
			if _, err := reader.Read(); !errors.Is(err, io.EOF) {
				return nil, errors.New("unexpected bill footer")
			}
			return entries, nil
		}
		if len(row) != len(header) {
			return nil, errors.New("bill column count mismatch")
		}
		detailCount++
		field := func(name string) string { return strings.TrimPrefix(strings.TrimSpace(row[columns[name]]), "`") }
		order := field("商户订单号")
		if field("公众账号ID") != appID || field("商户号") != merchantID || !tvendoOrderID.MatchString(order) {
			continue
		}
		entry := billEntry{OrderID: order}
		var amount string
		switch field("交易状态") {
		case "SUCCESS":
			entry.Kind, entry.WechatID, amount = "payment", field("微信订单号"), field("订单金额")
		case "REFUND":
			if field("退款状态") != "SUCCESS" {
				continue
			}
			entry.Kind, entry.WechatID, amount = "refund", field("微信退款单号"), field("申请退款金额")
		default:
			continue
		}
		entry.Cents, err = billCents(amount)
		if err != nil || entry.WechatID == "" || field("货币种类") != "CNY" {
			return nil, errors.New("invalid payment or refund in WeChat bill")
		}
		entries = append(entries, entry)
	}
}

type billDifference struct {
	Reason string     `json:"reason"`
	Local  *billEntry `json:"local,omitempty"`
	Wechat *billEntry `json:"wechat,omitempty"`
}

type paymentBillReport struct {
	Date            string           `json:"bill_date"`
	CheckedAt       time.Time        `json:"checked_at"`
	Matched         int              `json:"matched"`
	DifferenceCount int              `json:"difference_count"`
	Differences     []billDifference `json:"differences"`
}

func comparePaymentBill(report *paymentBillReport, local, wechat []billEntry) {
	record := func(reason string, local, wechat *billEntry) {
		report.DifferenceCount++
		if len(report.Differences) < 200 {
			report.Differences = append(report.Differences, billDifference{reason, local, wechat})
		}
	}
	expected := make(map[string]billEntry, len(local))
	for _, entry := range local {
		expected[entry.key()] = entry
	}
	seen := make(map[string]bool, len(wechat))
	for _, entry := range wechat {
		key := entry.key()
		platform, exists := expected[key]
		switch {
		case seen[key]:
			record("duplicate_in_wechat", nil, &entry)
		case !exists:
			record("missing_locally", nil, &entry)
		case !platform.LedgerOkay:
			record("ledger_mismatch", &platform, &entry)
		case platform.Cents != entry.Cents || platform.WechatID != entry.WechatID:
			record("amount_or_id_mismatch", &platform, &entry)
		default:
			report.Matched++
		}
		seen[key] = true
	}
	sort.Slice(local, func(i, j int) bool { return local[i].key() < local[j].key() })
	for _, entry := range local {
		if !seen[entry.key()] {
			record("missing_in_wechat", &entry, nil)
		}
	}
}

func (a *App) checkPaymentBill(ctx context.Context, billDay time.Time) (report paymentBillReport, err error) {
	gateway, ok := a.payments.gateway.(billGateway)
	if !ok {
		return report, errors.New("bill gateway unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	report = paymentBillReport{Date: billDay.Format(time.DateOnly), Differences: []billDifference{}}
	content, err := gateway.DownloadBill(ctx, report.Date)
	if err != nil {
		return report, err
	}
	mchID, appID := a.payments.mchID, a.payments.appID
	wechat, err := readWechatBill(content, appID, mchID)
	if err != nil {
		return report, err
	}
	until := billDay.AddDate(0, 0, 1)
	rows, err := a.db.Query(ctx, `
		SELECT 'payment',p.id,p.transaction_id,p.amount_cents,
			COALESCE(t.amount=p.amount_cents*10000 AND t.user_id=p.user_id AND t.kind='topup',false)
		FROM payment_orders p LEFT JOIN transactions t ON t.payment_order_id=p.id
		WHERE p.mch_id=$1 AND p.appid=$2 AND p.status='paid' AND p.paid_at >= $3 AND p.paid_at < $4
		UNION ALL
		SELECT 'refund',p.id,r.wechat_refund_id,p.amount_cents,
			COALESCE(t.amount=-p.amount_cents*10000 AND t.user_id=p.user_id AND t.kind='wechat_refund',false)
		FROM payment_refunds r JOIN payment_orders p ON p.id=r.order_id LEFT JOIN transactions t ON t.refund_order_id=r.order_id
		WHERE p.mch_id=$1 AND p.appid=$2 AND r.status='succeeded' AND r.succeeded_at >= $3 AND r.succeeded_at < $4`, mchID, appID, billDay, until)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	var local []billEntry
	for rows.Next() {
		var entry billEntry
		if err := rows.Scan(&entry.Kind, &entry.OrderID, &entry.WechatID, &entry.Cents, &entry.LedgerOkay); err != nil {
			return report, err
		}
		local = append(local, entry)
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	comparePaymentBill(&report, local, wechat)
	err = a.db.QueryRow(ctx, `INSERT INTO payment_bill_reports(merchant_id,app_id,bill_date,matched,difference_count,differences)
		VALUES($1,$2,$3::text::date,$4,$5,$6) ON CONFLICT (merchant_id,app_id,bill_date)
		DO UPDATE SET checked_at=now(),matched=EXCLUDED.matched,difference_count=EXCLUDED.difference_count,differences=EXCLUDED.differences
		RETURNING checked_at`, mchID, appID, report.Date, report.Matched, report.DifferenceCount, report.Differences).Scan(&report.CheckedAt)
	if err == nil {
		paymentLog("bill_checked", map[string]any{"bill_date": report.Date, "matched": report.Matched, "difference_count": report.DifferenceCount})
	}
	return report, err
}

func (a *App) listPaymentBills(w http.ResponseWriter, r *http.Request, _ User) {
	if a.payments == nil {
		writeJSON(w, 200, []paymentBillReport{})
		return
	}
	a.rows(w, r, `SELECT row_to_json(x) FROM (
		SELECT bill_date,checked_at,matched,difference_count,differences FROM payment_bill_reports
		WHERE merchant_id=$1 AND app_id=$2 ORDER BY bill_date DESC LIMIT 90) x`, a.payments.mchID, a.payments.appID)
}

func (a *App) requestPaymentBill(w http.ResponseWriter, r *http.Request, _ User) {
	if a.payments == nil {
		fail(w, 503, "微信支付未启用")
		return
	}
	var input struct {
		Date string `json:"bill_date"`
	}
	if !decode(w, r, &input) {
		return
	}
	day, err := time.ParseInLocation(time.DateOnly, input.Date, billingDayZone)
	now := time.Now().In(billingDayZone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, billingDayZone)
	if err != nil || !day.Before(today) || day.Before(today.AddDate(0, 0, -90)) {
		fail(w, 400, "请选择最近 90 天内已经结束的账单日期（北京时间）")
		return
	}
	report, err := a.checkPaymentBill(r.Context(), day)
	if err != nil {
		paymentLogFailure("bill_check_failed", report.Date, err)
		fail(w, 503, "对账未完成，请检查微信账单权限或稍后重试")
		return
	}
	writeJSON(w, 200, report)
}

func (a *App) runPaymentBillRecovery(ctx context.Context) {
	if _, ok := a.payments.gateway.(billGateway); !ok {
		return
	}
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		a.checkYesterdayPaymentBill(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) checkYesterdayPaymentBill(ctx context.Context) {
	now := time.Now().In(billingDayZone)
	if now.Hour() < 11 {
		return
	}
	yesterday := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, billingDayZone)
	var checked bool
	err := a.db.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM payment_bill_reports WHERE merchant_id=$1 AND app_id=$2 AND bill_date=$3::text::date
	)`, a.payments.mchID, a.payments.appID, yesterday.Format(time.DateOnly)).Scan(&checked)
	if err == nil && !checked {
		_, err = a.checkPaymentBill(ctx, yesterday)
	}
	if err != nil {
		paymentLogFailure("bill_check_failed", yesterday.Format(time.DateOnly), err)
	}
}
