package main

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed skill.md
var agentSkill string

//go:embed install.sh
var installScript string

func (a *App) skill(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(strings.ReplaceAll(agentSkill, "{{BASE}}", a.publicURL)))
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func (a *App) installer(w http.ResponseWriter, r *http.Request) {
	info := a.cliInfo()
	if info["cli_available"] != true {
		http.Error(w, "此部署尚未配置 CLI 产物", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	s := strings.ReplaceAll(installScript, "{{SHELL_BASE}}", shellQuote(a.publicURL))
	s = strings.ReplaceAll(s, "{{SHELL_VERSION}}", shellQuote(info["cli_version"].(string)))
	_, _ = w.Write([]byte(s))
}
