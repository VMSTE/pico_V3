package pika

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"
)

func setupAutoEventTest(t *testing.T) (
	*AutoEventHandler, *sql.DB,
) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Migrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bm.Close() })

	toolTypeMap := map[string]string{
		"sandbox.run":       "tool_exec",
		"sandbox.run_fail":  "tool_fail",
		"health.check":      "health_ping",
		"health.check_fail": "health_ping_fail",
	}
	toolTagMap := map[string][]string{
		"sandbox.run": {
			"tool:sandbox", "op:run", "result:ok",
		},
		"sandbox.run_fail": {
			"tool:sandbox", "op:run", "result:fail",
		},
	}
	eventClasses := EventClasses{
		Critical: map[string]bool{
			"tool_fail":           true,
			"health_ping_fail":    true,
			"registry_write_fail": true,
		},
		Diagnostic: map[string]bool{
			"tool_exec":           true,
			"registry_write":      true,
			"memory_search":       true,
			"clarify_ask":         true,
			"clarify_ask_manager": true,
		},
		Heartbeat: map[string]bool{
			"health_ping": true,
		},
	}

	h := NewAutoEventHandler(
		bm, toolTypeMap, toolTagMap, eventClasses,
	)
	return h, db
}

func countEvents(
	t *testing.T, db *sql.DB, sid string,
) int {
	t.Helper()
	var c int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM events "+
			"WHERE session_id=?", sid,
	).Scan(&c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func getEventTypes(
	t *testing.T, db *sql.DB, sid string,
) []string {
	t.Helper()
	rows, err := db.Query(
		"SELECT type FROM events "+
			"WHERE session_id=? ORDER BY id ASC",
		sid,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	for rows.Next() {
		var tp string
		if err := rows.Scan(&tp); err != nil {
			t.Fatal(err)
		}
		types = append(types, tp)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return types
}

func getEventOutcome(
	t *testing.T, db *sql.DB, sid string,
) string {
	t.Helper()
	var outcome sql.NullString
	err := db.QueryRow(
		"SELECT outcome FROM events "+
			"WHERE session_id=? "+
			"ORDER BY id DESC LIMIT 1",
		sid,
	).Scan(&outcome)
	if err != nil {
		t.Fatal(err)
	}
	return outcome.String
}

func TestAutoEvent_WriteOp(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-write-op"

	err := h.HandleToolResult(
		ctx, "sandbox", "run", false, sid, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if c := countEvents(t, db, sid); c != 1 {
		t.Fatalf("expected 1 event, got %d", c)
	}
	types := getEventTypes(t, db, sid)
	if types[0] != "tool_exec" {
		t.Fatalf(
			"expected tool_exec, got %q", types[0],
		)
	}
}

func TestAutoEvent_ReadOpSkipped(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-read-skip"

	err := h.HandleToolResult(
		ctx, "compose", "status", false, sid, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if c := countEvents(t, db, sid); c != 0 {
		t.Fatalf("expected 0 events, got %d", c)
	}
}

func TestAutoEvent_FailSuffix(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-fail-suffix"

	err := h.HandleToolResult(
		ctx, "sandbox", "run", true, sid, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if c := countEvents(t, db, sid); c != 1 {
		t.Fatalf("expected 1 event, got %d", c)
	}
	types := getEventTypes(t, db, sid)
	if types[0] != "tool_fail" {
		t.Fatalf(
			"expected tool_fail, got %q", types[0],
		)
	}
	outcome := getEventOutcome(t, db, sid)
	if outcome != "fail" {
		t.Fatalf(
			"expected outcome fail, got %q", outcome,
		)
	}
}

func TestAutoEvent_ConsecutiveDedup(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-dedup"

	for i := 0; i < 4; i++ {
		_ = h.HandleToolResult(
			ctx, "sandbox", "run", false, sid, 1,
		)
	}
	c := countEvents(t, db, sid)
	if c != 3 {
		t.Fatalf(
			"expected 3 (4th dropped), got %d", c,
		)
	}
}

func TestAutoEvent_HeartbeatCounter(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-heartbeat"

	err := h.HandleToolResult(
		ctx, "health", "check", false, sid, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if c := countEvents(t, db, sid); c != 0 {
		t.Fatalf(
			"expected 0 (heartbeat), got %d", c,
		)
	}
	val, ok := h.heartbeatCtrs.Load("health_ping")
	if !ok {
		t.Fatal("heartbeat counter not found")
	}
	if atomic.LoadInt64(val.(*int64)) != 1 {
		t.Fatal("expected counter = 1")
	}
}

func TestAutoEvent_HeartbeatFlush(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-hb-flush"

	for i := 0; i < 5; i++ {
		_ = h.HandleToolResult(
			ctx, "health", "check", false, sid, 1,
		)
	}
	if c := countEvents(t, db, sid); c != 0 {
		t.Fatalf(
			"expected 0 before flush, got %d", c,
		)
	}

	err := h.FlushHeartbeats(ctx, sid, 2)
	if err != nil {
		t.Fatal(err)
	}
	if c := countEvents(t, db, sid); c != 1 {
		t.Fatalf(
			"expected 1 after flush, got %d", c,
		)
	}

	var summary string
	err = db.QueryRow(
		"SELECT summary FROM events "+
			"WHERE session_id=?", sid,
	).Scan(&summary)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "heartbeat: health_ping x5" {
		t.Fatalf(
			"unexpected summary: %q", summary,
		)
	}
}

func TestAutoEvent_HeartbeatFailEscalate(
	t *testing.T,
) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-hb-escalate"

	err := h.HandleToolResult(
		ctx, "health", "check", true, sid, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if c := countEvents(t, db, sid); c != 1 {
		t.Fatalf(
			"expected 1 (escalated), got %d", c,
		)
	}
	types := getEventTypes(t, db, sid)
	if types[0] != "health_ping_fail" {
		t.Fatalf(
			"expected health_ping_fail, got %q",
			types[0],
		)
	}
}

func TestAutoEvent_InvalidType(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-invalid"

	h.toolTypeMap["bad.tool"] = "nonexistent_type"

	err := h.HandleToolResult(
		ctx, "bad", "tool", false, sid, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if c := countEvents(t, db, sid); c != 0 {
		t.Fatalf(
			"expected 0 (invalid type), got %d", c,
		)
	}
}

func TestAutoEvent_ValidateStartup(t *testing.T) {
	h, _ := setupAutoEventTest(t)

	h.eventClasses.Critical["orphan_type"] = true

	warnings := h.ValidateStartup()
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "orphan_type") &&
			strings.Contains(w, "orphan") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf(
			"expected orphan warning, got: %v",
			warnings,
		)
	}
}

func TestAutoEvent_CoverageCheck(t *testing.T) {
	h, _ := setupAutoEventTest(t)

	h.SetRegisteredWriteOps([]string{
		"sandbox.run",
		"unknown.write_op",
	})

	warnings := h.ValidateStartup()
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "unknown.write_op") &&
			strings.Contains(w, "unmapped") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf(
			"expected unmapped warning, got: %v",
			warnings,
		)
	}
}

func TestAutoEvent_BrainTools(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-brain"

	err := h.HandleToolResult(
		ctx, "search_memory", "search",
		false, sid, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if c := countEvents(t, db, sid); c != 1 {
		t.Fatalf("expected 1 event, got %d", c)
	}
	types := getEventTypes(t, db, sid)
	if types[0] != "memory_search" {
		t.Fatalf(
			"expected memory_search, got %q",
			types[0],
		)
	}
}
