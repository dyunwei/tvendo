// Package cli implements Tvendo's non-interactive agent client.
package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type config struct {
	Server string `json:"server"`
	Key    string `json:"api_key,omitempty"`
}
type failure struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	RequestID  string `json:"request_id,omitempty"`
	RunID      any    `json:"run_id,omitempty"`
	Settlement string `json:"settlement,omitempty"`
	Exit       int    `json:"-"`
	Result     any    `json:"result,omitempty"`
	Cost       any    `json:"cost_micros,omitempty"`
}

func (f *failure) Error() string { return f.Message }
func bad(message string) *failure {
	return &failure{Code: "INVALID_ARGUMENT", Message: message, Exit: 2}
}

type command struct {
	args  []string
	flags map[string]string
	json  bool
}

func parse(args []string) (command, error) {
	c := command{flags: map[string]string{}}
	bools := map[string]bool{"json": true, "help": true, "version": true, "key-stdin": true}
	values := map[string]bool{"server": true, "limit": true, "cursor": true, "request-id": true, "input": true, "input-file": true, "output": true, "max-cost-micros": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			c.args = append(c.args, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") {
			c.args = append(c.args, arg)
			continue
		}
		name, value, has := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if _, exists := c.flags[name]; exists {
			return c, bad("参数重复: --" + name)
		}
		if bools[name] {
			if has {
				return c, bad("开关不接收值: --" + name)
			}
			c.flags[name] = "true"
		} else if values[name] {
			if !has {
				i++
				if i >= len(args) || strings.HasPrefix(args[i], "--") {
					return c, bad("参数缺少值: --" + name)
				}
				value = args[i]
			}
			c.flags[name] = value
		} else {
			return c, bad("未知参数: --" + name)
		}
	}
	c.json = c.flags["json"] != ""
	return c, nil
}

const help = `Tvendo：让 Agent 搜索并调用工具

  tvendo setup --server <url> [--key-stdin]
  tvendo doctor
  tvendo tools search "需求" [--limit 10] [--cursor <值>]
  tvendo tools show <tool_ref>
  tvendo run <tool_ref> [--input <JSON> | --input-file <文件或->]
             [--max-cost-micros <整数>] [--output <新文件>]
  tvendo runs list [--limit 20] [--cursor <值>] [--request-id <id>]
  tvendo runs get <id>
  tvendo wallet balance
  tvendo budget show

所有命令支持 --json、--help。--version 查看版本。
配置：TVENDO_SERVER、TVENDO_API_KEY；TVENDO_CONFIG 可指定配置文件。
金额单位为整数微美元，1000000 = 1 USD。run 不会自动重试。
`

func Main(args []string, in io.Reader, out, stderr io.Writer, version string) int {
	c, err := parse(args)
	// Preserve JSON error output even if parsing fails before --json is reached.
	machine := c.json
	for _, s := range args {
		if s == "--json" {
			machine = true
		}
	}
	var data any
	if err == nil {
		if c.flags["version"] != "" {
			if machine {
				data = map[string]any{"version": version}
			} else {
				fmt.Fprintln(out, version)
				return 0
			}
		} else if c.flags["help"] != "" || len(c.args) == 0 {
			if machine {
				data = map[string]any{"help": help}
			} else {
				fmt.Fprint(out, help)
				return 0
			}
		} else {
			data, err = execute(c, in, stderr, version)
		}
	}
	if err != nil {
		var f *failure
		if !errors.As(err, &f) {
			f = &failure{Code: "CLIENT_ERROR", Message: err.Error(), Exit: 1}
		}
		if machine {
			_ = json.NewEncoder(out).Encode(map[string]any{"ok": false, "error": f})
		} else {
			fmt.Fprintln(stderr, f.Message)
			if f.RequestID != "" {
				fmt.Fprintln(stderr, "请求标识:", f.RequestID)
			}
			if f.RunID != nil {
				fmt.Fprintln(stderr, "调用编号:", f.RunID)
			}
		}
		return f.Exit
	}
	if machine {
		_ = json.NewEncoder(out).Encode(map[string]any{"ok": true, "data": data})
	} else {
		b, _ := json.MarshalIndent(data, "", "  ")
		fmt.Fprintln(out, string(b))
	}
	return 0
}
func configPath() (string, error) {
	if p := os.Getenv("TVENDO_CONFIG"); p != "" {
		return p, nil
	}
	root, e := os.UserConfigDir()
	return filepath.Join(root, "tvendo", "config.json"), e
}
func readConfig() (config, string, error) {
	var cfg config
	p, e := configPath()
	if e != nil {
		return cfg, p, bad("无法确定配置目录，请设置 TVENDO_CONFIG")
	}
	b, e := os.ReadFile(p)
	if errors.Is(e, os.ErrNotExist) {
		return cfg, p, nil
	}
	if e != nil {
		return cfg, p, bad("无法读取配置文件")
	}
	if len(b) > 64<<10 || json.Unmarshal(b, &cfg) != nil {
		return cfg, p, bad("配置文件格式无效")
	}
	return cfg, p, nil
}
func saveConfig(path string, cfg config) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return bad("无法创建配置目录")
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".tvendo-*")
	if e != nil {
		return bad("无法写入配置文件")
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		e = json.NewEncoder(f).Encode(cfg)
	}
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return bad("保存配置失败")
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return bad("替换配置失败")
	}
	return nil
}
func serverURL(s string) (string, error) {
	u, e := url.Parse(s)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return "", bad("服务地址须为完整来源，例如 https://tools.example.com，不含路径、凭证或查询参数")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	local := host == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", bad("服务地址须使用 HTTPS，本机开发可使用 HTTP")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
func execute(c command, in io.Reader, stderr io.Writer, version string) (any, error) {
	args := c.args
	kind := args[0]
	if len(args) > 1 && (kind == "tools" || kind == "runs" || kind == "wallet" || kind == "budget") {
		kind += " " + args[1]
		args = args[2:]
	} else {
		args = args[1:]
	}
	allowed := map[string]string{"setup": "server key-stdin", "doctor": "", "tools search": "limit cursor", "tools show": "", "run": "input input-file max-cost-micros output", "runs list": "limit cursor request-id", "runs get": "", "wallet balance": "", "budget show": ""}
	opts, exists := allowed[kind]
	if !exists {
		return nil, bad("未知命令，请运行 tvendo --help")
	}
	for k := range c.flags {
		if k != "json" && !strings.Contains(" "+opts+" ", " "+k+" ") {
			return nil, bad("此命令不支持 --" + k)
		}
	}
	needs := kind == "tools search" || kind == "tools show" || kind == "run" || kind == "runs get"
	if needs && len(args) != 1 || !needs && len(args) != 0 {
		return nil, bad("命令参数数量不正确，请运行 tvendo --help")
	}
	cfg, path, e := readConfig()
	if e != nil {
		return nil, e
	}
	old := cfg.Server
	if s := os.Getenv("TVENDO_SERVER"); s != "" {
		cfg.Server = s
	}
	if old == "" {
		old = cfg.Server
	}
	if s, ok := c.flags["server"]; ok {
		cfg.Server = s
	}
	cfg.Server, e = serverURL(cfg.Server)
	if e != nil {
		return nil, e
	}
	if old != "" {
		old, e = serverURL(old)
		if e != nil {
			return nil, bad("已保存的服务地址无效")
		}
	}
	envKey := os.Getenv("TVENDO_API_KEY")
	if old != "" && cfg.Server != old {
		cfg.Key = ""
		if envKey != "" {
			return nil, bad("服务地址已改变，请先清除旧 TVENDO_API_KEY，再为新服务配置密钥")
		}
	}
	if envKey != "" {
		cfg.Key = envKey
	}
	if c.flags["key-stdin"] != "" {
		if envKey != "" {
			return nil, bad("请先清除 TVENDO_API_KEY 再使用 --key-stdin，避免配置歧义")
		}
		b, e := io.ReadAll(io.LimitReader(in, 4097))
		if e != nil || len(b) > 4096 {
			return nil, bad("无法读取密钥")
		}
		cfg.Key = strings.TrimSpace(string(b))
	}
	if !strings.HasPrefix(cfg.Key, "tv_live_") || strings.ContainsAny(cfg.Key, "\r\n\t ") {
		return nil, &failure{Code: "AUTH_REQUIRED", Message: "请在 " + cfg.Server + "/app/keys 创建平台密钥，通过 TVENDO_API_KEY 或 setup --key-stdin 配置", Exit: 3}
	}
	client := &apiClient{server: cfg.Server, key: cfg.Key, http: &http.Client{Timeout: 95 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	switch kind {
	case "setup", "doctor":
		info, e := client.call("GET", "/v1/info", nil, "")
		if e != nil {
			return nil, e
		}
		info = unwrap(info)
		if fmt.Sprint(info["api_version"]) != "1" {
			return nil, &failure{Code: "INCOMPATIBLE_API", Message: "服务 API 版本不兼容，请更新 CLI", Exit: 2}
		}
		balance, e := client.call("GET", "/v1/balance", nil, "")
		if e != nil {
			return nil, e
		}
		if kind == "setup" {
			saved := cfg
			if envKey != "" {
				saved.Key = ""
			}
			if e = saveConfig(path, saved); e != nil {
				return nil, e
			}
		}
		return map[string]any{"server": cfg.Server, "cli_version": version, "api_version": 1, "authenticated": true, "balance": unwrap(balance), "config_saved": kind == "setup"}, nil
	case "tools search", "runs list":
		q := url.Values{}
		for _, k := range []string{"limit", "cursor", "request-id"} {
			if v, ok := c.flags[k]; ok {
				if k == "limit" {
					n, e := strconv.Atoi(v)
					if e != nil || n < 1 || n > 50 {
						return nil, bad("limit 应为 1–50")
					}
				}
				q.Set(strings.ReplaceAll(k, "-", "_"), v)
			}
		}
		endpoint := "/v1/runs"
		if kind == "tools search" {
			q.Set("q", args[0])
			endpoint = "/v1/catalog"
		}
		res, e := client.call("GET", endpoint+"?"+q.Encode(), nil, "")
		return unwrap(res), e
	case "tools show":
		res, e := client.call("GET", "/v1/catalog/"+url.PathEscape(args[0]), nil, "")
		return unwrap(res), e
	case "runs get":
		id, e := strconv.ParseInt(args[0], 10, 64)
		if e != nil || id <= 0 {
			return nil, bad("调用编号应为正整数")
		}
		res, e := client.call("GET", "/v1/runs/"+args[0], nil, "")
		return unwrap(res), e
	case "wallet balance", "budget show":
		endpoint := "/v1/balance"
		if kind == "budget show" {
			endpoint = "/v1/budgets"
		}
		res, e := client.call("GET", endpoint, nil, "")
		return unwrap(res), e
	case "run":
		return run(c, args[0], in, stderr, client)
	}
	return nil, bad("未知命令")
}
func unwrap(m map[string]any) map[string]any {
	if d, ok := m["data"].(map[string]any); ok && m["ok"] == true {
		return d
	}
	return m
}

type apiClient struct {
	server, key string
	http        *http.Client
}

func (c *apiClient) call(method, path string, body any, requestID string) (map[string]any, error) {
	var buf bytes.Buffer
	if body != nil {
		if e := json.NewEncoder(&buf).Encode(body); e != nil {
			return nil, bad("请求编码失败")
		}
	}
	req, e := http.NewRequest(method, c.server+path, &buf)
	if e != nil {
		return nil, bad("请求地址无效")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	res, e := c.http.Do(req)
	if e != nil {
		return nil, &failure{Code: "NETWORK_UNKNOWN", Message: "连接失败或响应丢失；收费调用请先按请求标识查询记录，勿自动重试", RequestID: requestID, Settlement: "unknown", Exit: 7}
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, (10<<20)+1))
	if e != nil || len(raw) > 10<<20 {
		return nil, &failure{Code: "RESPONSE_UNKNOWN", Message: "响应不完整或过大，请先核对调用记录", RequestID: requestID, Settlement: "unknown", Exit: 7}
	}
	var data map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	e = dec.Decode(&data)
	if res.StatusCode >= 200 && res.StatusCode < 300 && e == nil && data != nil {
		return data, nil
	}
	f := &failure{Code: "API_ERROR", Message: fmt.Sprintf("服务请求失败（HTTP %d）", res.StatusCode), Exit: 1, RequestID: requestID}
	if message, ok := data["error"].(string); ok {
		f.Message = strings.ReplaceAll(message, c.key, "[已隐藏]")
	}
	if code, ok := data["code"].(string); ok {
		f.Code = code
	}
	if id, ok := data["id"]; ok {
		f.RunID = id
	}
	if s, ok := data["settlement"].(string); ok {
		f.Settlement = s
	}
	switch res.StatusCode {
	case 400:
		f.Exit = 2
	case 401:
		f.Exit = 3
	case 402, 403:
		f.Exit = 4
	case 404:
		f.Exit = 5
	case 502:
		if f.Settlement == "refunded" {
			f.Exit = 6
		} else {
			f.Exit = 7
		}
	case 503:
		f.Exit = 7
	}
	if method == "POST" && res.StatusCode >= 400 && res.StatusCode < 500 && f.Settlement == "" {
		f.Settlement = "not_started"
	}
	if method == "POST" && f.Settlement != "refunded" && f.Settlement != "not_started" {
		f.Exit = 7
		f.Settlement = "unknown"
	}
	if e != nil || data == nil {
		f.Code = "INVALID_RESPONSE"
		f.Message = "服务未返回有效 JSON，请核对服务地址与调用记录"
		f.Exit = 7
	}
	return nil, f
}
func run(c command, ref string, in io.Reader, stderr io.Writer, client *apiClient) (any, error) {
	raw := []byte("{}")
	inline, hasInline := c.flags["input"]
	file, hasFile := c.flags["input-file"]
	if hasInline && hasFile {
		return nil, bad("--input 和 --input-file 不能同时使用")
	}
	if hasInline {
		raw = []byte(inline)
	}
	if hasFile {
		var reader io.Reader = in
		var f *os.File
		if file != "-" {
			var e error
			f, e = os.Open(file)
			if e != nil {
				return nil, bad("无法打开输入文件")
			}
			defer f.Close()
			reader = f
		}
		var e error
		raw, e = io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		if e != nil {
			return nil, bad("无法读取输入")
		}
	}
	if len(raw) > 1<<20 {
		return nil, bad("输入超过 1 MB")
	}
	var params map[string]json.RawMessage
	if json.Unmarshal(raw, &params) != nil || params == nil {
		return nil, bad("输入必须是 JSON 对象")
	}
	body := map[string]any{"tool_ref": ref, "parameters": params}
	if s, ok := c.flags["max-cost-micros"]; ok {
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil || n < 0 {
			return nil, bad("费用上限须为非负整数微美元")
		}
		body["max_cost_micros"] = n
	}
	// Reserve a new output path before any paid call. Never truncate existing data.
	var output *os.File
	completed := false
	if path, ok := c.flags["output"]; ok {
		var e error
		output, e = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, bad("无法创建结果文件（文件可能已存在），未发起调用")
		}
		defer func() {
			output.Close()
			if !completed {
				os.Remove(path)
			}
		}()
	}
	token := make([]byte, 16)
	if _, e := rand.Read(token); e != nil {
		return nil, bad("无法生成请求标识")
	}
	requestID := hex.EncodeToString(token)
	fmt.Fprintln(stderr, "请求标识:", requestID)
	res, e := client.call("POST", "/v1/run", body, requestID)
	if e != nil {
		return nil, e
	}
	result := map[string]any{"run_id": res["id"], "request_id": requestID, "status": "success", "cost_micros": res["cost_micros"], "result": res["data"]}
	if output != nil {
		encErr := json.NewEncoder(output).Encode(res["data"])
		syncErr := output.Sync()
		closeErr := output.Close()
		if encErr != nil || syncErr != nil || closeErr != nil {
			return nil, &failure{Code: "OUTPUT_WRITE_FAILED", Message: "调用已成功但保存结果失败，勿重新执行收费调用", RunID: res["id"], RequestID: requestID, Settlement: "charged", Exit: 1, Result: res["data"], Cost: res["cost_micros"]}
		}
		completed = true
	}
	return result, nil
}
