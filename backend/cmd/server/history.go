package main

import (
	"encoding/csv"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) usageHistory(w http.ResponseWriter, r *http.Request, u User) {
	a.history(w, r, u, true)
}

func (a *App) transactionHistory(w http.ResponseWriter, r *http.Request, u User) {
	a.history(w, r, u, false)
}

func (a *App) history(w http.ResponseWriter, r *http.Request, u User, usage bool) {
	q := r.URL.Query()
	format := q.Get("format")
	if format != "" && format != "csv" {
		fail(w, 400, "不支持的导出格式")
		return
	}
	legacy := format == "" && !q.Has("limit") && !q.Has("cursor") && !q.Has("status") && !q.Has("kind") && !q.Has("from") && !q.Has("before")
	var from, before *time.Time
	for parameter, destination := range map[string]**time.Time{"from": &from, "before": &before} {
		if raw := q.Get(parameter); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				fail(w, 400, "日期需为包含时区的 RFC 3339 时间")
				return
			}
			*destination = &value
		}
	}
	if from != nil && before != nil && !from.Before(*before) {
		fail(w, 400, "开始日期必须早于结束边界")
		return
	}
	pageSize := 200
	if !legacy && format == "" {
		var err error
		pageSize, err = pageLimit(r, 25)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	var cursor *int64
	if raw := q.Get("cursor"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 || format == "csv" {
			fail(w, 400, "分页游标无效")
			return
		}
		cursor = &value
	}
	columns := []string{"id", "created_at", "kind", "amount", "note"}
	table, filterColumn, filename := "transactions", "kind", "tvendo-transactions.csv"
	filter := q.Get("kind")
	if usage {
		columns = []string{"id", "created_at", "tool_name", "endpoint", "key_label", "status", "cost", "http_status", "duration_ms", "request_id", "error_code", "error_message", "resolved_at", "resolution_note"}
		table, filterColumn, filename = "usage", "status", "tvendo-usage.csv"
		filter = q.Get("status")
	}
	if filter != "" && (usage && !strings.Contains("|running|unknown|success|failed|", "|"+filter+"|") ||
		!usage && !strings.Contains("|topup|usage|wechat_refund|", "|"+filter+"|") || strings.Contains(filter, "|")) {
		fail(w, 400, "筛选条件无效")
		return
	}

	selected := "entry." + strings.Join(columns, ",entry.")
	scope := "entry.user_id=$1"
	if usage {
		selected += ",entry.user_id,entry.key_id"
		if u.Role == "admin" {
			scope = "$1::bigint>0"
			table += " entry JOIN users ON users.id=entry.user_id"
			selected += ",users.name AS user_name"
			columns = append(columns, "user_name")
		} else {
			table += " entry"
		}
	} else {
		table += " entry"
	}
	query := `SELECT entry.id,to_jsonb(entry) FROM (
		SELECT ` + selected + ` FROM ` + table + ` WHERE ` + scope + `
		AND ($2::bigint IS NULL OR entry.id<$2)
		AND ($3::timestamptz IS NULL OR entry.created_at >= $3)
		AND ($4::timestamptz IS NULL OR entry.created_at < $4)
		AND ($5='' OR entry.` + filterColumn + `=$5) ORDER BY entry.id DESC LIMIT $6
	) entry ORDER BY entry.id DESC`
	var limit *int
	if format == "" {
		count := pageSize
		if !legacy {
			count++
		}
		limit = &count
	}
	rows, err := a.db.Query(r.Context(), query, u.ID, cursor, from, before, filter, limit)
	if err != nil {
		fail(w, 500, "无法读取记录")
		return
	}
	defer rows.Close()

	items := []json.RawMessage{}
	var next string
	var csvWriter *csv.Writer
	for rows.Next() {
		var id int64
		var raw json.RawMessage
		if err := rows.Scan(&id, &raw); err != nil {
			log.Printf("read history row: %v", err)
			if csvWriter == nil {
				fail(w, 500, "无法读取记录")
			}
			return
		}
		if format == "csv" {
			if csvWriter == nil {
				csvWriter = startHistoryCSV(w, filename, columns)
			}
			record, err := historyCSVRecord(raw, columns)
			if err != nil || csvWriter.Write(record) != nil {
				return
			}
			continue
		}
		if len(items) == pageSize {
			writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
			return
		}
		items = append(items, raw)
		next = strconv.FormatInt(id, 10)
	}
	if err := rows.Err(); err != nil {
		log.Printf("read history: %v", err)
		if csvWriter == nil {
			fail(w, 500, "无法读取记录")
		}
		return
	}
	switch {
	case format == "csv":
		if csvWriter == nil {
			csvWriter = startHistoryCSV(w, filename, columns)
		}
		csvWriter.Flush()
	case legacy:
		writeJSON(w, 200, items)
	default:
		writeJSON(w, 200, map[string]any{"items": items, "next_cursor": ""})
	}
}

func startHistoryCSV(w http.ResponseWriter, filename string, columns []string) *csv.Writer {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write([]byte("\xef\xbb\xbf"))
	output := csv.NewWriter(w)
	_ = output.Write(columns)
	return output
}

func historyCSVRecord(raw json.RawMessage, columns []string) ([]string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	record := make([]string, len(columns))
	for i, column := range columns {
		raw := fields[column]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		if raw[0] != '"' {
			record[i] = string(raw)
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(value)
		if (trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0]))) ||
			strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") {
			value = "'" + value // Prevent spreadsheet formulas in user controlled fields.
		}
		record[i] = value
	}
	return record, nil
}
