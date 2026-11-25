package main

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPTrustsConfiguredProxyChain(t *testing.T) {
	proxies, err := parseTrustedProxies("10.0.0.5/32, 2001:db8:1::/64")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{trustedProxies: proxies}
	for _, test := range []struct {
		name, peer, forwarded, want string
	}{
		{"untrusted peer", "198.51.100.7:443", "203.0.113.5", "198.51.100.7"},
		{"trusted proxy", "10.0.0.5:80", "198.51.100.8, 2001:db8:1::2", "198.51.100.8"},
		{"spoofed leftmost", "10.0.0.5:80", "198.51.100.1, 203.0.113.3", "203.0.113.3"},
		{"malformed rightmost", "10.0.0.5:80", "198.51.100.2, malformed", "10.0.0.5"},
		{"ipv6", "[2001:db8:1::2]:80", "2001:db8:2::5", "2001:db8:2::5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = test.peer
			r.Header.Set("X-Forwarded-For", test.forwarded)
			if got := a.clientIP(r); got != test.want {
				t.Fatalf("client IP = %q, want %q", got, test.want)
			}
		})
	}
}
