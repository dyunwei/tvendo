package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolReferences(t *testing.T) {
	for _, ep := range []string{"search", "路径/参数", "a:b.c"} {
		ref := toolRef(42, ep)
		id, got, err := parseToolRef(ref)
		if err != nil || id != 42 || got != ep {
			t.Fatalf("reference roundtrip %s", ref)
		}
	}
	for _, ref := range []string{"42.a", "t0.YQ", "t1.", "tt1.YQ", "t01.YQ"} {
		if _, _, e := parseToolRef(ref); e == nil {
			t.Fatalf("accepted %s", ref)
		}
	}
}
func TestSkillAndMissingRelease(t *testing.T) {
	t.Setenv("CLI_DIST", t.TempDir())
	a := &App{publicURL: "https://tools.example.com"}
	h := a.routes()
	for _, path := range []string{"/SKILL.md", "/v1/info"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), "{{BASE}}") {
			t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/cli/install.sh", nil))
	if w.Code != 503 {
		t.Fatal("advertised missing installer")
	}
}
