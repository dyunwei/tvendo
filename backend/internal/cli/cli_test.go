package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func env(t *testing.T, server string) {
	t.Helper()
	t.Setenv("TVENDO_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("TVENDO_SERVER", server)
	t.Setenv("TVENDO_API_KEY", "tv_live_testsecret")
}
func invoke(args ...string) (int, map[string]any, string) {
	var out, stderr bytes.Buffer
	n := Main(append(args, "--json"), strings.NewReader(""), &out, &stderr, "0.1.0")
	var data map[string]any
	_ = json.Unmarshal(out.Bytes(), &data)
	return n, data, stderr.String()
}
func TestSetupDoctorAndServerIsolation(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer tv_live_testsecret" {
			t.Error("missing credential")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/info" {
			w.Write([]byte(`{"ok":true,"data":{"api_version":1}}`))
		} else {
			w.Write([]byte(`{"ok":true,"data":{"balance_micros":123}}`))
		}
	}))
	defer srv.Close()
	env(t, srv.URL)
	beforeOverride := requests.Load()
	codeOverride, dataOverride, _ := invoke("setup", "--server", "https://another.example")
	if codeOverride != 2 || requests.Load() != beforeOverride {
		t.Fatalf("environment credential crossed origin: %d %v", codeOverride, dataOverride)
	}
	code, data, _ := invoke("setup", "--server", srv.URL)
	if code != 0 || data["ok"] != true {
		t.Fatalf("setup: %d %v", code, data)
	}
	b, e := os.ReadFile(os.Getenv("TVENDO_CONFIG"))
	if e != nil || bytes.Contains(b, []byte("testsecret")) {
		t.Fatal("environment key persisted")
	}
	st, _ := os.Stat(os.Getenv("TVENDO_CONFIG"))
	if st.Mode().Perm() != 0600 {
		t.Fatal("config mode")
	}
	code, data, _ = invoke("doctor")
	if code != 0 {
		t.Fatalf("doctor: %v", data)
	}
	before := requests.Load()
	t.Setenv("TVENDO_SERVER", "https://another.example")
	code, data, _ = invoke("doctor")
	if code != 2 || requests.Load() != before {
		t.Fatalf("credential sent to changed server: %d %v", code, data)
	}
}
func TestRunOutputAndNoRetry(t *testing.T) {
	var runs atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runs.Add(1)
		if len(r.Header.Get("X-Request-ID")) < 8 {
			t.Error("request ID missing")
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if b["tool_ref"] != "t1.b2s" || b["max_cost_micros"] != float64(7) {
			t.Errorf("input: %v", b)
		}
		w.Write([]byte(`{"id":42,"cost_micros":7,"currency":"CNY","data":{"value":9007199254740993}}`))
	}))
	defer srv.Close()
	env(t, srv.URL)
	path := filepath.Join(t.TempDir(), "result.json")
	code, data, stderr := invoke("run", "t1.b2s", "--input", `{"query":"中文"}`, "--max-cost-micros", "7", "--output", path)
	if code == 0 && data["data"].(map[string]any)["currency"] != "CNY" {
		t.Fatal("run omitted server currency")
	}
	if code != 0 || !strings.Contains(stderr, "请求标识") {
		t.Fatalf("run: %d %v", code, data)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte("9007199254740993")) {
		t.Fatal("integer result precision lost")
	}
	code, _, _ = invoke("run", "t1.b2s", "--output", path)
	if code != 2 || runs.Load() != 1 {
		t.Fatal("existing output caused paid call")
	}
	code, _, _ = invoke("run", "t1.b2s", "--input", "[]")
	if code != 2 || runs.Load() != 1 {
		t.Fatal("invalid input caused paid call")
	}
}
func TestFailureMappingAndNoRedirect(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		exit   int
	}{
		{400, `{"error":"输入无效"}`, 2}, {402, `{"error":"余额不足","code":"INSUFFICIENT_BALANCE","settlement":"not_started"}`, 4},
		{502, `{"error":"失败","settlement":"refunded","id":1}`, 6}, {503, `{"error":"结算未知","id":1}`, 7}, {200, `<html>wrong server</html>`, 7},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			env(t, srv.URL)
			n, data, _ := invoke("run", "t1.b2s")
			if n != tc.exit || calls.Load() != 1 || data["error"].(map[string]any)["request_id"] == "" {
				t.Fatalf("%d %v calls=%d", n, data, calls.Load())
			}
		})
	}
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	env(t, source.URL)
	code, _, _ := invoke("doctor")
	if code == 0 || leaked.Load() != 0 {
		t.Fatal("redirect followed")
	}
}
func TestJSONHelpAndErrors(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}, {"tools", "search", "词", "--bad"}, {"--bad"}} {
		n, data, _ := invoke(args...)
		if data == nil {
			t.Fatalf("non-JSON output %v", args)
		}
		if n == 0 && data["ok"] != true {
			t.Fatal(data)
		}
	}
}
