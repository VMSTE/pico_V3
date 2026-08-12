package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sipeed/picoclaw/pkg/config"
)

// D-AUDIT-86: read-only Pika telemetry dashboard API.
// The launcher is a separate process from the gateway, so it reads
// bot_memory.db directly. The DB is always opened read-only (mode=ro).

type pikaPeriodStats struct {
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens"`
	Errors   int64   `json:"errors"`
	ErrorPct float64 `json:"error_pct"`
}

type pikaComponentStats struct {
	Component string  `json:"component"`
	Requests  int64   `json:"requests"`
	Tokens    int64   `json:"tokens"`
	Errors    int64   `json:"errors"`
	AvgMs     float64 `json:"avg_ms"`
}

type pikaOverview struct {
	Available  bool                 `json:"available"`
	DBPath     string               `json:"db_path,omitempty"`
	Note       string               `json:"note,omitempty"`
	Today      pikaPeriodStats      `json:"today"`
	Totals     pikaPeriodStats      `json:"totals"`
	P95Ms      int64                `json:"p95_ms"`
	Components []pikaComponentStats `json:"components"`
}

type pikaRequestRow struct {
	TS                 string `json:"ts"`
	Component          string `json:"component"`
	Model              string `json:"model"`
	TaskTag            string `json:"task_tag"`
	PromptTokens       int64  `json:"prompt_tokens"`
	CompletionTokens   int64  `json:"completion_tokens"`
	ResponseMs         int64  `json:"response_ms"`
	Error              string `json:"error"`
	ToolCallsRequested int64  `json:"tool_calls_requested"`
	ToolCallsSuccess   int64  `json:"tool_calls_success"`
	ToolCallsFailed    int64  `json:"tool_calls_failed"`
}

func (h *Handler) registerPikaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pika/overview", h.handlePikaOverview)
	mux.HandleFunc("GET /api/pika/requests", h.handlePikaRequests)
}

// resolvePikaDBPath mirrors the gateway resolution order:
// PIKA_DB_PATH env > agents.defaults.memory_db_path > legacy workspace path.
func resolvePikaDBPath(cfg *config.Config) string {
	if p := strings.TrimSpace(os.Getenv("PIKA_DB_PATH")); p != "" {
		return expandHomePath(p)
	}
	if p := strings.TrimSpace(cfg.Agents.Defaults.MemoryDBPath); p != "" {
		return expandHomePath(p)
	}
	return filepath.Join(cfg.WorkspacePath(), "sessions", "bot_memory.db")
}

func expandHomePath(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func openPikaDBRO(path string) (*sql.DB, error) {
	return sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", path))
}

// handlePikaOverview returns aggregated telemetry stats.
//
//	GET /api/pika/overview
func (h *Handler) handlePikaOverview(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	ov := pikaOverview{DBPath: resolvePikaDBPath(cfg)}
	if _, stErr := os.Stat(ov.DBPath); stErr != nil {
		ov.Note = "bot_memory.db not found — gateway has not written telemetry yet"
		writePikaJSON(w, ov)
		return
	}

	db, err := openPikaDBRO(ov.DBPath)
	if err != nil {
		ov.Note = fmt.Sprintf("open db: %v", err)
		writePikaJSON(w, ov)
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ov.Available = true
	ov.Today = queryPikaPeriodStats(ctx, db, "WHERE ts >= date('now')")
	ov.Totals = queryPikaPeriodStats(ctx, db, "")
	ov.P95Ms = queryPikaP95(ctx, db)
	ov.Components = queryPikaComponents(ctx, db)
	writePikaJSON(w, ov)
}

// handlePikaRequests returns the most recent request_log rows.
//
//	GET /api/pika/requests?limit=50
func (h *Handler) handlePikaRequests(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, aErr := strconv.Atoi(v); aErr == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	dbPath := resolvePikaDBPath(cfg)
	if _, stErr := os.Stat(dbPath); stErr != nil {
		writePikaJSON(w, map[string]any{
			"available": false,
			"requests":  []pikaRequestRow{},
		})
		return
	}

	db, err := openPikaDBRO(dbPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open db: %v", err), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	writePikaJSON(w, map[string]any{
		"available": true,
		"requests":  queryPikaRequests(ctx, db, limit),
	})
}

func writePikaJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func queryPikaPeriodStats(ctx context.Context, db *sql.DB, where string) pikaPeriodStats {
	var s pikaPeriodStats
	q := "SELECT COUNT(*), COALESCE(SUM(prompt_tokens+completion_tokens),0), " +
		"COALESCE(SUM(CASE WHEN error != '' THEN 1 ELSE 0 END),0) " +
		"FROM request_log " + where
	if err := db.QueryRowContext(ctx, q).Scan(&s.Requests, &s.Tokens, &s.Errors); err != nil {
		return s
	}
	if s.Requests > 0 {
		s.ErrorPct = float64(s.Errors) / float64(s.Requests) * 100
	}
	return s
}

func queryPikaP95(ctx context.Context, db *sql.DB) int64 {
	var ms int64
	err := db.QueryRowContext(ctx,
		`SELECT response_ms FROM request_log WHERE response_ms > 0
		 ORDER BY response_ms
		 LIMIT 1 OFFSET (
		   SELECT CAST(COUNT(*) * 0.95 AS INTEGER) FROM request_log WHERE response_ms > 0
		 )`,
	).Scan(&ms)
	if err != nil {
		return 0
	}
	return ms
}

func queryPikaComponents(ctx context.Context, db *sql.DB) []pikaComponentStats {
	rows, err := db.QueryContext(ctx,
		`SELECT component, COUNT(*),
		        COALESCE(SUM(prompt_tokens+completion_tokens),0),
		        COALESCE(SUM(CASE WHEN error != '' THEN 1 ELSE 0 END),0),
		        COALESCE(AVG(NULLIF(response_ms,0)),0)
		 FROM request_log
		 GROUP BY component
		 ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []pikaComponentStats
	for rows.Next() {
		var c pikaComponentStats
		if sErr := rows.Scan(&c.Component, &c.Requests, &c.Tokens, &c.Errors, &c.AvgMs); sErr != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

func queryPikaRequests(ctx context.Context, db *sql.DB, limit int) []pikaRequestRow {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, component, model, COALESCE(task_tag,''),
		        prompt_tokens, completion_tokens, response_ms, error,
		        tool_calls_requested, tool_calls_success, tool_calls_failed
		 FROM request_log
		 ORDER BY id DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := make([]pikaRequestRow, 0, limit)
	for rows.Next() {
		var r pikaRequestRow
		if sErr := rows.Scan(
			&r.TS, &r.Component, &r.Model, &r.TaskTag,
			&r.PromptTokens, &r.CompletionTokens, &r.ResponseMs, &r.Error,
			&r.ToolCallsRequested, &r.ToolCallsSuccess, &r.ToolCallsFailed,
		); sErr != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}
