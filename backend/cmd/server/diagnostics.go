package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
)

var errResponseTooLarge = errors.New("response too large")

type callFailure struct {
	Code    string
	Message string
}

var diagnosticURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var diagnosticCredential = regexp.MustCompile(`(?i)(authorization|proxy-authorization|x-api-key|api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|secret)["']?\s*[:=]\s*["']?(?:Bearer\s+|Basic\s+)?[^\s,;"'}]+`)
var diagnosticBearer = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)

// Only selected structured error messages are retained, never whole responses,
// request parameters, headers, or transport errors containing request URLs.
func (a *App) describeCallFailure(provider Tool, err error, status int, body []byte) callFailure {
	failure := callFailure{}
	var timeout net.Error
	var dns *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()):
		failure = callFailure{"UPSTREAM_TIMEOUT", "上游请求超时，请核对调用记录和上游状态"}
	case errors.Is(err, context.Canceled):
		failure = callFailure{"REQUEST_CANCELED", "调用已取消或客户端连接已断开"}
	case errors.Is(err, errResponseTooLarge):
		failure = callFailure{"RESPONSE_TOO_LARGE", "上游响应超过 8 MiB 限制"}
	case errors.As(err, &dns):
		failure = callFailure{"UPSTREAM_DNS_ERROR", "无法解析上游域名，请检查上游地址和 DNS"}
	case isMCP(provider) && err != nil:
		failure = callFailure{"MCP_ERROR", a.mcpCallDetail(provider, err)}
	case err != nil && status != 0:
		failure = callFailure{"RESPONSE_READ_ERROR", "读取上游响应失败，连接可能已中断"}
	case err != nil:
		failure = callFailure{"UPSTREAM_CONNECTION_ERROR", "无法连接上游，请检查地址、网络和 TLS 配置"}
	default:
		failure = callFailure{"UPSTREAM_HTTP_ERROR", fmt.Sprintf("上游返回 HTTP %d %s", status, http.StatusText(status))}
		var payload struct {
			Message string          `json:"message"`
			Error   json.RawMessage `json:"error"`
		}
		if json.Unmarshal(body, &payload) == nil {
			detail := payload.Message
			if len(payload.Error) > 0 {
				var text string
				var nested struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(payload.Error, &text) == nil && text != "" {
					detail = text
				} else if json.Unmarshal(payload.Error, &nested) == nil && nested.Message != "" {
					detail = nested.Message
				}
			}
			if detail != "" {
				failure.Message += "：" + detail
			}
		}
	}
	// Reuse configured upstream key/address redaction and the 2000-character cap.
	failure.Message = a.mcpCallDetail(provider, &mcpCallError{detail: failure.Message})
	failure.Message = diagnosticURL.ReplaceAllString(failure.Message, "[upstream]")
	failure.Message = diagnosticCredential.ReplaceAllString(failure.Message, "[REDACTED]")
	failure.Message = diagnosticBearer.ReplaceAllString(failure.Message, "[REDACTED]")
	failure.Message = strings.TrimSpace(failure.Message)
	return failure
}
