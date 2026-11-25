package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var runLatencyBuckets = [...]float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 90}

type runObservation struct {
	count   atomic.Uint64
	micros  atomic.Uint64
	buckets [len(runLatencyBuckets)]atomic.Uint64
}

type metricsState struct {
	tokenHash       [sha256.Size]byte
	enabled         bool
	runSuccess      runObservation
	runFailure      runObservation
	runUnknown      runObservation
	runNotSettled   runObservation
	orderCheckError atomic.Uint64
	refundError     atomic.Uint64
	billError       atomic.Uint64
}

func (m *metricsState) observeRun(state string, span time.Duration) {
	metric := &m.runUnknown
	switch state {
	case "success":
		metric = &m.runSuccess
	case "failed":
		metric = &m.runFailure
	case "settlement_error":
		metric = &m.runNotSettled
	}
	metric.micros.Add(uint64(span.Microseconds()))
	for i, upper := range runLatencyBuckets {
		if span.Seconds() <= upper {
			metric.buckets[i].Add(1)
		}
	}
	metric.count.Add(1)
}

func (m *metricsState) writeRunMetrics(out *strings.Builder) {
	out.WriteString("# TYPE tvendo_tool_runs_total counter\n# TYPE tvendo_tool_run_duration_seconds histogram\n")
	for _, metric := range []struct {
		name string
		item *runObservation
	}{
		{"success", &m.runSuccess}, {"failed", &m.runFailure}, {"unknown", &m.runUnknown}, {"settlement_error", &m.runNotSettled},
	} {
		count := metric.item.count.Load()
		fmt.Fprintf(out, "tvendo_tool_runs_total{outcome=%q} %d\n", metric.name, count)
		for i, upper := range runLatencyBuckets {
			fmt.Fprintf(out, "tvendo_tool_run_duration_seconds_bucket{outcome=%q,le=%q} %d\n", metric.name, strconv.FormatFloat(upper, 'f', -1, 64), metric.item.buckets[i].Load())
		}
		fmt.Fprintf(out, "tvendo_tool_run_duration_seconds_bucket{outcome=%q,le=\"+Inf\"} %d\n", metric.name, count)
		fmt.Fprintf(out, "tvendo_tool_run_duration_seconds_sum{outcome=%q} %f\n", metric.name, float64(metric.item.micros.Load())/1e6)
		fmt.Fprintf(out, "tvendo_tool_run_duration_seconds_count{outcome=%q} %d\n", metric.name, count)
	}
	out.WriteString("# TYPE tvendo_wechat_background_errors_total counter\n")
	fmt.Fprintf(out, "tvendo_wechat_background_errors_total{operation=\"order\"} %d\n", m.orderCheckError.Load())
	fmt.Fprintf(out, "tvendo_wechat_background_errors_total{operation=\"refund\"} %d\n", m.refundError.Load())
	fmt.Fprintf(out, "tvendo_wechat_background_errors_total{operation=\"bill\"} %d\n", m.billError.Load())
}

func (a *App) serveMetrics(w http.ResponseWriter, r *http.Request) {
	m := &a.monitoring
	if !m.enabled {
		http.NotFound(w, r)
		return
	}
	token, hasBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	presented := sha256.Sum256([]byte(token))
	if !hasBearer || subtle.ConstantTimeCompare(presented[:], m.tokenHash[:]) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var out strings.Builder
	m.writeRunMetrics(&out)
	a.writeBacklogMetrics(&out, r)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(out.String()))
}

func (a *App) writeBacklogMetrics(out *strings.Builder, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if a.db == nil {
		writeGauge(out, "tvendo_database_snapshot_up", 0)
		return
	}
	var unknown, staleRuns, staleOrders, staleRefunds, billDifferences, billMissing int64
	mchID, appID := "", ""
	if a.payments != nil {
		mchID, appID = a.payments.mchID, a.payments.appID
	}
	now := time.Now().In(billingDayZone)
	yesterday := now.AddDate(0, 0, -1).Format(time.DateOnly)
	err := a.db.QueryRow(ctx, `
		SELECT
		(SELECT count(*) FROM usage WHERE status='unknown'),
		(SELECT count(*) FROM usage WHERE status='running' AND created_at<now()-interval '5 minutes'),
		(SELECT count(*) FROM payment_orders WHERE status='pending' AND expires_at<now()-interval '15 minutes' AND mch_id=$1 AND appid=$2),
		(SELECT count(*) FROM payment_refunds r JOIN payment_orders p ON p.id=r.order_id
			WHERE r.status IN ('queued','processing','abnormal') AND p.mch_id=$1 AND p.appid=$2
			AND (r.status='abnormal' OR r.updated_at<now()-interval '30 minutes')),
		COALESCE((SELECT difference_count FROM payment_bill_reports WHERE merchant_id=$1 AND app_id=$2 AND bill_date=$3::text::date),0),
		CASE WHEN $1<>'' AND $4 AND NOT EXISTS (
			SELECT 1 FROM payment_bill_reports WHERE merchant_id=$1 AND app_id=$2 AND bill_date=$3::text::date
		) THEN 1 ELSE 0 END`,
		mchID, appID, yesterday, now.Hour() >= 12).Scan(&unknown, &staleRuns, &staleOrders, &staleRefunds, &billDifferences, &billMissing)
	if err != nil {
		log.Printf("metrics database snapshot unavailable: %v", err)
		writeGauge(out, "tvendo_database_snapshot_up", 0)
		return
	}
	writeGauge(out, "tvendo_database_snapshot_up", 1)
	writeGauge(out, "tvendo_tool_runs_unknown", unknown)
	writeGauge(out, "tvendo_tool_runs_stale", staleRuns)
	writeGauge(out, "tvendo_wechat_orders_stale", staleOrders)
	writeGauge(out, "tvendo_wechat_refunds_stale", staleRefunds)
	writeGauge(out, "tvendo_wechat_bill_differences", billDifferences)
	writeGauge(out, "tvendo_wechat_bill_missing", billMissing)
}

func writeGauge(out *strings.Builder, name string, number int64) {
	fmt.Fprintf(out, "# TYPE %s gauge\n%s %d\n", name, name, number)
}
