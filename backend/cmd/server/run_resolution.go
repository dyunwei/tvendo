package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func writeUnknownRun(w http.ResponseWriter, runID int64, requestID string) {
	agentError(w, 503, "UPSTREAM_OUTCOME_UNKNOWN", "上游执行结果待核实，余额仍处于预留状态，请勿重新执行", "unknown", runID, requestID)
}

func (a *App) resolveRun(w http.ResponseWriter, r *http.Request, actor User) {
	runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || runID <= 0 {
		fail(w, 400, "调用编号无效")
		return
	}
	var input struct {
		Outcome string `json:"outcome"`
		Note    string `json:"note"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Note = strings.TrimSpace(input.Note)
	if (input.Outcome != "success" && input.Outcome != "failed") || input.Note == "" || len(input.Note) > 500 {
		fail(w, 400, "请选择结算结果并填写核实依据（最多 500 字节）")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		fail(w, 503, "暂时无法结算")
		return
	}
	defer tx.Rollback(ctx)
	var userID int64
	err = tx.QueryRow(ctx, `SELECT u.id FROM usage run JOIN users u ON u.id=run.user_id WHERE run.id=$1 FOR UPDATE OF u`, runID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "调用记录不存在")
		return
	}
	if err != nil {
		fail(w, 503, "暂时无法读取调用")
		return
	}
	var cost int64
	err = tx.QueryRow(ctx, `
		UPDATE usage SET status=$2,resolution_note=$3,resolved_at=now()
		WHERE id=$1 AND status='unknown' RETURNING cost`, runID, input.Outcome, input.Note).Scan(&cost)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 409, "该调用当前无需人工结算")
		return
	}
	if err == nil {
		err = applyRunSettlement(ctx, tx, runID, userID, cost, input.Outcome)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO audit_log(actor_id,action,target_id) VALUES($1,$2,$3)`, actor.ID, "resolve_run_"+input.Outcome, runID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		log.Printf("resolve run %d: %v", runID, err)
		fail(w, 503, "结算结果暂无法确认，请刷新调用记录")
		return
	}
	writeJSON(w, 200, map[string]any{"id": runID, "status": input.Outcome})
}
