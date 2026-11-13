package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestCLIReleaseMetadata(t *testing.T) {
	for _, version := range []string{"0.1.0", "0.2.0-beta.1"} {
		t.Run(version, func(t *testing.T) {
			directory := t.TempDir()
			t.Setenv("CLI_DIST", directory)
			name := "dyunwei-tvendo-" + version + ".tgz"
			if err := os.WriteFile(filepath.Join(directory, name), []byte("test-package"), 0600); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]string{"version": version, "package": name})
			if err := os.WriteFile(filepath.Join(directory, "manifest.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			a := &App{publicURL: "https://tools.example.com"}
			info := a.cliInfo()
			if info["cli_available"] != true || info["npm_package"] != "@dyunwei/tvendo" || info["cli_version"] != version {
				t.Fatal(info)
			}
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/cli/"+name, nil))
			if w.Code != 200 || w.Body.String() != "test-package" {
				t.Fatalf("release asset unavailable: %d", w.Code)
			}
		})
	}
}
