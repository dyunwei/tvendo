package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/redis/go-redis/v9"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateTool(t *testing.T) {
	valid := Tool{Slug: "search", Name: "Search", Category: "Data", BaseURL: "https://example.com/v1", Enabled: true, AuthHeader: "Authorization", Endpoints: []Endpoint{{ID: "search", Name: "Search", Method: "POST", Path: "/search", Price: 1000}}}
	if e := validateTool(valid); e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name string
		edit func(*Tool)
	}{
		{"negative price", func(v *Tool) { v.Endpoints[0].Price = -1 }},
		{"relative path", func(v *Tool) { v.Endpoints[0].Path = "//evil.example/path" }},
		{"path traversal", func(v *Tool) { v.Endpoints[0].Path = "/../secret" }},
		{"duplicate endpoints", func(v *Tool) { v.Endpoints = append(v.Endpoints, v.Endpoints[0]) }},
		{"unsafe website", func(v *Tool) { v.Website = "javascript:alert(1)" }},
		{"header injection", func(v *Tool) { v.AuthPrefix = "Bearer\r\nInjected: true" }},
		{"missing URL", func(v *Tool) { v.BaseURL = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := valid
			v.Endpoints = append([]Endpoint{}, valid.Endpoints...)
			c.edit(&v)
			if validateTool(v) == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
func TestSafeDialRejectsPrivate(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_UPSTREAM", "false")
	for _, addr := range []string{"127.0.0.1:80", "[::1]:80", "10.0.0.1:443", "169.254.169.254:80"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, e := safeDial(ctx, "tcp", addr)
		cancel()
		if e == nil {
			t.Fatalf("private address accepted: %s", addr)
		}
	}
}

// Opt-in: use a dedicated empty database and dedicated Redis instance.
func TestIntegrationBilling(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	redisURL := os.Getenv("TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_REDIS_URL for integration tests")
	}
	ctx := context.Background()
	db, e := pgxpool.New(ctx, dbURL)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	opt, e := redis.ParseURL(redisURL)
	if e != nil {
		t.Fatal(e)
	}
	rc := redis.NewClient(opt)
	defer rc.Close()
	var exists bool
	if e = db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='users')`).Scan(&exists); e != nil {
		t.Fatal(e)
	}
	if exists {
		t.Fatal("integration tests require an empty dedicated database")
	}
	t.Setenv("ADMIN_EMAIL", "admin@test.local")
	t.Setenv("ADMIN_PASSWORD", "test-admin-password-1234")
	t.Setenv("ALLOW_PRIVATE_UPSTREAM", "true")
	block, _ := aes.NewCipher(make([]byte, 32))
	crypt, _ := cipher.NewGCM(block)
	a := &App{db: db, redis: rc, crypt: crypt, publicURL: "http://test.local", client: &http.Client{Timeout: time.Second}}
	if e = a.bootstrap(ctx); e != nil {
		t.Fatal(e)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "upstream-secret" {
			t.Error("upstream secret missing")
		}
		if r.URL.Path == "/fail" {
			w.WriteHeader(500)
			return
		}
		time.Sleep(15 * time.Millisecond)
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	defer upstream.Close()
	h := a.routes()
	request := func(method, path string, body any, cookie *http.Cookie, csrf, key string) (int, map[string]any, []*http.Cookie) {
		var buf bytes.Buffer
		if body != nil {
			json.NewEncoder(&buf).Encode(body)
		}
		r := httptest.NewRequest(method, path, &buf)
		r.RemoteAddr = "192.0.2.1:1234"
		if cookie != nil {
			r.AddCookie(cookie)
		}
		r.Header.Set("X-CSRF-Token", csrf)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("X-Tvendo-Currency") != "CNY" {
			t.Error("API currency header missing")
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, w.Result().Cookies()
	}
	require := func(got, want int, out any) {
		t.Helper()
		if got != want {
			t.Fatalf("status %d; want %d; response %v", got, want, out)
		}
	}
	code, out, cookies := request("POST", "/api/auth/login", map[string]string{"email": "admin@test.local", "password": "test-admin-password-1234"}, nil, "", "")
	require(code, 200, out)
	adminCookie := cookies[0]
	adminCSRF := out["csrf"].(string)
	code, out, cookies = request("POST", "/api/auth/register", map[string]string{"email": "user@test.local", "name": "Test User", "password": "test-user-password-1234"}, nil, "", "")
	require(code, 200, out)
	userCookie := cookies[0]
	userCSRF := out["csrf"].(string)
	userID := int64(out["user"].(map[string]any)["id"].(float64))
	code, out, _ = request("GET", "/api/admin/users", nil, userCookie, "", "")
	require(code, 403, out)
	code, out, _ = request("POST", "/api/keys", map[string]string{"label": "bad csrf"}, userCookie, "", "")
	require(code, 403, out)
	code, out, _ = request("POST", "/api/keys", map[string]string{"label": "agent"}, userCookie, userCSRF, "")
	require(code, 201, out)
	apiKey := out["key"].(string)
	keyID := out["id"]
	secret := "upstream-secret"
	tool := Tool{Slug: "test", Name: "Test", Category: "Testing", BaseURL: upstream.URL, AuthHeader: "X-API-Key", Secret: &secret, Enabled: true, Endpoints: []Endpoint{{ID: "ok", Name: "OK", Path: "/ok", Method: "POST", Price: 1000000, Parameters: json.RawMessage(`{}`)}, {ID: "fail", Name: "Fail", Path: "/fail", Method: "POST", Price: 1000000, Parameters: json.RawMessage(`{}`)}}}
	code, out, _ = request("POST", "/api/admin/tools", tool, adminCookie, adminCSRF, "")
	require(code, 200, out)
	testProviderID := int64(out["id"].(float64))
	var stored string
	if e = db.QueryRow(ctx, `SELECT secret FROM tools WHERE slug='test'`).Scan(&stored); e != nil || stored == secret || strings.Contains(stored, secret) {
		t.Fatal("secret not encrypted")
	}
	decoded, e := a.decrypt(stored)
	if e != nil || decoded != secret {
		t.Fatal("secret encryption roundtrip failed")
	}
	code, out, _ = request("POST", "/api/admin/topup", map[string]any{"user_id": userID, "amount": 5500000, "note": "test deposit"}, adminCookie, adminCSRF, "")
	require(code, 200, out)
	runBody := func(ep string) map[string]any {
		return map[string]any{"tool": "test", "endpoint": ep, "parameters": map[string]any{}}
	}
	code, out, _ = request("GET", "/v1/catalog?q=OK&limit=1", nil, nil, "", apiKey)
	require(code, 200, out)
	catalog := out["data"].(map[string]any)
	if catalog["currency"] != "CNY" {
		t.Fatal("catalog currency mismatch")
	}
	if len(catalog["items"].([]any)) != 1 {
		t.Fatal("tool search failed")
	}
	ref := catalog["items"].([]any)[0].(map[string]any)["tool_ref"].(string)
	code, out, _ = request("GET", "/v1/catalog/"+ref, nil, nil, "", apiKey)
	require(code, 200, out)
	if out["data"].(map[string]any)["currency"] != "CNY" {
		t.Fatal("detail currency mismatch")
	}
	if out["data"].(map[string]any)["price_micros"] != float64(1000000) {
		t.Fatal("catalog price mismatch")
	}
	for _, body := range []any{
		map[string]any{"tool_ref": ref, "max_cost_micros": 999999},
		map[string]any{"tool_ref": ref, "max_cost_micros": -1},
		map[string]any{"tool_ref": ref, "tool": "test", "endpoint": "ok"},
	} {
		code, out, _ = request("POST", "/v1/run", body, nil, "", apiKey)
		if code != 400 && code != 403 {
			t.Fatalf("invalid run accepted: %d %v", code, out)
		}
	}
	var preflightRuns int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM usage WHERE user_id=$1`, userID).Scan(&preflightRuns); err != nil || preflightRuns != 0 {
		t.Fatal("preflight rejection created run")
	}
	code, out, _ = request("POST", "/v1/run", map[string]any{"tool_ref": toolRef(testProviderID, "fail"), "parameters": map[string]any{}, "max_cost_micros": 1000000}, nil, "", apiKey)
	require(code, 502, out)
	failedID := int64(out["id"].(float64))
	requestID := out["request_id"].(string)
	if out["currency"] != "CNY" {
		t.Fatal("refund currency mismatch")
	}
	if out["settlement"] != "refunded" {
		t.Fatal("refund state missing")
	}
	code, out, _ = request("GET", "/v1/runs?request_id="+requestID, nil, nil, "", apiKey)
	require(code, 200, out)
	records := out["data"].(map[string]any)["items"].([]any)
	if len(records) != 1 || records[0].(map[string]any)["currency"] != "CNY" || records[0].(map[string]any)["status"] != "failed" {
		t.Fatal("request correlation failed")
	}
	code, out, _ = request("GET", fmt.Sprintf("/v1/runs/%d", failedID), nil, nil, "", apiKey)
	require(code, 200, out)
	code, out, _ = request("GET", "/v1/balance", nil, nil, "", apiKey)
	require(code, 200, out)
	if out["data"].(map[string]any)["currency"] != "CNY" {
		t.Fatal("balance currency mismatch")
	}
	if out["data"].(map[string]any)["balance_micros"] != float64(5500000) {
		t.Fatal("API balance mismatch")
	}
	code, out, _ = request("GET", "/v1/budgets", nil, nil, "", apiKey)
	require(code, 200, out)
	if out["data"].(map[string]any)["currency"] != "CNY" {
		t.Fatal("budget currency mismatch")
	}
	if out["data"].(map[string]any)["resets_at"] == nil {
		t.Fatal("budget reset absent")
	}
	code, out, cookies = request("POST", "/api/auth/register", map[string]string{"email": "other@test.local", "name": "Other", "password": "test-other-password-1234"}, nil, "", "")
	require(code, 200, out)
	otherCSRF := out["csrf"].(string)
	code, out, _ = request("POST", "/api/keys", map[string]string{"label": "other"}, cookies[0], otherCSRF, "")
	require(code, 201, out)
	otherKey := out["key"].(string)
	code, out, _ = request("GET", fmt.Sprintf("/v1/runs/%d", failedID), nil, nil, "", otherKey)
	require(code, 404, out)
	code, out, _ = request("GET", "/v1/runs?request_id="+requestID, nil, nil, "", otherKey)
	require(code, 200, out)
	if len(out["data"].(map[string]any)["items"].([]any)) != 0 {
		t.Fatal("another user's records leaked")
	}

	u, _ := a.user(ctx, userID)
	if u.Balance != 5500000 {
		t.Fatal("failed upstream did not refund")
	}
	code, out, _ = request("PUT", "/api/budgets", map[string]any{"daily_limit": 500000, "daily_enabled": true, "call_limit": 2000000, "call_enabled": true}, userCookie, userCSRF, "")
	require(code, 200, out)
	code, out, _ = request("POST", "/v1/run", runBody("ok"), nil, "", apiKey)
	require(code, 403, out)
	code, out, _ = request("PUT", "/api/budgets", map[string]any{"daily_limit": 10000000, "daily_enabled": false, "call_limit": 500000, "call_enabled": true}, userCookie, userCSRF, "")
	require(code, 200, out)
	code, out, _ = request("POST", "/v1/run", runBody("ok"), nil, "", apiKey)
	require(code, 403, out)
	code, out, _ = request("PUT", "/api/budgets", map[string]any{"daily_limit": 10000000, "daily_enabled": false, "call_limit": 2000000, "call_enabled": false}, userCookie, userCSRF, "")
	require(code, 200, out)
	var success, insufficient atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, _ := request("POST", "/v1/run", runBody("ok"), nil, "", apiKey)
			if code == 200 {
				success.Add(1)
			} else if code == 402 {
				insufficient.Add(1)
			} else {
				t.Errorf("unexpected concurrent status %d", code)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 5 || insufficient.Load() != 7 {
		t.Fatalf("concurrent calls: success=%d insufficient=%d", success.Load(), insufficient.Load())
	}
	u, _ = a.user(ctx, userID)
	if u.Balance != 500000 {
		t.Fatalf("balance after concurrency=%d", u.Balance)
	}
	var ledger int64
	_ = db.QueryRow(ctx, `SELECT sum(amount) FROM transactions WHERE user_id=$1`, userID).Scan(&ledger)
	if ledger != u.Balance {
		t.Fatalf("ledger %d does not match balance %d", ledger, u.Balance)
	}
	r := httptest.NewRequest("GET", "/api/admin/tools", nil)
	r.AddCookie(adminCookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if bytes.Contains(w.Body.Bytes(), []byte(secret)) || bytes.Contains(w.Body.Bytes(), []byte(stored)) {
		t.Fatal("upstream secret leaked")
	}
	idJSON, _ := json.Marshal(keyID)
	code, out, _ = request("DELETE", "/api/keys/"+string(idJSON), nil, userCookie, userCSRF, "")
	require(code, 200, out)
	code, out, _ = request("POST", "/v1/run", runBody("ok"), nil, "", apiKey)
	require(code, 401, out)
	_, e = db.Exec(ctx, `INSERT INTO usage(user_id,tool_name,endpoint,cost,created_at) VALUES($1,'Test','/ok',100000,'2000-01-01')`, userID)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = db.Exec(ctx, `UPDATE users SET balance=balance-100000 WHERE id=$1`, userID)
	a.recoverRuns(ctx)
	a.recoverRuns(ctx)
	u, _ = a.user(ctx, userID)
	if u.Balance != 500000 {
		t.Fatal("reservation recovery not idempotent")
	}
	t.Setenv("ADMIN_PASSWORD", "different-valid-password")
	if e = a.bootstrap(ctx); e != nil {
		t.Fatal(e)
	}
	u, _ = a.user(ctx, userID)
	if u.Balance != 500000 || u.Currency != "CNY" {
		t.Fatal("bootstrap changed balance or currency")
	}
	code, out, _ = request("POST", "/api/auth/login", map[string]string{"email": "admin@test.local", "password": "test-admin-password-1234"}, nil, "", "")
	require(code, 200, out)

	// MCP catalog synchronization, bulk operations and real tool billing.
	remote := mcp.NewServer(&mcp.Implementation{Name: "integration", Version: "1"}, &mcp.ServerOptions{PageSize: 1})
	addRemote := func(name string, schema string) {
		remote.AddTool(&mcp.Tool{Name: name, InputSchema: json.RawMessage(schema)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: req.Params.Name == "fail", Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		})
	}
	for _, name := range []string{"one", "two", "fail"} {
		addRemote(name, `{"type":"object"}`)
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return remote }, &mcp.StreamableHTTPOptions{JSONResponse: true})
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "upstream-secret" {
			http.Error(w, "bad key", 401)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	defer mcpServer.Close()
	provider := Tool{UpstreamType: "mcp", Slug: "remote", Name: "Remote", Category: "MCP", BaseURL: mcpServer.URL, AuthHeader: "X-API-Key", Secret: &secret, DefaultPrice: 50000, Enabled: true}
	code, out, _ = request("POST", "/api/admin/tools", provider, adminCookie, adminCSRF, "")
	require(code, 200, out)
	providerID := fmt.Sprint(int64(out["id"].(float64)))
	syncPath := "/api/admin/tools/" + providerID + "/sync"
	bulkPath := "/api/admin/tools/" + providerID + "/bulk"
	code, out, _ = request("POST", syncPath, nil, userCookie, userCSRF, "")
	require(code, 403, out)
	code, out, _ = request("POST", syncPath, nil, adminCookie, adminCSRF, "")
	require(code, 200, out)
	if out["added"].(float64) != 3 {
		t.Fatal("did not sync all remote pages")
	}
	provider, e = a.provider(ctx, providerID)
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	byName := map[string]string{}
	for _, ep := range provider.Endpoints {
		if !ep.Disabled {
			t.Fatal("new tool automatically enabled")
		}
		ids = append(ids, ep.ID)
		byName[ep.MCPName] = ep.ID
	}
	code, out, _ = request("POST", "/api/keys", map[string]string{"label": "MCP agent"}, userCookie, userCSRF, "")
	require(code, 201, out)
	mcpKey := out["key"].(string)
	remoteRun := func(name string) map[string]any {
		return map[string]any{"tool": "remote", "endpoint": byName[name], "parameters": map[string]any{}}
	}
	code, out, _ = request("POST", "/v1/run", remoteRun("one"), nil, "", mcpKey)
	require(code, 404, out)
	yes := true
	code, out, _ = request("POST", bulkPath, BulkMCPRequest{Revision: provider.Revision, IDs: ids, Enabled: &yes}, adminCookie, adminCSRF, "")
	require(code, 200, out)
	code, out, _ = request("POST", bulkPath, BulkMCPRequest{Revision: provider.Revision, IDs: ids, Enabled: &yes}, adminCookie, adminCSRF, "")
	require(code, 409, out)
	code, out, _ = request("POST", "/v1/run", remoteRun("one"), nil, "", mcpKey)
	require(code, 200, out)
	if out["currency"] != "CNY" {
		t.Fatal("run currency mismatch")
	}
	if out["cost_micros"].(float64) != 50000 {
		t.Fatal("default MCP price not charged")
	}
	before, _ := a.user(ctx, userID)
	code, out, _ = request("POST", "/v1/run", remoteRun("fail"), nil, "", mcpKey)
	require(code, 502, out)
	after, _ := a.user(ctx, userID)
	if after.Balance != before.Balance {
		t.Fatal("MCP isError did not refund")
	}
	provider, _ = a.provider(ctx, providerID)
	override := int64(20000)
	code, out, _ = request("POST", bulkPath, BulkMCPRequest{Revision: provider.Revision, IDs: []string{byName["one"]}, Price: &override}, adminCookie, adminCSRF, "")
	require(code, 200, out)
	remote.RemoveTools("two")
	addRemote("four", `{"type":"object"}`)
	addRemote("one", `{"type":"object","properties":{"q":{"type":"string"}}}`)
	code, out, _ = request("POST", syncPath, nil, adminCookie, adminCSRF, "")
	require(code, 200, out)
	provider, _ = a.provider(ctx, providerID)
	for _, ep := range provider.Endpoints {
		switch ep.MCPName {
		case "one":
			if ep.Disabled || !ep.PriceOverride || ep.Price != 20000 || !ep.SchemaChanged {
				t.Fatal("resync overwrote price, enabled state or change indicator")
			}
		case "two":
			if !ep.Disabled || !ep.Missing {
				t.Fatal("removed tool not disabled")
			}
		case "four":
			if !ep.Disabled {
				t.Fatal("newly added tool auto-enabled")
			}
		}
	}
	code, out, _ = request("POST", "/v1/run", remoteRun("one"), nil, "", mcpKey)
	require(code, 200, out)
	if out["cost_micros"].(float64) != 20000 {
		t.Fatal("MCP override not charged")
	}
	// A failed connection leaves the complete catalog unchanged.
	originalRevision := provider.Revision
	_, e = db.Exec(ctx, `UPDATE tools SET secret=$2 WHERE id=$1`, provider.ID, a.encrypt("wrong-key"))
	if e != nil {
		t.Fatal(e)
	}
	code, out, _ = request("POST", syncPath, nil, adminCookie, adminCSRF, "")
	require(code, 502, out)
	provider, _ = a.provider(ctx, providerID)
	if provider.Revision != originalRevision || len(provider.Endpoints) != 4 {
		t.Fatal("failed sync modified catalog")
	}
}
