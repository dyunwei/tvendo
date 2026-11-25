package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpTestBearer string

func (key mcpTestBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(key))
	return http.DefaultTransport.RoundTrip(r)
}

func exerciseMCPAgent(t *testing.T, h http.Handler, key, toolRef string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	r := httptest.NewRequest("POST", "http://localhost/mcp", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("MCP accepted an anonymous request: %d", w.Code)
	}
	r = httptest.NewRequest("POST", "http://wrong-host.example/mcp", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("unexpected Host accepted: %d", w.Code)
	}

	httpServer := httptest.NewServer(h)
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "tvendo-mcp-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL + "/mcp",
		HTTPClient: &http.Client{Transport: mcpTestBearer(key)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 5 {
		t.Fatalf("MCP tool list: %+v, %v", tools, err)
	}
	call := func(name string, args map[string]any, wantError bool) map[string]any {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result == nil || result.IsError != wantError {
			t.Fatalf("%s: %+v, %v", name, result, err)
		}
		body, ok := result.StructuredContent.(map[string]any)
		if !ok {
			t.Fatalf("%s returned invalid structured data: %v", name, result.StructuredContent)
		}
		return body
	}
	found := call("tvendo_search_tools", map[string]any{"query": "OK"}, false)
	if items, ok := found["data"].(map[string]any)["items"].([]any); !ok || len(items) != 1 || items[0].(map[string]any)["tool_ref"] != toolRef {
		t.Fatalf("MCP catalog ignored the API Key's tool permission: %v", found)
	}
	shown := call("tvendo_show_tool", map[string]any{"tool_ref": toolRef}, false)
	if shown["data"].(map[string]any)["price_micros"] != float64(1000000) {
		t.Fatalf("incorrect MCP quote: %v", shown)
	}
	args := map[string]any{
		"tool_ref": toolRef, "parameters": map[string]any{}, "max_cost_micros": 0, "idempotency_key": "test-mcp-unique-key",
	}
	if rejected := call("tvendo_run_tool", args, true); rejected["code"] != "COST_LIMIT_EXCEEDED" {
		t.Fatalf("MCP did not enforce the quoted price limit: %v", rejected)
	}
	args["max_cost_micros"] = 1000000
	run := call("tvendo_run_tool", args, false)
	replay := call("tvendo_run_tool", args, false)
	if run["id"] == nil || replay["id"] != run["id"] || run["cost_micros"] != float64(1000000) {
		t.Fatalf("MCP replay did not return the same run: %v / %v", run, replay)
	}
	result := call("tvendo_get_run", map[string]any{"run_id": run["id"], "include_result": true}, false)
	if result["id"] != run["id"] || result["data"].(map[string]any)["ok"] != true {
		t.Fatalf("incorrect restored MCP result: %v", result)
	}
}
