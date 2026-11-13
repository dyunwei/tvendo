package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

//go:embed schema.sql
var schema string

type App struct {
	db        *pgxpool.Pool
	redis     *redis.Client
	crypt     cipher.AEAD
	publicURL string
	secure    bool
	client    *http.Client
}
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	Balance      int64  `json:"balance"`
	DailyLimit   int64  `json:"daily_limit"`
	CallLimit    int64  `json:"call_limit"`
	DailyEnabled bool   `json:"daily_enabled"`
	CallEnabled  bool   `json:"call_enabled"`
}
type Session struct {
	UserID int64  `json:"user_id"`
	CSRF   string `json:"csrf"`
}
type Endpoint struct {
	MCPName       string          `json:"mcp_name,omitempty"`
	InputSchema   json.RawMessage `json:"input_schema,omitempty"`
	Disabled      bool            `json:"disabled,omitempty"`
	Missing       bool            `json:"missing,omitempty"`
	SchemaChanged bool            `json:"schema_changed,omitempty"`
	PriceOverride bool            `json:"price_override,omitempty"`
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Path          string          `json:"path"`
	Method        string          `json:"method"`
	Description   string          `json:"description"`
	Price         int64           `json:"price_micros"`
	Parameters    json.RawMessage `json:"parameters"`
}
type Resource struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       string `json:"price"`
}
type Tool struct {
	UpstreamType string     `json:"upstream_type"`
	DefaultPrice int64      `json:"default_price_micros"`
	Revision     int64      `json:"revision"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
	ID           int64      `json:"id"`
	Slug         string     `json:"slug"`
	Name         string     `json:"name"`
	Category     string     `json:"category"`
	Description  string     `json:"description"`
	Website      string     `json:"website"`
	Docs         string     `json:"docs"`
	Icon         string     `json:"icon"`
	BaseURL      string     `json:"base_url,omitempty"`
	AuthHeader   string     `json:"auth_header,omitempty"`
	AuthPrefix   string     `json:"auth_prefix,omitempty"`
	Secret       *string    `json:"secret,omitempty"`
	HasSecret    bool       `json:"has_secret,omitempty"`
	Enabled      bool       `json:"enabled"`
	Endpoints    []Endpoint `json:"endpoints"`
	Resources    []Resource `json:"resources"`
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	if e := d.Decode(v); e != nil {
		fail(w, 400, "请求数据格式错误")
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		fail(w, 400, "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}
func (a *App) encrypt(s string) string {
	n := make([]byte, a.crypt.NonceSize())
	if _, e := rand.Read(n); e != nil {
		panic(e)
	}
	return hex.EncodeToString(a.crypt.Seal(n, n, []byte(s), nil))
}
func (a *App) decrypt(s string) (string, error) {
	b, e := hex.DecodeString(s)
	if e != nil || len(b) < a.crypt.NonceSize() {
		return "", errors.New("invalid encrypted key")
	}
	n := a.crypt.NonceSize()
	v, e := a.crypt.Open(nil, b[:n], b[n:], nil)
	return string(v), e
}
func (a *App) user(ctx context.Context, id int64) (User, error) {
	var u User
	e := a.db.QueryRow(ctx, `SELECT id,email,name,role,balance,daily_limit,call_limit,daily_enabled,call_enabled FROM users WHERE id=$1`, id).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Balance, &u.DailyLimit, &u.CallLimit, &u.DailyEnabled, &u.CallEnabled)
	return u, e
}
func (a *App) rate(r *http.Request, prefix string, max int64) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	key := "rate:" + prefix + ":" + host + ":" + strconv.FormatInt(time.Now().Unix()/60, 10)
	n, e := a.redis.Incr(r.Context(), key).Result()
	if e != nil {
		return false
	}
	if n == 1 {
		a.redis.Expire(r.Context(), key, 2*time.Minute)
	}
	return n <= max
}
func (a *App) session(r *http.Request) (Session, error) {
	var s Session
	c, e := r.Cookie("tv_session")
	if e != nil {
		return s, e
	}
	data, e := a.redis.Get(r.Context(), "session:"+hash(c.Value)).Bytes()
	if e != nil {
		return s, e
	}
	e = json.Unmarshal(data, &s)
	return s, e
}
func (a *App) withUser(admin bool, next func(http.ResponseWriter, *http.Request, User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, e := a.session(r)
		if e != nil {
			fail(w, 401, "请先登录")
			return
		}
		u, e := a.user(r.Context(), s.UserID)
		if e != nil {
			fail(w, 401, "登录已失效")
			return
		}
		if admin && u.Role != "admin" {
			fail(w, 403, "需要管理员权限")
			return
		}
		if r.Method != "GET" && r.Header.Get("X-CSRF-Token") != s.CSRF {
			fail(w, 403, "请求验证失败，请刷新页面")
			return
		}
		next(w, r, u)
	}
}
func (a *App) newSession(w http.ResponseWriter, r *http.Request, u User) {
	token := randomToken()
	s := Session{u.ID, randomToken()}
	b, _ := json.Marshal(s)
	if e := a.redis.Set(r.Context(), "session:"+hash(token), b, 7*24*time.Hour).Err(); e != nil {
		fail(w, 503, "会话服务暂不可用")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "tv_session", Value: token, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode, MaxAge: 604800})
	writeJSON(w, 200, map[string]any{"user": u, "csrf": s.CSRF})
}
func (a *App) auth(w http.ResponseWriter, r *http.Request) {
	if !a.rate(r, "auth", 12) {
		fail(w, 429, "操作过于频繁，请稍后重试")
		return
	}
	var b struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if !decode(w, r, &b) {
		return
	}
	b.Email = strings.ToLower(strings.TrimSpace(b.Email))
	if len(b.Email) > 254 || !strings.Contains(b.Email, "@") || len(b.Password) < 10 || len(b.Password) > 72 {
		fail(w, 400, "请输入有效邮箱，密码长度需为 10–72 字节")
		return
	}
	var id int64
	if r.URL.Path == "/api/auth/register" {
		b.Name = strings.TrimSpace(b.Name)
		if len(b.Name) < 1 || len(b.Name) > 80 {
			fail(w, 400, "请填写姓名（最多 80 字节）")
			return
		}
		pw, e := bcrypt.GenerateFromPassword([]byte(b.Password), bcrypt.DefaultCost)
		if e != nil {
			fail(w, 500, "注册失败")
			return
		}
		e = a.db.QueryRow(r.Context(), `INSERT INTO users(email,name,password) VALUES($1,$2,$3) ON CONFLICT(email) DO NOTHING RETURNING id`, b.Email, b.Name, string(pw)).Scan(&id)
		if e != nil {
			fail(w, 409, "该邮箱无法注册，请尝试登录")
			return
		}
	} else {
		var pw string
		e := a.db.QueryRow(r.Context(), `SELECT id,password FROM users WHERE email=$1`, b.Email).Scan(&id, &pw)
		if e != nil {
			pw = "$2a$10$7EqJtq98hPqEX7fNZaFWoO5S7HNlL9s6pVg0PiIsWMi4c9P0M0uRK"
		}
		err := bcrypt.CompareHashAndPassword([]byte(pw), []byte(b.Password))
		if e != nil || err != nil {
			fail(w, 401, "邮箱或密码错误")
			return
		}
	}
	u, e := a.user(r.Context(), id)
	if e != nil {
		fail(w, 500, "读取用户失败")
		return
	}
	a.newSession(w, r, u)
}
func (a *App) rows(w http.ResponseWriter, r *http.Request, q string, args ...any) {
	rows, e := a.db.Query(r.Context(), q, args...)
	if e != nil {
		log.Print(e)
		fail(w, 500, "读取数据失败")
		return
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e := rows.Scan(&b); e != nil {
			fail(w, 500, "读取数据失败")
			return
		}
		out = append(out, b)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取数据失败")
		return
	}
	writeJSON(w, 200, out)
}
func (a *App) listTools(w http.ResponseWriter, r *http.Request, admin bool) {
	q := `SELECT to_jsonb(t) FROM tools t`
	if !admin {
		q += ` WHERE enabled=true`
	}
	q += ` ORDER BY id`
	rows, err := a.db.Query(r.Context(), q)
	if err != nil {
		fail(w, 500, "无法加载工具")
		return
	}
	defer rows.Close()
	out := []Tool{}
	for rows.Next() {
		var data []byte
		var t Tool
		if rows.Scan(&data) != nil || json.Unmarshal(data, &t) != nil {
			fail(w, 500, "工具配置格式错误")
			return
		}
		t.HasSecret = t.Secret != nil && *t.Secret != ""
		t.Secret = nil
		if !admin {
			visible := []Endpoint{}
			for _, ep := range t.Endpoints {
				if !ep.Disabled && !ep.Missing {
					ep.Price = effectivePrice(t, ep)
					visible = append(visible, ep)
				}
			}
			t.Endpoints = visible
			t.BaseURL = ""
			t.AuthHeader = ""
			t.AuthPrefix = ""
			t.HasSecret = false
		}
		out = append(out, t)
	}
	if rows.Err() != nil {
		fail(w, 500, "无法加载工具")
		return
	}
	writeJSON(w, 200, out)
}
func validSlug(s string) bool {
	if len(s) < 1 || len(s) > 80 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func validateTool(t Tool) error {
	if t.UpstreamType != "" && t.UpstreamType != "http" && t.UpstreamType != "mcp" && t.UpstreamType != "mcp_sse" {
		return errors.New("不支持的上游类型")
	}
	if t.DefaultPrice < 0 || t.DefaultPrice > 1000000000 {
		return errors.New("默认售价需在 0–1000 美元内")
	}
	if !validSlug(t.Slug) || strings.TrimSpace(t.Name) == "" || len(t.Name) > 100 || t.Category == "" {
		return errors.New("请填写工具名称、分类及有效标识（小写字母、数字、-、_）")
	}
	if t.BaseURL != "" {
		u, e := url.Parse(t.BaseURL)
		if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(os.Getenv("ALLOW_PRIVATE_UPSTREAM") == "true" && u.Scheme == "http")) {
			return errors.New("上游地址必须是有效 HTTPS 地址，不含用户名、查询参数或锚点")
		}
	} else if t.Enabled {
		return errors.New("启用前请填写上游地址")
	}
	for _, link := range []string{t.Website, t.Docs} {
		if link != "" {
			u, e := url.Parse(link)
			if e != nil || u.Scheme != "https" || u.Host == "" {
				return errors.New("网站与文档链接必须使用 HTTPS")
			}
		}
	}
	if t.Secret != nil && *t.Secret != "" && t.AuthHeader == "" {
		return errors.New("配置密钥时请填写鉴权 Header")
	}
	if strings.ContainsAny(t.AuthHeader+t.AuthPrefix, "\r\n") || strings.ContainsAny(t.AuthHeader, " :\t") || strings.EqualFold(t.AuthHeader, "Host") || strings.EqualFold(t.AuthHeader, "Content-Length") {
		return errors.New("鉴权 Header 配置无效")
	}
	ids := map[string]bool{}
	for _, ep := range t.Endpoints {
		if (t.UpstreamType == "http" || t.UpstreamType == "") && !validSlug(ep.ID) || ep.ID == "" || ids[ep.ID] || ep.Name == "" || ep.Price < 0 || ep.Price > 1000000000 {
			return errors.New("接口标识需唯一且有效，名称必填，售价需在 0–1000 美元内")
		}
		ids[ep.ID] = true
		if isMCP(t) {
			continue
		}
		switch ep.Method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			return errors.New("接口 HTTP 方法无效")
		}
		p, e := url.Parse(ep.Path)
		if e != nil || !strings.HasPrefix(ep.Path, "/") || p.Host != "" || p.RawQuery != "" || p.Fragment != "" || strings.Contains(ep.Path, "..") || strings.HasPrefix(ep.Path, "//") {
			return errors.New("接口路径需以 / 开头，不含查询参数或 ..")
		}
		if len(ep.Parameters) > 0 && !json.Valid(ep.Parameters) {
			return errors.New("接口参数示例必须是 JSON")
		}
	}
	return nil
}
func (a *App) saveTool(w http.ResponseWriter, r *http.Request, u User) {
	var t Tool
	if !decode(w, r, &t) {
		return
	}
	if t.UpstreamType == "" {
		t.UpstreamType = "http"
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback(r.Context())
	if t.ID != 0 {
		var raw []byte
		var old Tool
		if tx.QueryRow(r.Context(), `SELECT to_jsonb(t) FROM tools t WHERE id=$1 FOR UPDATE`, t.ID).Scan(&raw) != nil || json.Unmarshal(raw, &old) != nil {
			fail(w, 404, "工具不存在")
			return
		}
		if old.Revision != t.Revision {
			fail(w, 409, "配置已被更新，请重新打开后操作")
			return
		}
		// Remote metadata is authoritative. MCP catalog changes use sync/bulk endpoints.
		if isMCP(t) {
			if isMCP(old) && old.BaseURL == t.BaseURL {
				t.Endpoints = old.Endpoints
			} else {
				t.Endpoints = []Endpoint{}
			}
		}
	} else if isMCP(t) {
		t.Endpoints = []Endpoint{}
	}
	if err = validateTool(t); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if t.Endpoints == nil {
		t.Endpoints = []Endpoint{}
	}
	if t.Resources == nil {
		t.Resources = []Resource{}
	}
	eps, _ := json.Marshal(t.Endpoints)
	res, _ := json.Marshal(t.Resources)
	var secret *string
	if t.Secret != nil {
		v := ""
		if *t.Secret != "" {
			v = a.encrypt(*t.Secret)
		}
		secret = &v
	}
	var id int64
	if t.ID == 0 {
		v := ""
		if secret != nil {
			v = *secret
		}
		err = tx.QueryRow(r.Context(), `INSERT INTO tools(slug,name,category,description,website,docs,icon,base_url,auth_header,auth_prefix,secret,enabled,endpoints,resources,upstream_type,default_price_micros) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id`, t.Slug, t.Name, t.Category, t.Description, t.Website, t.Docs, t.Icon, t.BaseURL, t.AuthHeader, t.AuthPrefix, v, t.Enabled, eps, res, t.UpstreamType, t.DefaultPrice).Scan(&id)
	} else {
		err = tx.QueryRow(r.Context(), `UPDATE tools SET slug=$2,name=$3,category=$4,description=$5,website=$6,docs=$7,icon=$8,base_url=$9,auth_header=$10,auth_prefix=$11,secret=COALESCE($12,secret),enabled=$13,endpoints=$14,resources=$15,upstream_type=$16,default_price_micros=$17,revision=revision+1,last_synced_at=CASE WHEN upstream_type=$16 AND base_url=$9 THEN last_synced_at ELSE NULL END WHERE id=$1 RETURNING id`, t.ID, t.Slug, t.Name, t.Category, t.Description, t.Website, t.Docs, t.Icon, t.BaseURL, t.AuthHeader, t.AuthPrefix, secret, t.Enabled, eps, res, t.UpstreamType, t.DefaultPrice).Scan(&id)
	}
	if err != nil {
		fail(w, 400, "保存失败：标识可能已存在")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_log(actor_id,action,target_id) VALUES($1,'save_tool',$2)`, u.ID, id); err != nil || tx.Commit(r.Context()) != nil {
		fail(w, 500, "保存失败")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}
func (a *App) keys(w http.ResponseWriter, r *http.Request, u User) {
	switch r.Method {
	case "GET":
		a.rows(w, r, `SELECT row_to_json(k) FROM (SELECT id,label,prefix,revoked,last_used,created_at FROM api_keys WHERE user_id=$1 ORDER BY id DESC) k`, u.ID)
	case "POST":
		var b struct {
			Label string `json:"label"`
		}
		if !decode(w, r, &b) {
			return
		}
		b.Label = strings.TrimSpace(b.Label)
		if len(b.Label) < 1 || len(b.Label) > 80 {
			fail(w, 400, "请输入 1–80 字节的名称")
			return
		}
		token := "tv_live_" + randomToken()
		var id int64
		e := a.db.QueryRow(r.Context(), `INSERT INTO api_keys(user_id,label,hash,prefix) VALUES($1,$2,$3,$4) RETURNING id`, u.ID, b.Label, hash(token), token[:16]).Scan(&id)
		if e != nil {
			fail(w, 500, "创建失败")
			return
		}
		writeJSON(w, 201, map[string]any{"id": id, "key": token})
	case "DELETE":
		result, e := a.db.Exec(r.Context(), `UPDATE api_keys SET revoked=true WHERE id=$1 AND user_id=$2 AND revoked=false`, r.PathValue("id"), u.ID)
		if e != nil || result.RowsAffected() == 0 {
			fail(w, 404, "密钥不存在或已撤销")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}
func (a *App) budget(w http.ResponseWriter, r *http.Request, u User) {
	if r.Method == "PUT" {
		var b struct {
			Daily        int64 `json:"daily_limit"`
			Call         int64 `json:"call_limit"`
			DailyEnabled bool  `json:"daily_enabled"`
			CallEnabled  bool  `json:"call_enabled"`
		}
		if !decode(w, r, &b) {
			return
		}
		if b.Daily < 0 || b.Call < 0 || b.Daily > 1000000000000 || b.Call > 1000000000000 {
			fail(w, 400, "预算数值无效")
			return
		}
		_, e := a.db.Exec(r.Context(), `UPDATE users SET daily_limit=$2,call_limit=$3,daily_enabled=$4,call_enabled=$5 WHERE id=$1`, u.ID, b.Daily, b.Call, b.DailyEnabled, b.CallEnabled)
		if e != nil {
			fail(w, 500, "保存失败")
			return
		}
		u, _ = a.user(r.Context(), u.ID)
	}
	var spent, held int64
	e := a.db.QueryRow(r.Context(), `SELECT COALESCE(sum(cost) FILTER (WHERE status='success'),0),COALESCE(sum(cost) FILTER(WHERE status='running'),0) FROM usage WHERE user_id=$1 AND created_at >= date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, u.ID).Scan(&spent, &held)
	if e != nil {
		fail(w, 500, "预算读取失败")
		return
	}
	writeJSON(w, 200, map[string]any{"daily_limit": u.DailyLimit, "call_limit": u.CallLimit, "daily_enabled": u.DailyEnabled, "call_enabled": u.CallEnabled, "spent": spent, "held": held})
}
func (a *App) topup(w http.ResponseWriter, r *http.Request, u User) {
	var b struct {
		UserID int64  `json:"user_id"`
		Amount int64  `json:"amount"`
		Note   string `json:"note"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Amount <= 0 || b.Amount > 1000000000000 || len(b.Note) > 500 {
		fail(w, 400, "充值金额需大于 0 且不超过 100 万美元，备注最多 500 字节")
		return
	}
	tx, e := a.db.Begin(r.Context())
	if e != nil {
		fail(w, 500, "充值失败")
		return
	}
	defer tx.Rollback(r.Context())
	var balance int64
	e = tx.QueryRow(r.Context(), `UPDATE users SET balance=balance+$2 WHERE id=$1 RETURNING balance`, b.UserID, b.Amount).Scan(&balance)
	if e != nil {
		fail(w, 404, "用户不存在")
		return
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO transactions(user_id,amount,kind,note,actor_id) VALUES($1,$2,'topup',$3,$4)`, b.UserID, b.Amount, b.Note, u.ID)
	if e != nil {
		fail(w, 500, "充值失败")
		return
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO audit_log(actor_id,action,target_id) VALUES($1,'topup',$2)`, u.ID, b.UserID)
	if e != nil || tx.Commit(r.Context()) != nil {
		fail(w, 500, "充值失败")
		return
	}
	writeJSON(w, 200, map[string]any{"balance": balance})
}

// Resolve and validate every connection, including DNS answers, to prevent access to private services.
func safeDial(ctx context.Context, network, addr string) (net.Conn, error) {
	if os.Getenv("ALLOW_PRIVATE_UPSTREAM") == "true" {
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
	}
	host, port, e := net.SplitHostPort(addr)
	if e != nil {
		return nil, e
	}
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
	if e != nil {
		return nil, e
	}
	if len(ips) == 0 {
		return nil, errors.New("no DNS result")
	}
	for _, ip := range ips {
		v := ip.IP
		if !v.IsGlobalUnicast() || v.IsPrivate() || v.IsLoopback() || v.IsLinkLocalUnicast() || v.IsUnspecified() || v.Equal(net.ParseIP("169.254.169.254")) || v.To4() == nil && v.To16()[0] == 0x20 && v.To16()[1] == 0x02 {
			return nil, errors.New("private upstream address rejected")
		}
	}
	return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}
func (a *App) apiUser(w http.ResponseWriter, r *http.Request) (User, bool) {
	var u User
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !strings.HasPrefix(token, "tv_live_") {
		fail(w, 401, "需要有效的平台 API Key")
		return u, false
	}
	var id, keyID int64
	e := a.db.QueryRow(r.Context(), `SELECT user_id,id FROM api_keys WHERE hash=$1 AND revoked=false`, hash(token)).Scan(&id, &keyID)
	if e != nil {
		fail(w, 401, "API Key 无效或已撤销")
		return u, false
	}
	u, e = a.user(r.Context(), id)
	if e != nil {
		fail(w, 401, "用户不存在")
		return u, false
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE api_keys SET last_used=now() WHERE id=$1`, keyID)
	return u, true
}
func (a *App) run(w http.ResponseWriter, r *http.Request) {
	u, ok := a.apiUser(w, r)
	if !ok {
		return
	}
	if !a.rate(r, "run", 120) {
		fail(w, 429, "调用频率超限")
		return
	}
	var b struct {
		Tool       string         `json:"tool"`
		Endpoint   string         `json:"endpoint"`
		Parameters map[string]any `json:"parameters"`
	}
	if !decode(w, r, &b) {
		return
	}
	var raw []byte
	var provider Tool
	e := a.db.QueryRow(r.Context(), `SELECT to_jsonb(t) FROM tools t WHERE slug=$1 AND enabled=true`, b.Tool).Scan(&raw)
	if e != nil || json.Unmarshal(raw, &provider) != nil {
		fail(w, 404, "工具不存在或未启用")
		return
	}
	name, base, header, prefix := provider.Name, provider.BaseURL, provider.AuthHeader, provider.AuthPrefix
	secret := ""
	if provider.Secret != nil {
		secret = *provider.Secret
	}
	data, _ := json.Marshal(provider.Endpoints)
	var eps []Endpoint
	if json.Unmarshal(data, &eps) != nil {
		fail(w, 500, "工具配置错误")
		return
	}
	var ep Endpoint
	for _, v := range eps {
		if v.ID == b.Endpoint {
			ep = v
			break
		}
	}
	if ep.ID == "" || ep.Disabled || ep.Missing {
		fail(w, 404, "接口不存在")
		return
	}
	ep.Price = effectivePrice(provider, ep)
	if b.Parameters == nil {
		b.Parameters = map[string]any{}
	}
	var req *http.Request
	if !isMCP(provider) {
		payload, _ := json.Marshal(b.Parameters)
		target := strings.TrimRight(base, "/") + ep.Path
		if ep.Method == "GET" {
			q := url.Values{}
			for k, v := range b.Parameters {
				switch x := v.(type) {
				case string:
					q.Set(k, x)
				case float64, bool:
					q.Set(k, fmt.Sprint(x))
				default:
					j, _ := json.Marshal(x)
					q.Set(k, string(j))
				}
			}
			target += "?" + q.Encode()
			payload = nil
		}
		req, e = http.NewRequestWithContext(r.Context(), ep.Method, target, strings.NewReader(string(payload)))
		if e != nil {
			fail(w, 400, "上游请求配置无效")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if secret != "" {
			key, err := a.decrypt(secret)
			if err != nil {
				fail(w, 500, "无法读取上游密钥")
				return
			}
			req.Header.Set(header, prefix+key)
		}
	}
	tx, e := a.db.Begin(r.Context())
	if e != nil {
		fail(w, 500, "无法预留余额")
		return
	}
	defer tx.Rollback(r.Context())
	var bal, daily, call int64
	var de, ce bool
	e = tx.QueryRow(r.Context(), `SELECT balance,daily_limit,call_limit,daily_enabled,call_enabled FROM users WHERE id=$1 FOR UPDATE`, u.ID).Scan(&bal, &daily, &call, &de, &ce)
	if e != nil {
		fail(w, 500, "无法读取余额")
		return
	}
	var spent int64
	e = tx.QueryRow(r.Context(), `SELECT COALESCE(sum(cost),0) FROM usage WHERE user_id=$1 AND status IN ('success','running') AND created_at >= date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, u.ID).Scan(&spent)
	if e != nil {
		fail(w, 500, "无法读取预算")
		return
	}
	if bal < ep.Price {
		fail(w, 402, "余额不足，请联系管理员充值")
		return
	}
	if ce && ep.Price > call || de && spent+ep.Price > daily {
		fail(w, 403, "调用超出预算限制")
		return
	}
	var runID int64
	e = tx.QueryRow(r.Context(), `INSERT INTO usage(user_id,tool_name,endpoint,cost) VALUES($1,$2,$3,$4) RETURNING id`, u.ID, name, ep.Path, ep.Price).Scan(&runID)
	if e != nil {
		fail(w, 500, "创建调用记录失败")
		return
	}
	_, e = tx.Exec(r.Context(), `UPDATE users SET balance=balance-$2 WHERE id=$1`, u.ID, ep.Price)
	if e != nil || tx.Commit(r.Context()) != nil {
		fail(w, 500, "预留余额失败")
		return
	}
	start := time.Now()
	var body []byte
	status := 0
	var callErr error
	if isMCP(provider) {
		callCtx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		body, callErr = a.callMCP(callCtx, provider, ep, b.Parameters)
		cancel()
		if callErr == nil {
			status = 200
		} else {
			status = 502
		}
	} else {
		var response *http.Response
		response, callErr = a.client.Do(req)
		if callErr == nil {
			status = response.StatusCode
			body, callErr = io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
			response.Body.Close()
			if len(body) > 8<<20 {
				callErr = errors.New("response too large")
			}
		}
	}
	success := callErr == nil && status >= 200 && status < 300
	finishCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e = a.settle(finishCtx, runID, u.ID, ep.Price, success, status, time.Since(start).Milliseconds()); e != nil {
		log.Printf("settle run %d: %v", runID, e)
		fail(w, 503, "结算暂未完成，请在调用记录中查看状态，勿自动重试")
		return
	}
	if !success {
		fail(w, 502, fmt.Sprintf("上游调用失败（HTTP %d），预留余额已退回。调用编号：%d", status, runID))
		return
	}
	var result any
	if json.Unmarshal(body, &result) != nil {
		result = string(body)
	}
	writeJSON(w, 200, map[string]any{"id": runID, "cost_micros": ep.Price, "data": result})
}
func (a *App) settle(ctx context.Context, runID, userID, cost int64, success bool, status int, duration int64) error {
	tx, e := a.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var locked int64
	if e = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&locked); e != nil {
		return e
	}
	state := "failed"
	if success {
		state = "success"
	}
	result, e := tx.Exec(ctx, `UPDATE usage SET status=$2,http_status=$3,duration_ms=$4 WHERE id=$1 AND status='running'`, runID, state, status, duration)
	if e != nil {
		return e
	}
	if result.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if !success {
		_, e = tx.Exec(ctx, `UPDATE users SET balance=balance+$2 WHERE id=$1`, userID, cost)
	} else {
		_, e = tx.Exec(ctx, `INSERT INTO transactions(user_id,amount,kind,note) VALUES($1,$2,'usage',$3)`, userID, -cost, fmt.Sprintf("调用 #%d", runID))
	}
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// Calls are capped at 60s. Recover reservations left behind by a process crash after 5 minutes.
func (a *App) recoverRuns(ctx context.Context) {
	rows, e := a.db.Query(ctx, `SELECT id,user_id,cost FROM usage WHERE status='running' AND created_at<now()-interval '5 minutes'`)
	if e != nil {
		log.Print(e)
		return
	}
	type pending struct{ id, user, cost int64 }
	out := []pending{}
	for rows.Next() {
		var p pending
		if rows.Scan(&p.id, &p.user, &p.cost) == nil {
			out = append(out, p)
		}
	}
	rows.Close()
	for _, p := range out {
		if e := a.settle(ctx, p.id, p.user, p.cost, false, 0, 0); e != nil {
			log.Print(e)
		}
	}
}
func (a *App) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if a.db.Ping(r.Context()) != nil || a.redis.Ping(r.Context()).Err() != nil {
			fail(w, 503, "依赖服务不可用")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	m.HandleFunc("POST /api/auth/register", a.auth)
	m.HandleFunc("POST /api/auth/login", a.auth)
	m.HandleFunc("GET /api/auth/me", a.withUser(false, func(w http.ResponseWriter, r *http.Request, u User) {
		s, _ := a.session(r)
		writeJSON(w, 200, map[string]any{"user": u, "csrf": s.CSRF})
	}))
	m.HandleFunc("POST /api/auth/logout", a.withUser(false, func(w http.ResponseWriter, r *http.Request, u User) {
		c, _ := r.Cookie("tv_session")
		if e := a.redis.Del(r.Context(), "session:"+hash(c.Value)).Err(); e != nil {
			fail(w, 503, "退出失败，请重试")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "tv_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode})
		writeJSON(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /api/tools", a.withUser(false, func(w http.ResponseWriter, r *http.Request, u User) { a.listTools(w, r, false) }))
	m.HandleFunc("GET /api/keys", a.withUser(false, a.keys))
	m.HandleFunc("POST /api/keys", a.withUser(false, a.keys))
	m.HandleFunc("DELETE /api/keys/{id}", a.withUser(false, a.keys))
	m.HandleFunc("GET /api/budgets", a.withUser(false, a.budget))
	m.HandleFunc("PUT /api/budgets", a.withUser(false, a.budget))
	m.HandleFunc("GET /api/usage", a.withUser(false, func(w http.ResponseWriter, r *http.Request, u User) {
		a.rows(w, r, `SELECT row_to_json(x) FROM (SELECT * FROM usage WHERE user_id=$1 ORDER BY id DESC LIMIT 200) x`, u.ID)
	}))
	m.HandleFunc("GET /api/transactions", a.withUser(false, func(w http.ResponseWriter, r *http.Request, u User) {
		a.rows(w, r, `SELECT row_to_json(x) FROM (SELECT id,amount,kind,note,created_at FROM transactions WHERE user_id=$1 ORDER BY id DESC LIMIT 200) x`, u.ID)
	}))
	m.HandleFunc("POST /api/admin/tools/{id}/sync", a.withUser(true, a.syncMCP))
	m.HandleFunc("POST /api/admin/tools/{id}/bulk", a.withUser(true, a.bulkMCP))
	m.HandleFunc("GET /api/admin/tools", a.withUser(true, func(w http.ResponseWriter, r *http.Request, u User) { a.listTools(w, r, true) }))
	m.HandleFunc("POST /api/admin/tools", a.withUser(true, a.saveTool))
	m.HandleFunc("GET /api/admin/users", a.withUser(true, func(w http.ResponseWriter, r *http.Request, u User) {
		a.rows(w, r, `SELECT row_to_json(x) FROM (SELECT id,name,email,role,balance,created_at FROM users ORDER BY id DESC LIMIT 1000) x`)
	}))
	m.HandleFunc("POST /api/admin/topup", a.withUser(true, a.topup))
	m.HandleFunc("GET /api/admin/audit", a.withUser(true, func(w http.ResponseWriter, r *http.Request, u User) {
		a.rows(w, r, `SELECT row_to_json(x) FROM (SELECT a.id,a.action,a.target_id,a.created_at,u.email AS actor FROM audit_log a JOIN users u ON u.id=a.actor_id ORDER BY a.id DESC LIMIT 200) x`)
	}))
	m.HandleFunc("GET /v1/tools", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.apiUser(w, r); ok {
			a.listTools(w, r, false)
		}
	})
	m.HandleFunc("POST /v1/run", a.run)
	m.HandleFunc("GET /SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		fmt.Fprintf(w, "# T vendo tools\n\nAsk the user for their T vendo API key. Never send this key to any other host.\n\nAPI base: %s\n\n1. GET /v1/tools with Authorization: Bearer <key> to discover enabled tools and their endpoint IDs, prices (integer micro-USD), and parameter examples.\n2. POST /v1/run with Authorization: Bearer <key> and JSON {\"tool\":\"slug\",\"endpoint\":\"endpoint-id\",\"parameters\":{}}. Confirm paid calls with the user.\n3. Result is {id,cost_micros,data}. Failed calls refund the reservation. Do not automatically retry calls with uncertain settlement.\n", a.publicURL)
	})
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "接口不存在") })
	m.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "接口不存在") })
	dist := env("FRONTEND_DIST", "../frontend/dist")
	fs := http.FileServer(http.Dir(dist))
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		if _, e := os.Stat(dist + r.URL.Path); e == nil && r.URL.Path != "/" {
			fs.ServeHTTP(w, r)
		} else {
			http.ServeFile(w, r, dist+"/index.html")
		}
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != a.publicURL {
				fail(w, 403, "来源不被允许")
				return
			}
		}
		m.ServeHTTP(w, r)
	})
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	key, e := hex.DecodeString(os.Getenv("ENCRYPTION_KEY"))
	if e != nil || len(key) != 32 {
		log.Fatal("ENCRYPTION_KEY must be 64 hex characters")
	}
	block, _ := aes.NewCipher(key)
	crypt, _ := cipher.NewGCM(block)
	db, e := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	if e = db.Ping(ctx); e != nil {
		log.Fatal(e)
	}
	opt, e := redis.ParseURL(env("REDIS_URL", "redis://localhost:6379/0"))
	if e != nil {
		log.Fatal(e)
	}
	rc := redis.NewClient(opt)
	defer rc.Close()
	if e = rc.Ping(ctx).Err(); e != nil {
		log.Fatal(e)
	}
	a := &App{db: db, redis: rc, crypt: crypt, publicURL: strings.TrimRight(env("PUBLIC_URL", "http://localhost:8080"), "/"), secure: env("COOKIE_SECURE", "false") == "true", client: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{DialContext: safeDial, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 55 * time.Second, MaxIdleConns: 50}}}
	if e = a.bootstrap(ctx); e != nil {
		log.Fatal(e)
	}
	a.recoverRuns(ctx)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.recoverRuns(ctx)
			}
		}
	}()
	server := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 80 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 16}
	go func() {
		log.Printf("T vendo listening on %s", server.Addr)
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Fatal(e)
		}
	}()
	<-ctx.Done()
	stop, c := context.WithTimeout(context.Background(), 75*time.Second)
	defer c()
	_ = server.Shutdown(stop)
}
