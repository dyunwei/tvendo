package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

func (u User) allowsTool(ref string) bool {
	return len(u.KeyTools) == 0 || slices.Contains(u.KeyTools, ref)
}

func (u User) runKeyScope() int64 {
	if len(u.KeyTools) > 0 {
		return u.KeyID
	}
	return 0
}

func (a *App) accountMailConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]bool{"email_enabled": a.mailer != nil})
}

func (a *App) queueAccountLink(ctx context.Context, userID int64, address, purpose string) {
	if a.mailSlots == nil {
		a.sendAccountLink(ctx, userID, address, purpose)
		return
	}
	select {
	case a.mailSlots <- struct{}{}:
		go func() {
			defer func() { <-a.mailSlots }()
			task, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			a.sendAccountLink(task, userID, address, purpose)
		}()
	default:
		log.Printf("account mail queue full: user_id=%d purpose=%s", userID, purpose)
	}
}

func (a *App) sendAccountLink(ctx context.Context, userID int64, address, purpose string) {
	token := randomToken()
	ttl := 30 * time.Minute
	subject, path := "Tvendo 密码重置", "/reset-password"
	if purpose == "verify" {
		ttl = 24 * time.Hour
		subject, path = "Tvendo 邮箱验证", "/verify-email"
	}
	_, err := a.db.Exec(ctx, `
		INSERT INTO account_tokens(token_hash,user_id,purpose,expires_at)
		VALUES($1,$2,$3,now()+$4::bigint*interval '1 second')`,
		hash(token), userID, purpose, int64(ttl/time.Second))
	if err == nil {
		// Fragment keeps the recovery token out of HTTP access logs and referrers.
		err = a.mailer(ctx, address, subject, "请在有效期内打开此链接：\r\n"+a.publicURL+path+"#"+token+"\r\n")
	}
	if err != nil {
		log.Printf("account email delivery failed: user_id=%d purpose=%s", userID, purpose)
	}
}

func (a *App) requestAccountLink(purpose string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.mailer == nil {
			fail(w, 503, "本站未配置邮件服务，请联系管理员")
			return
		}
		if !a.rateAllowed(w, r, trafficLimit{"account-mail:ip:" + a.clientIP(r), 8, minute}) {
			return
		}
		var input struct {
			Email string `json:"email"`
		}
		if !decode(w, r, &input) {
			return
		}
		address, valid := emailAddress(input.Email)
		if !valid {
			fail(w, 400, "请输入有效邮箱")
			return
		}
		if !a.rateAllowed(w, r, trafficLimit{"account-mail:address:" + hash(address), 4, 15 * minute}, trafficLimit{"account-mail:all", 60, minute}) {
			return
		}
		var id int64
		err := a.db.QueryRow(r.Context(), `
			SELECT id FROM users WHERE email=$1 AND disabled=false AND email_verified=($2='reset')`, address, purpose).Scan(&id)
		if err == nil {
			a.queueAccountLink(r.Context(), id, address, purpose)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			fail(w, 503, "暂时无法处理邮件请求")
			return
		}
		writeJSON(w, 202, map[string]bool{"accepted": true})
	}
}

func (a *App) consumeAccountLink(purpose string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.rateAllowed(w, r, trafficLimit{"account-link:ip:" + a.clientIP(r), 15, minute}) {
			return
		}
		var input struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if !decode(w, r, &input) {
			return
		}
		if len(input.Token) != 64 || !requestIDPattern.MatchString(input.Token) {
			fail(w, 400, "链接无效或已过期")
			return
		}
		if purpose == "reset" && (len(input.Password) < 10 || len(input.Password) > 72) {
			fail(w, 400, "新密码长度需为 10–72 字节")
			return
		}
		ctx := r.Context()
		tx, err := a.db.Begin(ctx)
		if err != nil {
			fail(w, 503, "暂时无法处理链接")
			return
		}
		defer tx.Rollback(ctx)
		var userID int64
		err = tx.QueryRow(ctx, `
			SELECT u.id FROM account_tokens t JOIN users u ON t.user_id=u.id
			WHERE t.token_hash=$1 AND t.purpose=$2 AND t.expires_at>now()
			AND NOT u.disabled AND u.email_verified=($2='reset') FOR UPDATE OF u`, hash(input.Token), purpose).Scan(&userID)
		if err == nil {
			var used int64
			err = tx.QueryRow(ctx, `
				DELETE FROM account_tokens WHERE token_hash=$1 AND expires_at>now() RETURNING user_id`, hash(input.Token)).Scan(&used)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 400, "链接无效或已过期")
			return
		}
		if err == nil && purpose == "verify" {
			_, err = tx.Exec(ctx, `UPDATE users SET email_verified=true WHERE id=$1`, userID)
		} else if err == nil {
			var encoded []byte
			encoded, err = bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE users SET password=$2,session_epoch=session_epoch+1 WHERE id=$1`, userID, string(encoded))
			}
		}
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM account_tokens WHERE user_id=$1 AND purpose=$2`, userID, purpose)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err != nil {
			fail(w, 503, "暂时无法处理链接")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}

func (a *App) changePassword(w http.ResponseWriter, r *http.Request, u User) {
	if !a.rateAllowed(w, r, trafficLimit{"account-password:" + strconv.FormatInt(u.ID, 10), 8, minute}) {
		return
	}
	var input struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.New) < 10 || len(input.New) > 72 || len(input.Current) > 72 {
		fail(w, 400, "新密码长度需为 10–72 字节")
		return
	}
	ctx := r.Context()
	var old string
	if err := a.db.QueryRow(ctx, `SELECT password FROM users WHERE id=$1`, u.ID).Scan(&old); err != nil {
		fail(w, 503, "暂时无法修改密码")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(old), []byte(input.Current)) != nil {
		fail(w, 400, "当前密码不正确")
		return
	}
	encoded, err := bcrypt.GenerateFromPassword([]byte(input.New), bcrypt.DefaultCost)
	if err != nil {
		fail(w, 503, "暂时无法修改密码")
		return
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		fail(w, 503, "暂时无法修改密码")
		return
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `
		UPDATE users SET password=$2,session_epoch=session_epoch+1
		WHERE id=$1 AND password=$3 AND session_epoch=$4 AND NOT disabled RETURNING session_epoch`,
		u.ID, string(encoded), old, u.SessionEpoch).Scan(&u.SessionEpoch)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 409, "账户状态已更新，请重新登录")
		return
	}
	if err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM account_tokens WHERE user_id=$1 AND purpose='reset'`, u.ID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		fail(w, 503, "暂时无法修改密码")
		return
	}
	a.newSession(w, r, u)
}

func (a *App) setUserDisabled(w http.ResponseWriter, r *http.Request, actor User) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || userID <= 0 {
		fail(w, 400, "用户编号无效")
		return
	}
	var input struct {
		Disabled *bool `json:"disabled"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Disabled == nil {
		fail(w, 400, "请选择账户状态")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		fail(w, 503, "暂时无法更改账户状态")
		return
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `
		UPDATE users SET disabled=$2,session_epoch=session_epoch+1
		WHERE id=$1 AND role<>'admin' AND disabled<>$2`, userID, *input.Disabled)
	if err == nil && result.RowsAffected() == 0 {
		var role string
		err = tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1`, userID).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 404, "用户不存在")
			return
		}
		if role == "admin" {
			fail(w, 403, "管理员账户不能停用")
			return
		}
	}
	if err == nil && result.RowsAffected() > 0 {
		action := "enable_user"
		if *input.Disabled {
			action = "disable_user"
			_, err = tx.Exec(ctx, `DELETE FROM account_tokens WHERE user_id=$1`, userID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO audit_log(actor_id,action,target_id) VALUES($1,$2,$3)`, actor.ID, action, userID)
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		fail(w, 503, "暂时无法更改账户状态")
		return
	}
	writeJSON(w, 200, map[string]bool{"disabled": *input.Disabled})
}

func (a *App) purgeAccountTokens(ctx context.Context) {
	if _, err := a.db.Exec(ctx, `DELETE FROM account_tokens WHERE expires_at<=now()`); err != nil {
		log.Printf("purge expired account tokens: %v", err)
	}
}
