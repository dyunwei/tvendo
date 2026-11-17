package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
)

type fakePaymentGateway struct {
	prepayCalls atomic.Int32
	closeCalls  atomic.Int32
	prepayErr   error
	transaction *payments.Transaction
	queryErr    error
	closeErr    error
}

func (f *fakePaymentGateway) Prepay(context.Context, paymentOrder) (string, error) {
	f.prepayCalls.Add(1)
	return "weixin://wxpay/bizpayurl?pr=test", f.prepayErr
}
func (f *fakePaymentGateway) Query(context.Context, string) (*payments.Transaction, error) {
	return f.transaction, f.queryErr
}
func (f *fakePaymentGateway) Close(context.Context, string) error {
	f.closeCalls.Add(1)
	return f.closeErr
}
func successfulPayment(o paymentOrder) *payments.Transaction {
	return &payments.Transaction{Appid: &o.AppID, Mchid: &o.MchID, OutTradeNo: &o.ID, TradeType: core.String("NATIVE"), TradeState: core.String("SUCCESS"), TransactionId: core.String("wx-" + o.ID), SuccessTime: core.String(time.Now().UTC().Format(time.RFC3339)), Amount: &payments.TransactionAmount{Currency: core.String("CNY"), Total: &o.AmountCents}}
}
func TestPaymentConfiguration(t *testing.T) {
	t.Setenv("WECHAT_PAY_VERIFY_MODE", "public_key")
	t.Setenv("WECHAT_PAY_ENABLED", "false")
	if p, e := newWechatPayment(context.Background()); e != nil || p != nil {
		t.Fatalf("disabled: %v", e)
	}
	t.Setenv("WECHAT_PAY_ENABLED", "true")
	t.Setenv("WECHAT_PAY_APP_ID", "")
	if _, e := newWechatPayment(context.Background()); e == nil {
		t.Fatal("missing configuration accepted")
	}
	for _, key := range []string{"WECHAT_PAY_APP_ID", "WECHAT_PAY_MCH_ID", "WECHAT_PAY_SERIAL_NO", "WECHAT_PAY_PRIVATE_KEY_PATH", "WECHAT_PAY_PUBLIC_KEY_ID", "WECHAT_PAY_PUBLIC_KEY_PATH"} {
		t.Setenv(key, "test")
	}
	t.Setenv("WECHAT_PAY_API_V3_KEY", strings.Repeat("a", 32))
	for _, callback := range []string{"", "http://example.com/notify", "/notify", "https://example.com/notify?bad=1", "https://example.com/notify?", "https://user:pass@example.com/notify", "https://example.com/notify#fragment", "https://example.com/notify#"} {
		t.Setenv("WECHAT_PAY_NOTIFY_URL", callback)
		if _, e := newWechatPayment(context.Background()); e == nil || !strings.Contains(e.Error(), "WECHAT_PAY_NOTIFY_URL") {
			t.Fatalf("invalid callback URL %q: %v", callback, e)
		}
	}
	for _, callback := range []string{"https://payments.example.com/api/payments/wechat/notify", "https://gateway.example.com:8443/webhooks/wechat"} {
		t.Setenv("WECHAT_PAY_NOTIFY_URL", callback)
		t.Setenv("PUBLIC_URL", "http://localhost:8080")
		if got, e := configuredWechatNotifyURL(); e != nil || got != callback {
			t.Fatalf("independent callback URL: %q %v", got, e)
		}
	}

}
func TestPaymentValidation(t *testing.T) {
	o := paymentOrder{ID: "test", AppID: "app", MchID: "merchant", AmountCents: 1000}
	for _, mutate := range []func(*payments.Transaction){
		func(v *payments.Transaction) { v.Appid = core.String("other") }, func(v *payments.Transaction) { v.Mchid = core.String("other") }, func(v *payments.Transaction) { v.OutTradeNo = core.String("other") }, func(v *payments.Transaction) { v.TradeType = core.String("JSAPI") }, func(v *payments.Transaction) { v.Amount.Total = core.Int64(999) }, func(v *payments.Transaction) { v.Amount.Currency = core.String("USD") }, func(v *payments.Transaction) { v.Amount = nil },
	} {
		v := successfulPayment(o)
		mutate(v)
		if validatePayment(o, v) == nil {
			t.Fatal("invalid payment accepted")
		}
	}
	if e := validatePayment(o, successfulPayment(o)); e != nil {
		t.Fatal(e)
	}
}

// Called by the existing isolated PostgreSQL/Redis integration test after its billing checks.
func exercisePayments(t *testing.T, a *App, cookie *http.Cookie, csrf string, userID int64) {
	ctx := context.Background()
	gateway := &fakePaymentGateway{}
	rsaKey, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	apiKey := strings.Repeat("k", 32)
	keyID := "PUB_KEY_ID_test"
	handler, e := notify.NewRSANotifyHandler(apiKey, verifiers.NewSHA256WithRSAPubkeyVerifier(keyID, rsaKey.PublicKey))
	if e != nil {
		t.Fatal(e)
	}
	a.payments = &wechatPayment{gateway: gateway, handler: handler, appID: "test-app", mchID: "test-merchant"}
	defer func() { a.payments = nil }()
	h := a.routes()
	request := func(method, path string, body any, auth bool, token string) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			if e := json.NewEncoder(&buf).Encode(body); e != nil {
				t.Fatal(e)
			}
		}
		r := httptest.NewRequest(method, path, &buf)
		if auth {
			r.AddCookie(cookie)
		}
		r.Header.Set("X-CSRF-Token", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	readOrder := func(id string) paymentOrder {
		t.Helper()
		o, e := scanPayment(a.db.QueryRow(ctx, `SELECT `+paymentColumns+` FROM payment_orders WHERE id=$1`, id))
		if e != nil {
			t.Fatal(e)
		}
		return o
	}
	createBody := func(key string) map[string]any { return map[string]any{"amount_cents": 1000, "request_key": key} }
	create := func(key string) paymentOrder {
		t.Helper()
		w := request("POST", "/api/payments/orders", createBody(key), true, csrf)
		if w.Code != 201 && w.Code != 200 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		var o paymentOrder
		if e := json.Unmarshal(w.Body.Bytes(), &o); e != nil {
			t.Fatal(e)
		}
		return readOrder(o.ID)
	}
	assertLedger := func(o paymentOrder, want int) {
		t.Helper()
		var count int
		var amount int64
		e := a.db.QueryRow(ctx, `SELECT count(*),coalesce(sum(amount),0) FROM transactions WHERE payment_order_id=$1`, o.ID).Scan(&count, &amount)
		if e != nil || count != want || amount != int64(want)*o.AmountCents*10000 {
			t.Fatalf("ledger: count=%d amount=%d err=%v", count, amount, e)
		}
	}
	signedNotify := func(v *payments.Transaction, tamper bool) *httptest.ResponseRecorder {
		plain, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		block, _ := aes.NewCipher([]byte(apiKey))
		gcm, _ := cipher.NewGCM(block)
		nonce := "123456789012"
		aad := "transaction"
		encrypted := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte(nonce), plain, []byte(aad)))
		body, _ := json.Marshal(map[string]any{"id": "notice", "event_type": "TRANSACTION.SUCCESS", "resource_type": "encrypt-resource", "resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "nonce": nonce, "associated_data": aad, "ciphertext": encrypted}})
		timestamp := fmt.Sprint(time.Now().Unix())
		message := timestamp + "\nnonce\n" + string(body) + "\n"
		digest := sha256.Sum256([]byte(message))
		signature, e := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest[:])
		if e != nil {
			t.Fatal(e)
		}
		if tamper {
			body = append(body, ' ')
		}
		r := httptest.NewRequest("POST", "/api/payments/wechat/notify", bytes.NewReader(body))
		r.Header.Set("Wechatpay-Serial", keyID)
		r.Header.Set("Wechatpay-Timestamp", timestamp)
		r.Header.Set("Wechatpay-Nonce", "nonce")
		r.Header.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(signature))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	t.Run("authorization and CSRF", func(t *testing.T) {
		for _, c := range []struct {
			auth  bool
			token string
			want  int
		}{{false, "", 401}, {true, "", 403}} {
			w := request("POST", "/api/payments/orders", createBody(randomToken()), c.auth, c.token)
			if w.Code != c.want {
				t.Fatalf("got %d want %d", w.Code, c.want)
			}
		}
		if w := request("GET", "/api/admin/payments", nil, true, ""); w.Code != 403 {
			t.Fatalf("admin isolation: %d", w.Code)
		}
		w := request("POST", "/api/payments/orders", map[string]any{"amount_cents": 2, "request_key": randomToken()}, true, csrf)
		if w.Code != 400 {
			t.Fatal("arbitrary amount accepted")
		}
	})
	t.Run("concurrent idempotent creation", func(t *testing.T) {
		key := randomToken()
		var wg sync.WaitGroup
		ids := make(chan string, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w := request("POST", "/api/payments/orders", createBody(key), true, csrf)
				if w.Code != 200 && w.Code != 201 {
					t.Errorf("create %d %s", w.Code, w.Body.String())
					return
				}
				var o paymentOrder
				json.Unmarshal(w.Body.Bytes(), &o)
				ids <- o.ID
			}()
		}
		wg.Wait()
		close(ids)
		id := ""
		for v := range ids {
			if id != "" && id != v {
				t.Fatal("duplicate orders")
			}
			id = v
		}
		if gateway.prepayCalls.Load() != 1 {
			t.Fatal("duplicate prepay")
		}
		body := createBody(key)
		body["amount_cents"] = 5000
		if w := request("POST", "/api/payments/orders", body, true, csrf); w.Code != 409 {
			t.Fatal("amount conflict accepted")
		}
		o := readOrder(id)
		// The original user's order is invisible to a different account, even via the handler.
		r := httptest.NewRequest("GET", "/api/payments/orders/"+o.ID, nil)
		r.SetPathValue("id", o.ID)
		w := httptest.NewRecorder()
		a.getPayment(w, r, User{ID: userID + 100000})
		if w.Code != 404 {
			t.Fatal("order exposed across accounts")
		}
		before, _ := a.user(ctx, userID)
		v := successfulPayment(o)
		if w := signedNotify(v, true); w.Code != 400 {
			t.Fatal("tampered signature accepted")
		}
		assertLedger(o, 0)
		bad := successfulPayment(o)
		bad.Amount.Total = core.Int64(2000)
		if w := signedNotify(bad, false); w.Code != 500 {
			t.Fatal("amount mismatch accepted")
		}
		assertLedger(o, 0)
		// Exercise valid signature verification/decryption and concurrent callback delivery.
		var notifyWG sync.WaitGroup
		for i := 0; i < 12; i++ {
			notifyWG.Add(1)
			go func() {
				defer notifyWG.Done()
				if w := signedNotify(v, false); w.Code != 204 {
					t.Errorf("notify %d %s", w.Code, w.Body.String())
				}
			}()
		}
		notifyWG.Wait()
		assertLedger(o, 1)
		after, _ := a.user(ctx, userID)
		if after.Balance-before.Balance != 10000000 {
			t.Fatalf("wrong balance delta: %d", after.Balance-before.Balance)
		}
	})
	t.Run("platform certificate notification", func(t *testing.T) {
		configureTestPayment(t, rsaKey)
		t.Setenv("WECHAT_PAY_VERIFY_MODE", "certificate")
		t.Setenv("WECHAT_PAY_PLATFORM_CERT_PATH", testPaymentFile(t, "platform.pem", testPlatformCertificate(t, rsaKey, 0x5678, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))))
		configured, e := newWechatPayment(ctx)
		if e != nil {
			t.Fatal(e)
		}
		previousHandler, previousSerial := a.payments.handler, keyID
		a.payments.handler, keyID = configured.handler, "5678"
		defer func() { a.payments.handler, keyID = previousHandler, previousSerial }()
		o := create(randomToken())
		v := successfulPayment(o)
		if w := signedNotify(v, true); w.Code != 400 {
			t.Fatal("tampered certificate notification accepted")
		}
		assertLedger(o, 0)
		for i := 0; i < 2; i++ {
			if w := signedNotify(v, false); w.Code != 204 {
				t.Fatalf("certificate notification: %d %s", w.Code, w.Body.String())
			}
		}
		assertLedger(o, 1)
	})

	t.Run("unique transaction and atomic rollback", func(t *testing.T) {
		o := create(randomToken())
		v := successfulPayment(o)
		// Reuse an existing WeChat transaction ID; the entire transaction must be rejected.
		var existing string
		if e := a.db.QueryRow(ctx, `SELECT transaction_id FROM payment_orders WHERE status='paid' LIMIT 1`).Scan(&existing); e != nil {
			t.Fatal(e)
		}
		v.TransactionId = &existing
		before, _ := a.user(ctx, userID)
		if e := a.applyPayment(ctx, v); e == nil {
			t.Fatal("duplicate transaction accepted")
		}
		assertLedger(o, 0)
		if readOrder(o.ID).Status != "pending" {
			t.Fatal("failed settlement changed order")
		}
		// Force ledger insertion to fail after the balance update and ensure both roll back.
		_, e := a.db.Exec(ctx, `INSERT INTO transactions(user_id,amount,kind,payment_order_id) VALUES($1,0,'test',$2)`, userID, o.ID)
		if e != nil {
			t.Fatal(e)
		}
		if e = a.applyPayment(ctx, successfulPayment(o)); e == nil {
			t.Fatal("ledger uniqueness failure ignored")
		}
		after, _ := a.user(ctx, userID)
		if after.Balance != before.Balance || readOrder(o.ID).Status != "pending" {
			t.Fatal("partial settlement committed")
		}
		if _, e = a.db.Exec(ctx, `DELETE FROM transactions WHERE payment_order_id=$1`, o.ID); e != nil {
			t.Fatal(e)
		}
		if e = a.applyPayment(ctx, successfulPayment(o)); e != nil {
			t.Fatal(e)
		}
		assertLedger(o, 1)
	})
	t.Run("lost notification recovered after restart", func(t *testing.T) {
		o := create(randomToken())
		gateway.transaction = successfulPayment(o)
		if _, e := a.db.Exec(ctx, `UPDATE payment_orders SET next_check_at=now() WHERE id=$1`, o.ID); e != nil {
			t.Fatal(e)
		}
		restarted := &App{db: a.db, payments: a.payments}
		restarted.recoverPayments(ctx)
		if readOrder(o.ID).Status != "paid" {
			t.Fatal("recovery did not settle")
		}
		assertLedger(o, 1)
		if e := restarted.reconcilePayment(ctx, o); e != nil {
			t.Fatal(e)
		}
		assertLedger(o, 1)
	})
	t.Run("prepay timeout and expired order closure", func(t *testing.T) {
		gateway.prepayErr = errors.New("timeout")
		o := create(randomToken())
		if o.Status != "pending" || o.CodeURL != "" {
			t.Fatal("uncertain order discarded")
		}
		gateway.prepayErr = nil
		gateway.transaction = successfulPayment(o)
		gateway.transaction.TradeState = core.String("NOTPAY")
		gateway.transaction.TradeType = nil
		gateway.transaction.Amount = nil
		o.ExpiresAt = time.Now().Add(-time.Minute)
		gateway.closeErr = errors.New("timeout")
		if e := a.reconcilePayment(ctx, o); e == nil || readOrder(o.ID).Status != "pending" {
			t.Fatal("unconfirmed close marked final")
		}
		gateway.closeErr = nil
		if e := a.reconcilePayment(ctx, o); e != nil {
			t.Fatal(e)
		}
		if readOrder(o.ID).Status != "closed" {
			t.Fatal("order not closed")
		}
		assertLedger(o, 0)
		// A late, verified success is still credited once, regardless of previous close.
		if e := a.applyPayment(ctx, successfulPayment(o)); e != nil {
			t.Fatal(e)
		}
		assertLedger(o, 1)
	})
	t.Run("order nonexistent only closes after expiry grace", func(t *testing.T) {
		o := create(randomToken())
		gateway.queryErr = &core.APIError{Code: "ORDER_NOT_EXIST"}
		if e := a.reconcilePayment(ctx, o); e == nil || readOrder(o.ID).Status != "pending" {
			t.Fatal("fresh nonexistent order closed")
		}
		o.ExpiresAt = time.Now().Add(-2 * time.Minute)
		if e := a.reconcilePayment(ctx, o); e != nil {
			t.Fatal(e)
		}
		if readOrder(o.ID).Status != "closed" {
			t.Fatal("nonexistent expired order not closed")
		}
		gateway.queryErr = nil
	})
	t.Run("one cent live payment", func(t *testing.T) {
		// Simulate the previous release's amount constraint before applying the upgrade.
		if _, e := a.db.Exec(ctx, `ALTER TABLE payment_orders DROP CONSTRAINT payment_orders_amount_cents_check, ADD CONSTRAINT payment_orders_amount_cents_check CHECK (amount_cents IN (1000,5000,10000,20000))`); e != nil {
			t.Fatal(e)
		}
		if _, e := a.db.Exec(ctx, schema); e != nil {
			t.Fatal(e)
		}
		body := createBody(randomToken())
		body["amount_cents"] = 1
		w := request("POST", "/api/payments/orders", body, true, csrf)
		if w.Code != 201 {
			t.Fatalf("one cent create: %d %s", w.Code, w.Body.String())
		}
		var created paymentOrder
		if e := json.Unmarshal(w.Body.Bytes(), &created); e != nil {
			t.Fatal(e)
		}
		o := readOrder(created.ID)
		if o.AmountCents != 1 {
			t.Fatal("one cent amount changed")
		}
		before, _ := a.user(ctx, userID)
		if e := a.applyPayment(ctx, successfulPayment(o)); e != nil {
			t.Fatal(e)
		}
		after, _ := a.user(ctx, userID)
		if after.Balance-before.Balance != 10000 {
			t.Fatal("one cent credit incorrect")
		}
		assertLedger(o, 1)
	})

	t.Run("pending limit", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			create(randomToken())
		}
		if w := request("POST", "/api/payments/orders", createBody(randomToken()), true, csrf); w.Code != 429 {
			t.Fatalf("pending limit %d", w.Code)
		}
	})
}
