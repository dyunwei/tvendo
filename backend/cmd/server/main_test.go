package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
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
	code, out, _ = request("POST", "/v1/run", runBody("fail"), nil, "", apiKey)
	require(code, 502, out)
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
	code, out, _ = request("POST", "/api/auth/login", map[string]string{"email": "admin@test.local", "password": "test-admin-password-1234"}, nil, "", "")
	require(code, 200, out)
}
