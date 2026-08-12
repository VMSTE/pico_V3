package api

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/sipeed/picoclaw/pkg/config"
)

func setupPikaTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ddl := `CREATE TABLE request_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT NOT NULL DEFAULT (datetime('now')),
		component TEXT NOT NULL DEFAULT 'main',
		model TEXT NOT NULL DEFAULT '',
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		tool_calls_requested INTEGER NOT NULL DEFAULT 0,
		tool_calls_success INTEGER NOT NULL DEFAULT 0,
		tool_calls_failed INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		response_ms INTEGER NOT NULL DEFAULT 0,
		task_tag TEXT
	)`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("create table: %v", err)
	}

	rows := []struct {
		component string
		prompt    int64
		comp      int64
		err       string
		ms        int64
		tag       string
	}{
		{"main", 100, 50, "", 1000, "chat"},
		{"main", 200, 80, "timeout", 9000, "fix"},
		{"archivarius", 300, 20, "", 500, ""},
		{"reflexor", 400, 100, "", 3000, ""},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO request_log
			 (component, prompt_tokens, completion_tokens, error, response_ms, task_tag)
			 VALUES (?,?,?,?,?,?)`,
			r.component, r.prompt, r.comp, r.err, r.ms, r.tag,
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return db
}

func TestQueryPikaPeriodStats(t *testing.T) {
	db := setupPikaTestDB(t)
	s := queryPikaPeriodStats(context.Background(), db, "")
	if s.Requests != 4 {
		t.Errorf("Requests = %d, want 4", s.Requests)
	}
	if s.Tokens != 1250 {
		t.Errorf("Tokens = %d, want 1250", s.Tokens)
	}
	if s.Errors != 1 {
		t.Errorf("Errors = %d, want 1", s.Errors)
	}
	if s.ErrorPct != 25 {
		t.Errorf("ErrorPct = %v, want 25", s.ErrorPct)
	}
	// Пустая БД-результат по будущей дате — нули, без паники
	empty := queryPikaPeriodStats(context.Background(), db, "WHERE ts >= date('now','+1 day')")
	if empty.Requests != 0 || empty.ErrorPct != 0 {
		t.Errorf("empty period = %+v, want zeros", empty)
	}
}

func TestQueryPikaComponents(t *testing.T) {
	db := setupPikaTestDB(t)
	comps := queryPikaComponents(context.Background(), db)
	if len(comps) != 3 {
		t.Fatalf("components = %d, want 3", len(comps))
	}
	if comps[0].Component != "main" || comps[0].Requests != 2 {
		t.Errorf("top component = %+v, want main×2", comps[0])
	}
}

func TestQueryPikaP95(t *testing.T) {
	db := setupPikaTestDB(t)
	p95 := queryPikaP95(context.Background(), db)
	if p95 != 9000 {
		t.Errorf("p95 = %d, want 9000 (top of 4 rows)", p95)
	}
}

func TestResolvePikaDBPath(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agents.Defaults.MemoryDBPath = "/tmp/from-config.db"
	if got := resolvePikaDBPath(cfg); got != "/tmp/from-config.db" {
		t.Errorf("config path = %q", got)
	}
	t.Setenv("PIKA_DB_PATH", "/tmp/from-env.db")
	if got := resolvePikaDBPath(cfg); got != "/tmp/from-env.db" {
		t.Errorf("env override = %q, want /tmp/from-env.db", got)
	}
}
