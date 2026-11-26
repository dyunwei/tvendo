package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserOriginForDevelopmentAndProduction(t *testing.T) {
	const publicURL = "http://192.0.2.10:5173"
	for _, tc := range []struct {
		name, origin string
		dev          bool
		want         int
	}{
		{"vite", publicURL, true, http.StatusNotFound},
		{"direct backend", "http://192.0.2.10:8080", true, http.StatusNotFound},
		{"loopback alias", "http://127.0.0.1:8080", true, http.StatusNotFound},
		{"other host", "http://192.0.2.11:8080", true, http.StatusForbidden},
		{"other port", "http://192.0.2.10:8081", true, http.StatusForbidden},
		{"opaque browser origin", "null", true, http.StatusForbidden},
		{"production public URL", publicURL, false, http.StatusNotFound},
		{"production alternate port", "http://192.0.2.10:8080", false, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{publicURL: publicURL}
			if tc.dev {
				a.devOrigins = developmentOrigins(publicURL, "8080")
			}
			r := httptest.NewRequest(http.MethodPost, "/api/unknown", nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Forwarded-Host", "192.0.2.11:8080")
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("origin %q returned %d, want %d: %s", tc.origin, w.Code, tc.want, w.Body)
			}
		})
	}
}
