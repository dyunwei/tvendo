package main

import (
	"net"
	"net/url"
	"strings"
)

func httpOrigin(host, port string) string {
	if port == "80" {
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		return "http://" + host
	}
	return "http://" + net.JoinHostPort(host, port)
}

// make dev can serve the built frontend directly or through Vite. Use only
// known development addresses, independent of incoming Host and proxy headers.
func developmentOrigins(publicURL, backendPort string) map[string]bool {
	origin, err := url.Parse(publicURL)
	if err != nil || origin.Scheme != "http" || origin.Hostname() == "" {
		return nil
	}
	hosts := []string{"localhost", "127.0.0.1", "::1", origin.Hostname()}
	origins := make(map[string]bool)
	for _, host := range hosts {
		for _, port := range []string{backendPort, "5173"} {
			origins[httpOrigin(host, port)] = true
		}
	}
	return origins
}

func (a *App) allowsOrigin(origin string) bool {
	return origin == "" || origin == a.publicURL || a.devOrigins[origin]
}
