package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpAccountContextKey struct{}

type mcpSearchInput struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

type mcpShowInput struct {
	Ref string `json:"tool_ref"`
}

type mcpRunInput struct {
	Ref        string                     `json:"tool_ref"`
	Parameters map[string]json.RawMessage `json:"parameters"`
	MaxCost    *int64                     `json:"max_cost_micros"`
	Key        string                     `json:"idempotency_key"`
}

type mcpRunRecordInput struct {
	RunID      int64  `json:"run_id"`
	RequestID  string `json:"request_id"`
	WithResult bool   `json:"include_result"`
}

func (a *App) mcpAgentHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "tvendo", Version: buildVersion}, &mcp.ServerOptions{
		Instructions: "Use tvendo_search_tools to find an endpoint and tvendo_show_tool to inspect its schema and price. " +
			"Obtain authorization for any paid action. Amounts are CNY micro-yuan (1 CNY = 1,000,000). " +
			"Keep the idempotency key for status lookup and do not automatically retry uncertain actions.",
	})
	addAgentMCPTool(server, "tvendo_search_tools", "Search enabled Tvendo tools. Returns prices, tool references and a pagination cursor.", true, `{
		"type":"object","additionalProperties":false,
		"properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":50},"cursor":{"type":"string"}}
	}`, func(w http.ResponseWriter, r *http.Request, u User, in mcpSearchInput) {
		q := url.Values{"q": {in.Query}}
		if in.Limit != 0 {
			q.Set("limit", strconv.Itoa(in.Limit))
		}
		if in.Cursor != "" {
			q.Set("cursor", in.Cursor)
		}
		r.URL.RawQuery = q.Encode()
		a.catalog(w, r, u)
	})
	addAgentMCPTool(server, "tvendo_show_tool", "Read the input schema, examples and current price for a tool reference.", true, `{
		"type":"object","required":["tool_ref"],"additionalProperties":false,
		"properties":{"tool_ref":{"type":"string"}}
	}`, func(w http.ResponseWriter, r *http.Request, u User, in mcpShowInput) {
		if _, _, err := parseToolRef(in.Ref); err != nil {
			fail(w, 400, "工具引用无效")
			return
		}
		r.SetPathValue("ref", in.Ref)
		a.catalog(w, r, u)
	})
	addAgentMCPTool(server, "tvendo_run_tool", "Execute a tool after inspecting its schema and price. This can charge the account and affect external systems. A unique idempotency key and an authorized maximum price are required.", false, `{
		"type":"object","required":["tool_ref","parameters","max_cost_micros","idempotency_key"],"additionalProperties":false,
		"properties":{
			"tool_ref":{"type":"string"},
			"parameters":{"type":"object","description":"Use the selected tool's input schema."},
			"max_cost_micros":{"type":"integer","minimum":0,"description":"Maximum approved cost in CNY micro-yuan."},
			"idempotency_key":{"type":"string","pattern":"^[A-Za-z0-9_-]{8,80}$","description":"Unique key for this action. Keep it to look up the outcome."}
		}
	}`, func(w http.ResponseWriter, r *http.Request, u User, in mcpRunInput) {
		if _, _, err := parseToolRef(in.Ref); err != nil {
			fail(w, 400, "工具引用无效")
			return
		}
		if in.Parameters == nil || in.MaxCost == nil || *in.MaxCost < 0 || !requestIDPattern.MatchString(in.Key) {
			fail(w, 400, "请提供参数对象、有效费用上限和幂等键")
			return
		}
		body, err := json.Marshal(map[string]any{"tool_ref": in.Ref, "parameters": in.Parameters, "max_cost_micros": in.MaxCost})
		if err != nil {
			fail(w, 400, "调用参数无效")
			return
		}
		r = newMCPRequest(r.Context(), http.MethodPost, body)
		r.Header.Set("Idempotency-Key", in.Key)
		r.Header.Set("X-Request-ID", in.Key)
		a.runAsUser(w, r, u)
	})
	addAgentMCPTool(server, "tvendo_get_run", "Look up a call by run ID or request ID. When a charge might have occurred, check this record before taking another action.", true, `{
		"type":"object","additionalProperties":false,
		"properties":{"run_id":{"type":"integer","minimum":1},"request_id":{"type":"string"},"include_result":{"type":"boolean"}}
	}`, func(w http.ResponseWriter, r *http.Request, u User, in mcpRunRecordInput) {
		if in.RunID > 0 && in.RequestID == "" {
			r.SetPathValue("id", strconv.FormatInt(in.RunID, 10))
			if in.WithResult {
				a.getRunResult(w, r, u)
			} else {
				a.agentRuns(w, r, u)
			}
			return
		}
		if in.RunID != 0 || !requestIDPattern.MatchString(in.RequestID) || in.WithResult {
			fail(w, 400, "请指定调用编号或请求标识，恢复结果时需使用调用编号")
			return
		}
		r.URL.RawQuery = url.Values{"request_id": {in.RequestID}}.Encode()
		a.agentRuns(w, r, u)
	})
	addAgentMCPTool(server, "tvendo_wallet_balance", "Check the current Tvendo account's available balance.", true, `{
		"type":"object","additionalProperties":false
	}`, func(w http.ResponseWriter, r *http.Request, u User, _ struct{}) {
		a.agentBalance(w, r, u)
	})

	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20,
		// Check the configured external Host before calling the SDK, including for loopback reverse proxies.
		DisableLocalhostProtection: true,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.validMCPHost(r.Host) || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != a.publicURL) {
			fail(w, 403, "MCP 服务地址或来源不匹配")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			fail(w, 405, "MCP 使用 Streamable HTTP POST")
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		user, ok := a.apiUser(w, r)
		if !ok {
			return
		}
		w.Header().Del("WWW-Authenticate")
		if !a.rateAllowed(w, r, requestLimits("mcp", user)...) {
			return
		}
		transport.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpAccountContextKey{}, user)))
	})
}

func (a *App) validMCPHost(host string) bool {
	origin, err := url.Parse(a.publicURL)
	if err == nil && origin.Host != "" && strings.EqualFold(host, origin.Host) {
		return true
	}
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

type mcpAPIResponse struct {
	headers http.Header
	body    bytes.Buffer
	status  int
}

func (r *mcpAPIResponse) Header() http.Header            { return r.headers }
func (r *mcpAPIResponse) WriteHeader(status int)         { r.status = status }
func (r *mcpAPIResponse) Write(body []byte) (int, error) { return r.body.Write(body) }

func newMCPRequest(ctx context.Context, method string, body []byte) *http.Request {
	r, _ := http.NewRequestWithContext(ctx, method, "http://localhost/", bytes.NewReader(body))
	return r
}

func addAgentMCPTool[T any](s *mcp.Server, name, description string, readOnly bool, schema string, run func(http.ResponseWriter, *http.Request, User, T)) {
	s.AddTool(&mcp.Tool{
		Name: name, Description: description, InputSchema: json.RawMessage(schema),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		user, ok := ctx.Value(mcpAccountContextKey{}).(User)
		if !ok {
			return mcpToolError("未通过 API Key 验证"), nil
		}
		var input T
		if raw := bytes.TrimSpace(req.Params.Arguments); len(raw) > 0 {
			if raw[0] != '{' {
				return mcpToolError("工具参数须为 JSON 对象"), nil
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				return mcpToolError("工具参数格式错误"), nil
			}
		}
		response := &mcpAPIResponse{headers: make(http.Header)}
		run(response, newMCPRequest(ctx, http.MethodGet, nil), user, input)
		if !json.Valid(response.body.Bytes()) {
			return mcpToolError("服务未返回有效结果"), nil
		}
		return &mcp.CallToolResult{
			IsError:           response.status >= 400,
			Content:           []mcp.Content{&mcp.TextContent{Text: response.body.String()}},
			StructuredContent: json.RawMessage(response.body.Bytes()),
		}, nil
	})
}

func mcpToolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}
