package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func (a *App) bootstrap(ctx context.Context) error {
	tx, e := a.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// Transaction-scoped lock makes first startup safe when multiple instances start together.
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(83912011)`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, schema); e != nil {
		return e
	}
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE role='admin')`).Scan(&exists); e != nil {
		return e
	}
	if !exists {
		email := strings.ToLower(strings.TrimSpace(os.Getenv("ADMIN_EMAIL")))
		password := os.Getenv("ADMIN_PASSWORD")
		if !strings.Contains(email, "@") || len(password) < 12 || len(password) > 72 || strings.HasPrefix(password, "replace-") {
			return errors.New("first startup requires ADMIN_EMAIL and a strong ADMIN_PASSWORD (12–72 bytes)")
		}
		pw, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if _, e = tx.Exec(ctx, `INSERT INTO users(email,name,password,role) VALUES($1,$2,$3,'admin')`, email, env("ADMIN_NAME", "Administrator"), string(pw)); e != nil {
			return e
		}
	}
	// Disabled examples describe the configuration format; they never pretend to be live integrations.
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM tools`).Scan(&count); e != nil {
		return e
	}
	if count == 0 {
		endpoints := []Endpoint{
			{ID: "search", Name: "TinyFish Web Search", Method: "POST", Path: "/search", Description: "搜索网页并获取结构化结果。此示例需按上游实际文档核对后启用。", Price: 1000, Parameters: json.RawMessage(`{"query":"AI agent tools"}`)},
			{ID: "fetch", Name: "TinyFish Fetch", Method: "POST", Path: "/fetch", Description: "提取网页内容。请根据实际账户套餐填写价格和参数。", Price: 2000, Parameters: json.RawMessage(`{"url":"https://example.com"}`)},
		}
		data, _ := json.Marshal(endpoints)
		_, e = tx.Exec(ctx, `INSERT INTO tools(slug,name,category,description,website,docs,icon,base_url,endpoints,resources,enabled) VALUES('tinyfish','TinyFish','搜索与 SEO','网页搜索、内容提取和 AI 浏览器。配置示例，需核实上游 API 后启用。','https://tinyfish.ai','https://docs.tinyfish.ai','🐟','',$1,'[]',false)`, data)
		if e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
