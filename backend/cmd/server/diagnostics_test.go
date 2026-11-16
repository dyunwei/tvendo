package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestCallFailureDiagnostics(t *testing.T) {
	a := &App{}
	for _, tc := range []struct {
		name         string
		provider     Tool
		err          error
		status       int
		body         string
		code, detail string
	}{
		{name: "timeout", err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded), code: "UPSTREAM_TIMEOUT", detail: "超时"},
		{name: "canceled", err: context.Canceled, code: "REQUEST_CANCELED", detail: "取消"},
		{name: "dns", err: &net.DNSError{Err: "no such host", Name: "private.example"}, code: "UPSTREAM_DNS_ERROR", detail: "DNS"},
		{name: "connection", err: &url.Error{Op: "Post", URL: "https://secret:password@example.com?key=hidden", Err: errors.New("connection refused")}, code: "UPSTREAM_CONNECTION_ERROR", detail: "无法连接"},
		{name: "read", err: errors.New("unexpected EOF"), status: 200, code: "RESPONSE_READ_ERROR", detail: "读取"},
		{name: "size", err: errResponseTooLarge, status: 200, code: "RESPONSE_TOO_LARGE", detail: "8 MiB"},
		{name: "http", status: 401, body: `{"error":{"message":"invalid credential"}}`, code: "UPSTREAM_HTTP_ERROR", detail: "invalid credential"},
		{name: "http string", status: 500, body: `{"error":"service unavailable"}`, code: "UPSTREAM_HTTP_ERROR", detail: "service unavailable"},
		{name: "http message", status: 400, body: `{"message":"missing required field"}`, code: "UPSTREAM_HTTP_ERROR", detail: "missing required field"},
		{name: "html omitted", status: 502, body: `<html>private upstream response</html>`, code: "UPSTREAM_HTTP_ERROR", detail: "HTTP 502"},
		{name: "mcp", provider: Tool{UpstreamType: "mcp"}, err: &mcpCallError{detail: "MCP 工具执行失败: missing parameter"}, code: "MCP_ERROR", detail: "missing parameter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := a.describeCallFailure(tc.provider, tc.err, tc.status, []byte(tc.body))
			if got.Code != tc.code || !strings.Contains(got.Message, tc.detail) {
				t.Fatalf("unexpected diagnostic: %+v", got)
			}
			for _, private := range []string{"private.example", "password", "hidden", "private upstream response"} {
				if strings.Contains(got.Message, private) {
					t.Fatalf("private data retained: %+v", got)
				}
			}
		})
	}
}

func TestCallFailureRedaction(t *testing.T) {
	a := &App{}
	got := a.describeCallFailure(Tool{BaseURL: "https://example.com"}, nil, 400,
		[]byte(`{"message":"invalid api_key=hidden token: tok123 Authorization: Bearer abc123 password=pwd123 https://user:pass@elsewhere.example/path?secret=value\nnext"}`))
	for _, private := range []string{"hidden", "tok123", "abc123", "pwd123", "user:pass", "secret=value", "\n"} {
		if strings.Contains(got.Message, private) {
			t.Fatalf("secret retained: %s", got.Message)
		}
	}
	got = a.describeCallFailure(Tool{}, nil, 500, []byte(`{"message":"`+strings.Repeat("错", 3000)+`"}`))
	if len([]rune(got.Message)) > 2001 {
		t.Fatalf("unbounded diagnostic: %d", len([]rune(got.Message)))
	}
}
