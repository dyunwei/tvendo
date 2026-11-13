package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func isMCP(t Tool) bool { return t.UpstreamType == "mcp" || t.UpstreamType == "mcp_sse" }
func effectivePrice(t Tool, ep Endpoint) int64 {
	if isMCP(t) && !ep.PriceOverride {
		return t.DefaultPrice
	}
	return ep.Price
}
func (a *App) provider(ctx context.Context, id string) (Tool, error) {
	var raw []byte
	var t Tool
	err := a.db.QueryRow(ctx, `SELECT to_jsonb(t) FROM tools t WHERE id=$1`, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &t)
	}
	return t, err
}

type mcpHTTPStatus struct{ code int }

func (e *mcpHTTPStatus) Error() string { return fmt.Sprintf("MCP HTTP %d", e.code) }

type mcpTransport struct {
	base                http.RoundTripper
	origin              *url.URL
	header, prefix, key string
}

func (t *mcpTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Legacy SSE may advertise a message URL. Never send secrets to another origin.
	if r.URL.Scheme != t.origin.Scheme || r.URL.Host != t.origin.Host {
		return nil, errors.New("MCP cross-origin endpoint rejected")
	}
	req := r.Clone(r.Context())
	req.Header = r.Header.Clone()
	if t.key != "" {
		req.Header.Set(t.header, t.prefix+t.key)
	}
	res, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == 401 || res.StatusCode == 403 {
		res.Body.Close()
		return nil, &mcpHTTPStatus{res.StatusCode}
	}
	res.Body = &mcpLimitedBody{ReadCloser: res.Body, remaining: 8 << 20}
	return res, nil
}

type mcpLimitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *mcpLimitedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, errors.New("MCP response exceeds 8 MB")
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, e := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, e
}
func (a *App) connectMCP(ctx context.Context, t Tool) (*mcp.ClientSession, error) {
	if !isMCP(t) || t.BaseURL == "" {
		return nil, errors.New("MCP endpoint missing")
	}
	if err := validateTool(t); err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(t.BaseURL)
	if err != nil {
		return nil, err
	}
	key := ""
	if t.Secret != nil && *t.Secret != "" {
		key, err = a.decrypt(*t.Secret)
		if err != nil {
			return nil, err
		}
	}
	transport := &http.Transport{DialContext: safeDial, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 55 * time.Second, DisableKeepAlives: true}
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &mcpTransport{transport, endpoint, t.AuthHeader, t.AuthPrefix, key}}
	c := mcp.NewClient(&mcp.Implementation{Name: "tvendo", Version: "0.2.0"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	var wire mcp.Transport
	if t.UpstreamType == "mcp_sse" {
		wire = &mcp.SSEClientTransport{Endpoint: t.BaseURL, HTTPClient: client}
	} else {
		wire = &mcp.StreamableClientTransport{Endpoint: t.BaseURL, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true}
	}
	return c.Connect(ctx, wire, nil)
}
func mcpProblem(err error) string {
	var status *mcpHTTPStatus
	if errors.As(err, &status) {
		return fmt.Sprintf("MCP 鉴权失败（HTTP %d），请检查 Header、密钥前缀和 Key", status.code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "MCP 请求超时，请检查上游服务"
	}
	return "MCP 连接或同步失败，请核对地址、鉴权和传输类型；原有工具未修改"
}
func (a *App) discoverMCP(ctx context.Context, t Tool) ([]Endpoint, error) {
	session, err := a.connectMCP(ctx, t)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	out := []Endpoint{}
	seen := map[string]bool{}
	cursors := map[string]bool{}
	cursor := ""
	for page := 0; page < 100; page++ {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, remote := range result.Tools {
			if seen[remote.Name] {
				return nil, errors.New("duplicate remote tool")
			}
			seen[remote.Name] = true
			schema, err := json.Marshal(remote.InputSchema)
			if err != nil || len(schema) > 256<<10 {
				return nil, errors.New("invalid or oversized input schema")
			}
			var shape map[string]any
			if json.Unmarshal(schema, &shape) != nil || shape["type"] != "object" {
				return nil, errors.New("MCP input schema must be object")
			}
			name := remote.Title
			if name == "" {
				name = remote.Name
			}
			out = append(out, Endpoint{ID: "mcp-" + hash(remote.Name)[:32], MCPName: remote.Name, Name: name, Path: remote.Name, Method: "MCP", Description: remote.Description, InputSchema: schema, Parameters: schemaExample(shape), Disabled: true})
			if len(out) > 5000 {
				return nil, errors.New("MCP catalog exceeds 5000 tools")
			}
		}
		pageData, _ := json.Marshal(out)
		if len(pageData) > 16<<20 {
			return nil, errors.New("MCP catalog exceeds 16 MB")
		}
		if result.NextCursor == "" {
			data, _ := json.Marshal(out)
			if len(data) > 16<<20 {
				return nil, errors.New("MCP catalog exceeds 16 MB")
			}
			return out, nil
		}
		cursor = result.NextCursor
		if cursors[cursor] {
			return nil, errors.New("repeated pagination cursor")
		}
		cursors[cursor] = true
	}
	return nil, errors.New("MCP pagination exceeds 100 pages")
}
func schemaExample(s map[string]any) json.RawMessage {
	props, _ := s["properties"].(map[string]any)
	out := map[string]any{}
	for key, value := range props {
		p, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := p["default"]; ok {
			out[key] = v
			continue
		}
		if v, ok := p["enum"].([]any); ok && len(v) > 0 {
			out[key] = v[0]
			continue
		}
		switch p["type"] {
		case "string":
			out[key] = ""
		case "integer", "number":
			out[key] = 0
		case "boolean":
			out[key] = false
		case "array":
			out[key] = []any{}
		case "object":
			out[key] = map[string]any{}
		}
	}
	b, _ := json.Marshal(out)
	return b
}
func sameSchema(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	p, _ := json.Marshal(x)
	q, _ := json.Marshal(y)
	return string(p) == string(q)
}

type SyncSummary struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Missing int `json:"missing"`
	Changed int `json:"changed"`
}

func mergeMCP(old, remote []Endpoint) ([]Endpoint, SyncSummary) {
	prior := map[string]Endpoint{}
	for _, ep := range old {
		prior[ep.MCPName] = ep
	}
	seen := map[string]bool{}
	out := []Endpoint{}
	summary := SyncSummary{}
	for _, ep := range remote {
		seen[ep.MCPName] = true
		if was, ok := prior[ep.MCPName]; ok {
			ep.ID = was.ID
			ep.Price = was.Price
			ep.PriceOverride = was.PriceOverride
			ep.Disabled = was.Disabled
			ep.SchemaChanged = was.SchemaChanged || !sameSchema(was.InputSchema, ep.InputSchema)
			if ep.SchemaChanged {
				summary.Changed++
			}
			summary.Updated++
		} else {
			ep.Disabled = true
			summary.Added++
		}
		out = append(out, ep)
	}
	for _, ep := range old {
		if !seen[ep.MCPName] {
			ep.Missing = true
			ep.Disabled = true
			out = append(out, ep)
			summary.Missing++
		}
	}
	return out, summary
}
func (a *App) syncMCP(w http.ResponseWriter, r *http.Request, u User) {
	t, err := a.provider(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, 404, "上游不存在")
		return
	}
	if !isMCP(t) {
		fail(w, 400, "请先保存为 MCP 上游类型")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	remote, err := a.discoverMCP(ctx, t)
	if err != nil {
		fail(w, 502, mcpProblem(err))
		return
	}
	endpoints, summary := mergeMCP(t.Endpoints, remote)
	data, _ := json.Marshal(endpoints)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		fail(w, 500, "同步保存失败")
		return
	}
	defer tx.Rollback(r.Context())
	result, err := tx.Exec(r.Context(), `UPDATE tools SET endpoints=$2,last_synced_at=now(),revision=revision+1 WHERE id=$1 AND revision=$3`, t.ID, data, t.Revision)
	if err != nil {
		fail(w, 500, "同步保存失败")
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 409, "同步期间配置有变化，请重新同步")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_log(actor_id,action,target_id) VALUES($1,'sync_mcp',$2)`, u.ID, t.ID); err != nil || tx.Commit(r.Context()) != nil {
		fail(w, 500, "同步保存失败")
		return
	}
	writeJSON(w, 200, summary)
}

type BulkMCPRequest struct {
	Revision   int64    `json:"revision"`
	IDs        []string `json:"ids"`
	Enabled    *bool    `json:"enabled"`
	Price      *int64   `json:"price_micros"`
	UseDefault bool     `json:"use_default"`
}

func applyBulk(t Tool, b BulkMCPRequest) ([]Endpoint, error) {
	if len(b.IDs) == 0 || len(b.IDs) > 5000 {
		return nil, errors.New("请选择 1–5000 个工具")
	}
	if b.Price != nil && (*b.Price < 0 || *b.Price > 1000000000) || b.Price != nil && b.UseDefault {
		return nil, errors.New("售价需在 0–1000 元内，不能同时设置单独售价和默认售价")
	}
	if b.Enabled == nil && b.Price == nil && !b.UseDefault {
		return nil, errors.New("请选择操作")
	}
	selected := map[string]bool{}
	for _, id := range b.IDs {
		selected[id] = true
	}
	out := append([]Endpoint(nil), t.Endpoints...)
	for i := range out {
		ep := &out[i]
		if !selected[ep.ID] {
			continue
		}
		delete(selected, ep.ID)
		if b.Enabled != nil {
			if *b.Enabled && ep.Missing {
				return nil, errors.New("不能启用上游已删除的工具")
			}
			ep.Disabled = !*b.Enabled
			if *b.Enabled {
				ep.SchemaChanged = false
			}
		}
		if b.Price != nil {
			ep.Price = *b.Price
			ep.PriceOverride = true
		}
		if b.UseDefault {
			ep.PriceOverride = false
		}
	}
	if len(selected) > 0 {
		return nil, errors.New("部分工具已不存在，请刷新")
	}
	return out, nil
}
func (a *App) bulkMCP(w http.ResponseWriter, r *http.Request, u User) {
	var b BulkMCPRequest
	if !decode(w, r, &b) {
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		fail(w, 500, "更新失败")
		return
	}
	defer tx.Rollback(r.Context())
	var raw []byte
	var t Tool
	if tx.QueryRow(r.Context(), `SELECT to_jsonb(t) FROM tools t WHERE id=$1 FOR UPDATE`, r.PathValue("id")).Scan(&raw) != nil || json.Unmarshal(raw, &t) != nil {
		fail(w, 404, "上游不存在")
		return
	}
	if !isMCP(t) {
		fail(w, 400, "批量管理仅适用于 MCP 上游")
		return
	}
	if b.Revision != t.Revision {
		fail(w, 409, "配置已更新，请刷新工具列表")
		return
	}
	endpoints, err := applyBulk(t, b)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	data, _ := json.Marshal(endpoints)
	if _, err = tx.Exec(r.Context(), `UPDATE tools SET endpoints=$2,revision=revision+1 WHERE id=$1`, t.ID, data); err != nil {
		fail(w, 500, "更新失败")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_log(actor_id,action,target_id) VALUES($1,'bulk_mcp',$2)`, u.ID, t.ID); err != nil || tx.Commit(r.Context()) != nil {
		fail(w, 500, "更新失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *App) callMCP(ctx context.Context, t Tool, ep Endpoint, args map[string]any) ([]byte, error) {
	session, err := a.connectMCP(ctx, t)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: ep.MCPName, Arguments: args})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, errors.New("MCP tool execution failed")
	}
	data, err := json.Marshal(result)
	if len(data) > 8<<20 {
		return nil, errors.New("MCP result exceeds 8 MB")
	}
	return data, err
}
