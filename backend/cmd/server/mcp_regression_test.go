package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPExampleOmitsOptionalFilters(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["query"],"properties":{
 "query":{"type":"string","example":"Where is Southeast University in China?"},
 "fetch":{"type":"string","example":"{}"},
 "purpose":{"type":"string","minLength":1},
 "recency_minutes":{"type":"integer","minimum":1},
 "domain_type":{"type":"string","enum":["web","research_paper"]},
 "pub_year_min":{"type":"integer"},
 "include_thumbnail":{"type":"string","default":"false"}}}`)
	ep := Endpoint{InputSchema: schema, Parameters: json.RawMessage(`{"query":"","fetch":"","recency_minutes":0,"pub_year_min":0}`)}
	for _, kind := range []string{"mcp", "mcp_sse"} {
		example := endpointExample(Tool{UpstreamType: kind}, ep)
		var got map[string]any
		if err := json.Unmarshal(example, &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got["query"] != "Where is Southeast University in China?" {
			t.Fatalf("invalid minimal example: %s", example)
		}
	}
	if got := endpointExample(Tool{UpstreamType: "http"}, ep); string(got) != string(ep.Parameters) {
		t.Fatal("HTTP custom example changed")
	}
	if !strings.Contains(string(ep.Parameters), "recency_minutes") {
		t.Fatal("stored example mutated")
	}
}

func TestSchemaExampleNestedRequired(t *testing.T) {
	var schema map[string]any
	json.Unmarshal([]byte(`{"type":"object","required":["options","mode","limit"],"properties":{
 "options":{"type":"object","required":["name"],"properties":{"name":{"type":"string","examples":["demo"]},"optional":{"type":"boolean"}}},
 "mode":{"type":"string","enum":["first","second"]},
 "limit":{"type":"integer","minimum":1}}}`), &schema)
	got := schemaExample(schema)
	if string(got) != `{"limit":1,"mode":"first","options":{"name":"demo"}}` {
		t.Fatalf("got %s", got)
	}
}

func TestMCPCallDiagnostics(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_UPSTREAM", "true")
	block, _ := aes.NewCipher(make([]byte, 32))
	crypt, _ := cipher.NewGCM(block)
	a := &App{crypt: crypt}
	secret := a.encrypt("test-upstream-secret")
	for _, mode := range []string{"bad_request", "tool_error"} {
		t.Run(mode, func(t *testing.T) {
			remote := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			remote.AddTool(&mcp.Tool{Name: "search", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "fetch must be a valid JSON object; test-upstream-secret"}}}, nil
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, &mcp.StreamableHTTPOptions{JSONResponse: true})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Session initialization succeeds; only tools/call is rejected.
				if mode == "bad_request" && r.Method == "POST" {
					var body json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					r.Body.Close()
					r.Body = io.NopCloser(strings.NewReader(string(body)))
					var envelope struct {
						Method string          `json:"method"`
						ID     json.RawMessage `json:"id"`
					}
					json.Unmarshal(body, &envelope)
					if envelope.Method == "tools/call" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(400)
						json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": envelope.ID, "error": map[string]any{"code": -32602, "message": "recency_minutes must be >= 1; test-upstream-secret"}})
						return
					}
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			provider := Tool{Slug: "test", Name: "Test", Category: "MCP", UpstreamType: "mcp", BaseURL: server.URL, AuthHeader: "Authorization", AuthPrefix: "Bearer ", Secret: &secret}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := a.callMCP(ctx, provider, Endpoint{MCPName: "search"}, map[string]any{})
			if err == nil {
				t.Fatal("expected upstream failure")
			}
			detail := a.mcpCallDetail(provider, err)
			want := "fetch must be a valid JSON object"
			if mode == "bad_request" {
				want = "recency_minutes must be >= 1"
			}
			if !strings.Contains(detail, want) || strings.Contains(detail, "test-upstream-secret") {
				t.Fatalf("unsafe or missing diagnostic: %s", detail)
			}
		})
	}
	detail := a.mcpCallDetail(Tool{}, &mcpCallError{detail: strings.Repeat("错", 2100) + "\n"})
	if len([]rune(detail)) > 2001 || strings.Contains(detail, "\n") {
		t.Fatal("diagnostic not bounded")
	}
	if got := a.mcpCallDetail(Tool{}, errors.New("https://secret@host")); strings.Contains(got, "secret") {
		t.Fatal("transport error exposed")
	}
}
