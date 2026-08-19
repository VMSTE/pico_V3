package api

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// PIKA-V3 (D-AUDIT-108): дашборд v2 — разрез по агентам, медианы,
// legacy-фолбэк для БД без колонки agent_id (миграция v4).

func setupPikaV2TestDB(t *testing.T) *sql.DB {
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
		agent_id TEXT NOT NULL DEFAULT '',
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
		agentID   string
		prompt    int64
		comp      int64
		errStr    string
		ms        int64
		tag       string
	}{
		{"main", "main", 100, 50, "", 100, "chat"},
		{"main", "main", 200, 80, "timeout", 200, "fix"},
		{"main", "main", 300, 100, "", 300, "fix"},
		{"subturn", "researcher", 500, 200, "", 900, ""},
		{"subturn", "", 400, 150, "", 700, ""},
		{"archivarius", "", 300, 20, "", 500, ""},
	}
	for _, r := range rows {
		// PIKA-V3: прод пишет error через strOrNil — пустая строка = NULL.
		// Лента обязана переживать NULL (баг D-AUDIT-86: скан NULL в string
		// молча выкидывал строку из Recent requests).
		var errVal any
		if r.errStr != "" {
			errVal = r.errStr
		}
		if _, err := db.Exec(
			`INSERT INTO request_log
			 (component, agent_id, prompt_tokens, completion_tokens, error, response_ms, task_tag)
			 VALUES (?,?,?,?,?,?,?)`,
			r.component, r.agentID, r.prompt, r.comp, errVal, r.ms, r.tag,
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	return db
}

func TestPikaHasAgentIDColumn(t *testing.T) {
	db := setupPikaV2TestDB(t)
	if !pikaHasAgentIDColumn(context.Background(), db) {
		t.Error("v2 db: expected agent_id column detected")
	}

	legacy := setupPikaTestDB(t)
	if pikaHasAgentIDColumn(context.Background(), legacy) {
		t.Error("legacy db: must report no agent_id column")
	}
}

func TestQueryPikaAgents(t *testing.T) {
	db := setupPikaV2TestDB(t)
	agents := queryPikaAgents(context.Background(), db)
	if len(agents) != 3 {
		t.Fatalf("agents len = %d, want 3 (main, researcher, ephemeral)", len(agents))
	}
	// ORDER BY COUNT(*) DESC → main (3 строки) первым
	if agents[0].AgentID != "main" || agents[0].Requests != 3 {
		t.Errorf("agents[0] = %+v, want main x3", agents[0])
	}
	if agents[0].Tokens != 830 { // (100+50)+(200+80)+(300+100)
		t.Errorf("main tokens = %d, want 830", agents[0].Tokens)
	}
	if agents[0].Errors != 1 {
		t.Errorf("main errors = %d, want 1", agents[0].Errors)
	}
}

func TestQueryPikaMedians(t *testing.T) {
	db := setupPikaV2TestDB(t)
	ctx := context.Background()

	byComp := map[string]int64{}
	for _, m := range queryPikaMedians(ctx, db, "component") {
		byComp[m.Key] = m.MedianMs
	}
	// main: 100,200,300 → n=3, offset 1 → 200
	if byComp["main"] != 200 {
		t.Errorf("median(component=main) = %d, want 200", byComp["main"])
	}
	// subturn: 700,900 → n=2, offset 1 → 900 (upper median)
	if byComp["subturn"] != 900 {
		t.Errorf("median(component=subturn) = %d, want 900 (upper median)", byComp["subturn"])
	}

	byTag := map[string]int64{}
	for _, m := range queryPikaMedians(ctx, db, "task_tag") {
		byTag[m.Key] = m.MedianMs
	}
	// fix: 200,300 → n=2, offset 1 → 300
	if byTag["fix"] != 300 {
		t.Errorf("median(task_tag=fix) = %d, want 300", byTag["fix"])
	}
	if _, ok := byTag[""]; ok {
		t.Error("empty task_tag must be excluded from medians")
	}
}

func TestQueryPikaRequestsAgentID(t *testing.T) {
	// legacy-БД (без agent_id): лента работает, AgentID пустой
	legacy := setupPikaTestDB(t)
	rows := queryPikaRequests(context.Background(), legacy, 10)
	if len(rows) != 4 {
		t.Fatalf("legacy requests len = %d, want 4", len(rows))
	}
	for _, r := range rows {
		if r.AgentID != "" {
			t.Errorf("legacy row agent_id = %q, want empty", r.AgentID)
		}
	}

	// v2-БД: agent_id присутствует
	db := setupPikaV2TestDB(t)
	rows = queryPikaRequests(context.Background(), db, 10)
	if len(rows) != 6 {
		t.Fatalf("requests len = %d, want 6", len(rows))
	}
	found := false
	for _, r := range rows {
		if r.AgentID == "researcher" {
			found = true
		}
	}
	if !found {
		t.Error("expected researcher agent_id in feed")
	}
}
