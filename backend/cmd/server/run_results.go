package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
	"tvendo/internal/billing"

	"github.com/jackc/pgx/v5"
)

type runQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (a *App) replayRun(w http.ResponseWriter, r *http.Request, db runQuerier, userID int64, key, fingerprint, requestID string) bool {
	var runID, cost int64
	var originalRequestID, originalFingerprint, state, message, encrypted string
	err := db.QueryRow(r.Context(), `
		SELECT u.id,u.cost,u.request_id,u.request_fingerprint,u.status,u.error_message,COALESCE(rr.encrypted_data,'')
		FROM usage u LEFT JOIN run_results rr ON rr.run_id=u.id AND rr.expires_at>now()
		WHERE u.user_id=$1 AND u.idempotency_key=$2`, userID, key).
		Scan(&runID, &cost, &originalRequestID, &originalFingerprint, &state, &message, &encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		agentError(w, 503, "RUN_LOOKUP_FAILED", "无法确认调用是否已执行，请查询调用记录", "unknown", 0, requestID)
		return true
	}
	if originalFingerprint != fingerprint {
		agentError(w, 409, "IDEMPOTENCY_CONFLICT", "同一个幂等键已经用于不同的调用参数", "not_started", 0, requestID)
		return true
	}
	w.Header().Set("X-Request-ID", originalRequestID)
	switch state {
	case "success":
		a.writeStoredRunResult(w, runID, cost, originalRequestID, encrypted)
	case "unknown":
		writeUnknownRun(w, runID, originalRequestID)
	case "failed":
		agentError(w, 502, "UPSTREAM_FAILED", upstreamFailureMessage(runID, message), "refunded", runID, originalRequestID)
	default:
		agentError(w, 409, "RUN_PENDING", "调用尚未完成，请查询调用记录", "unknown", runID, originalRequestID)
	}
	return true
}

func upstreamFailureMessage(runID int64, detail string) string {
	return fmt.Sprintf("上游调用失败，预留余额已退回。调用编号：%d。详情：%s", runID, detail)
}

func writeRunResult(w http.ResponseWriter, runID, cost int64, requestID string, result json.RawMessage) {
	writeJSON(w, 200, map[string]any{"id": runID, "cost_micros": cost, "data": result, "request_id": requestID, "currency": billing.Currency})
}

func (a *App) writeStoredRunResult(w http.ResponseWriter, runID, cost int64, requestID, encrypted string) {
	if encrypted == "" {
		agentError(w, 410, "RESULT_UNAVAILABLE", "调用已结算，结果未保存或已过期", "charged", runID, requestID)
		return
	}
	data, err := a.decrypt(encrypted)
	if err != nil || !json.Valid([]byte(data)) {
		log.Printf("read stored result: run_id=%d invalid encrypted data", runID)
		agentError(w, 503, "RESULT_READ_FAILED", "调用已结算，暂时无法读取结果", "charged", runID, requestID)
		return
	}
	writeRunResult(w, runID, cost, requestID, json.RawMessage(data))
}

func (a *App) getRunResult(w http.ResponseWriter, r *http.Request, u User) {
	runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || runID <= 0 {
		fail(w, 400, "调用编号无效")
		return
	}
	var cost int64
	var requestID, state, encrypted string
	err = a.db.QueryRow(r.Context(), `
		SELECT u.cost,u.request_id,u.status,COALESCE(rr.encrypted_data,'')
		FROM usage u LEFT JOIN run_results rr ON rr.run_id=u.id AND rr.expires_at>now()
		WHERE u.id=$1 AND u.user_id=$2`, runID, u.ID).Scan(&cost, &requestID, &state, &encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "调用记录不存在")
		return
	}
	if err != nil {
		fail(w, 503, "暂时无法读取调用结果")
		return
	}
	switch state {
	case "success":
		a.writeStoredRunResult(w, runID, cost, requestID, encrypted)
	case "unknown":
		writeUnknownRun(w, runID, requestID)
	case "failed":
		agentError(w, 409, "RUN_FAILED", "调用失败，预留余额已退回", "refunded", runID, requestID)
	default:
		agentError(w, 409, "RUN_PENDING", "调用尚未完成，请查询调用记录", "unknown", runID, requestID)
	}
}

func (a *App) purgeRunResults(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := a.db.Exec(ctx, `
		DELETE FROM run_results WHERE run_id IN (
			SELECT run_id FROM run_results WHERE expires_at<=now() ORDER BY expires_at LIMIT 500 FOR UPDATE SKIP LOCKED
		)`); err != nil {
		log.Printf("purge expired run results: %v", err)
	}
}
