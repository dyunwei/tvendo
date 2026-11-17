package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wechatpay-apiv3/wechatpay-go/core"
)

// Log only explicitly selected metadata. SDK error strings can contain full request
// headers or decrypted bodies, so never log them directly.
func paymentLog(event string, fields map[string]any) {
	fields["event"] = event
	for key, value := range fields {
		if s, ok := value.(string); ok {
			fields[key] = safePaymentLogText(s)
		}
	}
	data, err := json.Marshal(fields)
	if err == nil {
		log.Printf("wechat_pay %s", data)
	}
}
func safePaymentLogText(s string) string {
	for _, key := range []string{"WECHAT_PAY_API_V3_KEY", "ENCRYPTION_KEY", "ADMIN_PASSWORD"} {
		if secret := os.Getenv(key); secret != "" {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
	}
	if len(s) > 512 {
		s = s[:512] + "…"
	}
	return s
}
func paymentErrorFields(err error) map[string]any {
	fields := map[string]any{"error": "internal_error"}
	var apiErr *core.APIError
	var network net.Error
	var dbErr *pgconn.PgError
	switch {
	case errors.As(err, &apiErr):
		fields["error"] = "wechat_api_error"
		fields["error_code"] = apiErr.Code
		fields["error_message"] = apiErr.Message
		fields["http_status"] = apiErr.StatusCode
		fields["request_id"] = apiErr.Header.Get("Request-ID")
	case errors.As(err, &dbErr):
		fields["error"] = "database_error"
		fields["sqlstate"] = dbErr.Code
	case errors.Is(err, context.Canceled):
		fields["error"] = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		fields["error"] = "timeout"
	case errors.As(err, &network):
		if network.Timeout() {
			fields["error"] = "timeout"
		} else {
			fields["error"] = "network_error"
		}
	default:
		// The notification SDK embeds request headers in validation errors.
		// Match the stage but deliberately discard the original error string.
		message := err.Error()
		switch {
		case strings.Contains(message, "invalid notification"), strings.Contains(message, "validate"), strings.Contains(message, "signature"), strings.Contains(message, "certificate"), strings.Contains(message, "serial"):
			fields["error"] = "signature_or_certificate_validation_failed"
			fields["validation_reason"] = paymentVerificationReason(message)
		case strings.Contains(message, "decrypt error"):
			fields["error"] = "notification_decryption_failed"
		case strings.Contains(message, "unmarshal"), strings.Contains(message, "parse request body"), strings.Contains(message, "read request body"):
			fields["error"] = "invalid_response_or_notification_body"
		default:
			for _, safe := range []string{"payment identity mismatch", "payment amount mismatch", "payment is not successful", "invalid payment success time", "payment transaction mismatch", "invalid WeChat payment URL", "payment merchant configuration changed"} {
				if message == safe {
					fields["error"] = safe
					break
				}
			}
		}
	}
	return fields
}
func paymentLogFailure(event, orderID string, err error) {
	fields := paymentErrorFields(err)
	fields["order_id"] = orderID
	paymentLog(event, fields)
}
func paymentResponseLog(operation, orderID string, started time.Time, result *core.APIResult, err error, state string) {
	fields := map[string]any{"operation": operation, "order_id": orderID, "duration_ms": time.Since(started).Milliseconds(), "ok": err == nil}
	if result != nil && result.Response != nil {
		fields["http_status"] = result.Response.StatusCode
		fields["request_id"] = result.Response.Header.Get("Request-ID")
		fields["wechatpay_serial"] = result.Response.Header.Get("Wechatpay-Serial")
		fields["wechatpay_timestamp"] = result.Response.Header.Get("Wechatpay-Timestamp")
		fields["local_timestamp"] = time.Now().Unix()
		if timestamp, e := strconv.ParseInt(result.Response.Header.Get("Wechatpay-Timestamp"), 10, 64); e == nil {
			fields["clock_skew_seconds"] = time.Now().Unix() - timestamp
		}
	}
	if state != "" {
		fields["trade_state"] = state
	}
	if err != nil {
		for k, v := range paymentErrorFields(err) {
			fields[k] = v
		}
	}
	paymentLog("response", fields)
}

// Fixed reason codes preserve useful diagnostics without exposing SDK error text.
func paymentVerificationReason(message string) string {
	switch {
	case strings.Contains(message, "unknown WeChat Pay platform certificate serial"), strings.Contains(message, "not found in verifier"):
		return "unknown_platform_certificate_serial"
	case strings.Contains(message, "outside its validity period"):
		return "certificate_outside_validity_period"
	case strings.Contains(message, "timestamp=") && strings.Contains(message, "expires"):
		return "timestamp_outside_five_minute_window"
	case strings.Contains(message, "does not match serial number"):
		return "public_key_id_mismatch"
	case strings.Contains(message, "is empty in header"):
		return "missing_signature_header"
	case strings.Contains(message, "not base64 encoded"):
		return "invalid_signature_encoding"
	case strings.Contains(message, "verification error"):
		return "rsa_signature_mismatch"
	default:
		return "signature_or_certificate_validation_failed"
	}
}
