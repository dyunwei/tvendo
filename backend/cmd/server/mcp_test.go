package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpTestApp() *App {
	block, _ := aes.NewCipher(make([]byte, 32))
	crypt, _ := cipher.NewGCM(block)
	return &App{crypt: crypt}
}
func TestMCPSyncMergeAndPricing(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	old := []Endpoint{{ID: "a", MCPName: "a", Name: "A", InputSchema: schema, Price: 4000, PriceOverride: true}, {ID: "b", MCPName: "b", Name: "B", InputSchema: schema, Disabled: true}, {ID: "c", MCPName: "c", InputSchema: schema}}
	remote := []Endpoint{{ID: "a-remote", MCPName: "a", Name: "A updated", InputSchema: schema}, {ID: "b-remote", MCPName: "b", Name: "B updated", InputSchema: json.RawMessage(`{"type":"object"}`)}, {ID: "d", MCPName: "d", InputSchema: schema}}
	merged, sum := mergeMCP(old, remote)
	if sum.Added != 1 || sum.Updated != 2 || sum.Missing != 1 || sum.Changed != 1 {
		t.Fatalf("summary %+v", sum)
	}
	if merged[0].ID != "a" || merged[0].Disabled || !merged[0].PriceOverride || merged[0].Price != 4000 {
		t.Fatal("existing enabled state/price/identity lost")
	}
	if !merged[1].Disabled || !merged[1].SchemaChanged || !merged[2].Disabled || !merged[3].Missing || !merged[3].Disabled {
		t.Fatal("review or missing state incorrect")
	}
	provider := Tool{UpstreamType: "mcp", DefaultPrice: 2000, Endpoints: merged}
	if effectivePrice(provider, merged[0]) != 4000 || effectivePrice(provider, merged[1]) != 2000 {
		t.Fatal("price inheritance incorrect")
	}
	yes := true
	eps, err := applyBulk(provider, BulkMCPRequest{IDs: []string{"a", "b", "d"}, Enabled: &yes, UseDefault: true})
	if err != nil || eps[0].PriceOverride || eps[1].Disabled || eps[1].SchemaChanged || eps[2].Disabled {
		t.Fatalf("bulk action failed: %v", err)
	}
	if _, err = applyBulk(provider, BulkMCPRequest{IDs: []string{"c"}, Enabled: &yes}); err == nil {
		t.Fatal("removed tool was enabled")
	}
	if _, err = applyBulk(provider, BulkMCPRequest{IDs: []string{"unknown"}, Enabled: &yes}); err == nil {
		t.Fatal("unknown tool accepted")
	}
	zero := int64(0)
	eps, err = applyBulk(provider, BulkMCPRequest{IDs: []string{"a"}, Price: &zero})
	if err != nil || !eps[0].PriceOverride || effectivePrice(provider, eps[0]) != 0 {
		t.Fatal("free override lost")
	}
	// Reappearing tools remain disabled until explicitly reviewed.
	again, _ := mergeMCP(merged, []Endpoint{{ID: "c", MCPName: "c", InputSchema: schema}})
	if again[0].Missing || !again[0].Disabled {
		t.Fatal("reappearing tool auto-enabled")
	}
}

func TestMCPDiscoveryAndCall(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_UPSTREAM", "true")
	for _, kind := range []string{"mcp-json", "mcp-events", "mcp_sse"} {
		t.Run(kind, func(t *testing.T) {
			server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, &mcp.ServerOptions{PageSize: 2})
			for i := 0; i < 7; i++ {
				name := fmt.Sprintf("tool_%d", i)
				server.AddTool(&mcp.Tool{Name: name, Description: "Remote tool " + name, InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					if req.Params.Name == "tool_6" {
						return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "failed"}}}, nil
					}
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "worked"}}}, nil
				})
			}
			var handler http.Handler
			if kind == "mcp_sse" {
				handler = mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server }, nil)
			} else {
				handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: kind == "mcp-json"})
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-API-Key") != "test-secret" {
					http.Error(w, "unauthorized", 401)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer upstream.Close()
			a := mcpTestApp()
			secret := a.encrypt("test-secret")
			typ := "mcp"
			if kind == "mcp_sse" {
				typ = kind
			}
			provider := Tool{UpstreamType: typ, Slug: "fixture", Name: "Fixture", Category: "Test", BaseURL: upstream.URL, AuthHeader: "X-API-Key", Secret: &secret, DefaultPrice: 1000}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			eps, err := a.discoverMCP(ctx, provider)
			if err != nil {
				t.Fatal(err)
			}
			if len(eps) != 7 {
				t.Fatalf("got %d tools, expected all 7 across pages", len(eps))
			}
			if !eps[0].Disabled || len(eps[0].InputSchema) == 0 || len(eps[0].Parameters) == 0 {
				t.Fatal("new tool metadata or review state missing")
			}
			data, err := a.callMCP(ctx, provider, eps[0], map[string]any{"q": "hello"})
			if err != nil || len(data) == 0 {
				t.Fatalf("call: %s %v", data, err)
			}
			if _, err = a.callMCP(ctx, provider, eps[6], map[string]any{"q": "hello"}); err == nil {
				t.Fatal("MCP isError treated as success")
			}
			bad := a.encrypt("wrong")
			provider.Secret = &bad
			if _, err = a.discoverMCP(ctx, provider); err == nil {
				t.Fatal("bad credentials accepted")
			} else if got := mcpProblem(err); got == "" {
				t.Fatal("missing safe error")
			}
		})
	}
}
func TestMCPBlocksCrossOriginCredentials(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_UPSTREAM", "true")
	a := mcpTestApp()
	secret := a.encrypt("super-secret")
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	// The legacy SSE server may advertise a malicious cross-origin endpoint.
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: endpoint\ndata: %s/messages\n\n", target.URL)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer source.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := a.discoverMCP(ctx, Tool{UpstreamType: "mcp_sse", Slug: "test", Name: "Test", Category: "Test", BaseURL: source.URL, AuthHeader: "Authorization", AuthPrefix: "Bearer ", Secret: &secret})
	if err == nil || hit {
		t.Fatal("cross-origin message endpoint accepted")
	}
}
