package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"tvendo/internal/billing"
)

var buildVersion = "dev"

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,80}$`)

func toolRef(id int64, endpoint string) string {
	return fmt.Sprintf("t%d.%s", id, base64.RawURLEncoding.EncodeToString([]byte(endpoint)))
}
func parseToolRef(ref string) (int64, string, error) {
	a, b, ok := strings.Cut(strings.TrimPrefix(ref, "t"), ".")
	id, err := strconv.ParseInt(a, 10, 64)
	ep, decErr := base64.RawURLEncoding.DecodeString(b)
	if !ok || err != nil || decErr != nil || id <= 0 || len(ep) == 0 || toolRef(id, string(ep)) != ref {
		return 0, "", fmt.Errorf("工具引用无效")
	}
	return id, string(ep), nil
}
func agentError(w http.ResponseWriter, status int, code, message, settlement string, runID int64, requestID string) {
	writeJSON(w, status, map[string]any{"error": message, "code": code, "settlement": settlement, "id": runID, "request_id": requestID, "currency": billing.Currency})
}
func agentOK(w http.ResponseWriter, data any) {
	writeJSON(w, 200, map[string]any{"ok": true, "data": data})
}

type catalogItem struct {
	Currency          string          `json:"currency"`
	Ref               string          `json:"tool_ref"`
	Name              string          `json:"name"`
	Provider          string          `json:"provider"`
	Description       string          `json:"description"`
	SearchDescription string          `json:"search_description,omitempty"`
	Price             int64           `json:"price_micros"`
	InputSchema       json.RawMessage `json:"input_schema,omitempty"`
	Example           json.RawMessage `json:"input_example,omitempty"`
	Type              string          `json:"upstream_type,omitempty"`
	SchemaAvailable   bool            `json:"schema_available"`
}

func pageLimit(r *http.Request, fallback int) (int, error) {
	if r.URL.Query().Get("limit") == "" {
		return fallback, nil
	}
	n, e := strconv.Atoi(r.URL.Query().Get("limit"))
	if e != nil || n < 1 || n > 50 {
		return 0, fmt.Errorf("limit 应为 1–50")
	}
	return n, nil
}
func (a *App) catalog(w http.ResponseWriter, r *http.Request, u User) {
	limit, err := pageLimit(r, 10)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	docs, err := a.catalogDocuments(r.Context())
	if err != nil {
		fail(w, 500, "无法读取工具目录")
		return
	}
	if ref := r.PathValue("ref"); ref != "" {
		for _, d := range docs {
			if d.item.Ref == ref {
				agentOK(w, d.item)
				return
			}
		}
		fail(w, 404, "工具不存在或未启用")
		return
	}
	query := strings.ToLower(strings.Join(strings.Fields(r.URL.Query().Get("q")), " "))
	if len(query) > 4096 {
		fail(w, 400, "搜索关键词不能超过 4096 字节")
		return
	}
	items := make([]catalogItem, 0, len(docs))
	status := searchStatus{Mode: "directory", Total: len(docs)}
	if query == "" {
		for _, d := range docs {
			items = append(items, d.item)
		}
	} else {
		items, status = a.searchCatalog(r.Context(), docs, query)
	}
	page, next, err := catalogPage(items, query, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	suggestions := catalogSuggestions(docs, query, items, status, a.searchFallbackRef)
	agentOK(w, map[string]any{"items": page, "suggested_tools": suggestions, "next_cursor": next, "currency": billing.Currency, "search": status})
}
func (a *App) agentRuns(w http.ResponseWriter, r *http.Request, u User) {
	limit, err := pageLimit(r, 20)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	var before int64 = 1<<63 - 1
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		before, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || before <= 0 {
			fail(w, 400, "cursor 无效")
			return
		}
	}
	var id int64
	if s := r.PathValue("id"); s != "" {
		id, err = strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			fail(w, 400, "调用编号无效")
			return
		}
	}
	requestID := r.URL.Query().Get("request_id")
	if requestID != "" && !requestIDPattern.MatchString(requestID) {
		fail(w, 400, "请求标识无效")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT row_to_json(x) FROM (SELECT id,tool_name,endpoint,cost AS cost_micros,status,http_status,duration_ms,error_code,error_message,resolution_note,resolved_at,created_at,request_id,$6::text AS currency FROM usage WHERE user_id=$1 AND ($2::bigint=0 OR id=$2) AND id<$3 AND ($4='' OR request_id=$4) ORDER BY id DESC LIMIT $5) x`, u.ID, id, before, requestID, limit+1, billing.Currency)
	if err != nil {
		fail(w, 500, "无法读取调用记录")
		return
	}
	defer rows.Close()
	items := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if rows.Scan(&raw) != nil {
			fail(w, 500, "读取调用记录失败")
			return
		}
		items = append(items, raw)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取调用记录失败")
		return
	}
	if id != 0 {
		if len(items) == 0 {
			fail(w, 404, "调用记录不存在")
			return
		}
		agentOK(w, items[0])
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		var last struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(items[limit-1], &last)
		next = strconv.FormatInt(last.ID, 10)
	}
	agentOK(w, map[string]any{"items": items, "next_cursor": next, "currency": billing.Currency})
}
func (a *App) withAPI(fn func(http.ResponseWriter, *http.Request, User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u, ok := a.apiUser(w, r); ok {
			fn(w, r, u)
		}
	}
}
func (a *App) agentBudget(w http.ResponseWriter, r *http.Request, u User) {
	var spent, held int64
	err := a.db.QueryRow(r.Context(), `SELECT COALESCE(sum(cost) FILTER (WHERE status='success'),0),COALESCE(sum(cost) FILTER (WHERE status IN ('running','unknown')),0) FROM usage WHERE user_id=$1 AND created_at >= date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, u.ID).Scan(&spent, &held)
	if err != nil {
		fail(w, 500, "预算读取失败")
		return
	}
	reset := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	agentOK(w, map[string]any{"daily_limit_micros": u.DailyLimit, "call_limit_micros": u.CallLimit, "daily_enabled": u.DailyEnabled, "call_enabled": u.CallEnabled, "spent_micros": spent, "held_micros": held, "resets_at": reset, "currency": billing.Currency})
}

// Serve only generated release filenames. Never expose arbitrary local files.
func (a *App) cliAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !regexp.MustCompile(`^(tvendo-(linux|darwin|windows)-(amd64|arm64)(\.exe)?|(tvendo-cli|dyunwei-tvendo)-[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?\.tgz|SHA256SUMS|manifest\.json)$`).MatchString(name) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(env("CLI_DIST", "../distribution/dist"), name))
}
func (a *App) cliInfo() map[string]any {
	info := map[string]any{"brand": "T vendo", "api_version": 1, "version": buildVersion, "currency": billing.Currency, "micros_per_unit": 1000000, "cli_available": false, "skill_url": a.publicURL + "/SKILL.md"}
	info["run_idempotency"] = true
	raw, err := os.ReadFile(filepath.Join(env("CLI_DIST", "../distribution/dist"), "manifest.json"))
	var manifest struct {
		Version string `json:"version"`
		Package string `json:"package"`
	}
	if err == nil && json.Unmarshal(raw, &manifest) == nil && regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`).MatchString(manifest.Version) && (manifest.Package == "dyunwei-tvendo-"+manifest.Version+".tgz" || manifest.Package == "tvendo-cli-"+manifest.Version+".tgz") {
		if _, err := os.Stat(filepath.Join(env("CLI_DIST", "../distribution/dist"), manifest.Package)); err == nil {
			info["cli_available"] = true
			info["cli_version"] = manifest.Version
			info["npm_package"] = "@dyunwei/tvendo"
			info["npm_url"] = a.publicURL + "/cli/" + manifest.Package
			info["install_url"] = a.publicURL + "/cli/install.sh"
		}
	}
	return info
}
