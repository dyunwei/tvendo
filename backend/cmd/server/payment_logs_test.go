package main

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
)

func TestPaymentLogsRedactSensitiveData(t *testing.T) {
	var output bytes.Buffer
	writer := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(writer)
	t.Setenv("WECHAT_PAY_API_V3_KEY", "secret-api-v3-key")
	apiErr := &core.APIError{StatusCode: 400, Code: "PARAM_ERROR", Message: "bad value secret-api-v3-key", Header: http.Header{"Request-Id": []string{"wx-request-123"}, "Wechatpay-Signature": []string{"SECRET_SIGNATURE"}, "Authorization": []string{"SECRET_AUTHORIZATION"}}, Body: "SECRET_BODY", Detail: "SECRET_DETAIL"}
	paymentResponseLog("prepay", "order-123", time.Now(), nil, apiErr, "")
	paymentLogFailure("notify_rejected", "", errors.New("invalid notification request: SECRET_SIGNATURE SECRET_BODY"))
	paymentLog("request", map[string]any{"operation": "prepay", "order_id": "order-123", "amount_cents": 1})
	got := output.String()
	for _, expected := range []string{"wechat_pay", `"operation":"prepay"`, `"amount_cents":1`, `"order_id":"order-123"`, `"error_code":"PARAM_ERROR"`, `"request_id":"wx-request-123"`, `"http_status":400`, "[REDACTED]", "signature_or_certificate_validation_failed"} {
		if !strings.Contains(got, expected) {
			t.Errorf("missing %q in %s", expected, got)
		}
	}
	for _, secret := range []string{"secret-api-v3-key", "SECRET_SIGNATURE", "SECRET_AUTHORIZATION", "SECRET_BODY", "SECRET_DETAIL"} {
		if strings.Contains(got, secret) {
			t.Errorf("sensitive data leaked: %s", secret)
		}
	}
}
func TestPaymentNotifyReceiptLoggedWhenDisabled(t *testing.T) {
	var output bytes.Buffer
	writer := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(writer)
	r := httptest.NewRequest("POST", "/api/payments/wechat/notify", strings.NewReader("PRIVATE_BODY"))
	r.Header.Set("Wechatpay-Signature", "PRIVATE_SIGNATURE")
	w := httptest.NewRecorder()
	(&App{}).wechatNotify(w, r)
	got := output.String()
	if w.Code != 503 || !strings.Contains(got, "notify_received") || !strings.Contains(got, "payments_disabled") {
		t.Fatalf("missing callback receipt log: status=%d logs=%s", w.Code, got)
	}
	if strings.Contains(got, "PRIVATE_BODY") || strings.Contains(got, "PRIVATE_SIGNATURE") {
		t.Fatal("callback secret logged")
	}
}

func TestPaymentOrderRequestTrace(t *testing.T) {
	var output bytes.Buffer
	writer := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(writer)
	entered := false
	next := func(w http.ResponseWriter, r *http.Request) {
		entered = true
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 25*time.Second {
			t.Error("missing bounded request context")
		}
		w.WriteHeader(403)
	}
	w := httptest.NewRecorder()
	(&App{}).paymentOrderRequest(next)(w, httptest.NewRequest("POST", "/api/payments/orders", nil))
	if !entered || !strings.Contains(output.String(), "order_request_received") || !strings.Contains(output.String(), "order_request_finished") {
		t.Fatalf("request trace missing: %s", output.String())
	}
}

func TestPaymentVerificationDiagnostics(t *testing.T) {
	for _, c := range []struct{ message, want string }{
		{"validate verify: unknown WeChat Pay platform certificate serial", "unknown_platform_certificate_serial"},
		{"validate verify: WeChat Pay platform certificate is outside its validity period", "certificate_outside_validity_period"},
		{"timestamp=[1] expires, request-id=[test]", "timestamp_outside_five_minute_window"},
		{"verify failed: key-id[test] does not match serial number[other]", "public_key_id_mismatch"},
		{"key `Wechatpay-Serial` is empty in header", "missing_signature_header"},
		{"verify signature: crypto/rsa: verification error", "rsa_signature_mismatch"},
	} {
		if got := paymentVerificationReason(c.message); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
	var output bytes.Buffer
	writer := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(writer)
	response := &core.APIResult{Response: &http.Response{StatusCode: 200, Header: http.Header{"Wechatpay-Serial": []string{"PLATFORM123"}, "Wechatpay-Timestamp": []string{"1"}, "Wechatpay-Signature": []string{"SECRET_SIGNATURE"}}}}
	paymentResponseLog("prepay", "order", time.Now(), response, errors.New("validate: timestamp=[1] expires"), "")
	for _, want := range []string{`"wechatpay_serial":"PLATFORM123"`, `"clock_skew_seconds":`, `"local_timestamp":`, `"validation_reason":"timestamp_outside_five_minute_window"`} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("missing diagnostics %s in %s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "SECRET_SIGNATURE") {
		t.Fatal("signature leaked")
	}
}
