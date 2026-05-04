package pika

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"
)

// setupAutoEventTest creates an in-memory DB with schema,
// BotMemory, and AutoEventHandler configured for tests.
func setupAutoEventTest(t *testing.T) (
	*AutoEventHandler, *sql.DB,
) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bm.Close()
		db.Close()
	})

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
			"tool_fail":         true,
			"health_ping_fail":  true,
			"registry_write_fail": true,
		},
		Diagnostic: map[string]bool{
			"tool_exec":       true,
			"registry_write":  true,
			"memory_search":   true,
			"clarify_ask":     true,
			"clarify_ask_manager": true,
		},
		Heartbeat: map[string]bool{
			"health_ping": true,
		},
	}

	h := NewAutoEventHandler(bm, toolTypeMap, toolTagMap, eventClasses)
	return h, db
}

func countEvents(
	t *testing.T, db *sql.DB, sessionID string,
) int {
	t.Helper()
	var c int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM events WHERE session_id=?",
		sessionID,
	).Scan(&c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func getEventTypes(
	t *testing.T, db *sql.DB, sessionID string,
) []string {
	t.Helper()
	rows, err := db.Query(
		"SELECT type FROM events WHERE session_id=? "+
			"ORDER BY id ASC",
		sessionID,
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
	return types
}

func getEventOutcome(
	t *testing.T, db *sql.DB, sessionID string,
) string {
	t.Helper()
	var outcome sql.NullString
	err := db.QueryRow(
		"SELECT outcome FROM events WHERE session_id=? "+
			"ORDER BY id DESC LIMIT 1",
		sessionID,
	).Scan(&outcome)
	if err != nil {
		t.Fatal(err)
	}
	return outcome.String
}

// 1. TestAutoEvent_WriteOp — sandbox.run → INSERT type=tool_exec
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
		t.Fatalf("expected type tool_exec, got %q", types[0])
	}
}

// 2. TestAutoEvent_ReadOpSkipped — compose.status → no mapping
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

// 3. TestAutoEvent_FailSuffix — sandbox.run + isError → tool_fail
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
		t.Fatalf("expected type tool_fail, got %q", types[0])
	}
	outcome := getEventOutcome(t, db, sid)
	if outcome != "fail" {
		t.Fatalf("expected outcome fail, got %q", outcome)
	}
}

// 4. TestAutoEvent_ConsecutiveDedup — 4x same → 3 written, 4th dropped
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
		t.Fatalf("expected 3 events (4th dropped), got %d", c)
	}
}

// 5. TestAutoEvent_HeartbeatCounter — heartbeat type → 0 INSERTs
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
		t.Fatalf("expected 0 events (heartbeat), got %d", c)
	}

	// Verify counter incremented
	val, ok := h.heartbeatCtrs.Load("health_ping")
	if !ok {
		t.Fatal("heartbeat counter not found")
	}
	if atomic.LoadInt64(val.(*int64)) != 1 {
		t.Fatal("expected heartbeat counter = 1")
	}
}

// 6. TestAutoEvent_HeartbeatFlush — flush → 1 summary INSERT
func TestAutoEvent_HeartbeatFlush(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-hb-flush"

	// Accumulate 5 heartbeats
	for i := 0; i < 5; i++ {
		_ = h.HandleToolResult(
			ctx, "health", "check", false, sid, 1,
		)
	}

	// No events yet (all heartbeat)
	if c := countEvents(t, db, sid); c != 0 {
		t.Fatalf("expected 0 events before flush, got %d", c)
	}

	// Flush
	err := h.FlushHeartbeats(ctx, sid, 2)
	if err != nil {
		t.Fatal(err)
	}

	// 1 summary event
	if c := countEvents(t, db, sid); c != 1 {
		t.Fatalf("expected 1 event after flush, got %d", c)
	}

	// Check summary text
	var summary string
	err = db.QueryRow(
		"SELECT summary FROM events WHERE session_id=?",
		sid,
	).Scan(&summary)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "heartbeat: health_ping x5" {
		t.Fatalf("unexpected summary: %q", summary)
	}
}

// 7. TestAutoEvent_HeartbeatFailEscalate — heartbeat + isError → INSERT
func TestAutoEvent_HeartbeatFailEscalate(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-hb-escalate"

	// health.check with isError → should escalate, not increment
	err := h.HandleToolResult(
		ctx, "health", "check", true, sid, 1,
	)
	if err != nil {
		t.Fatal(err)
	}

	if c := countEvents(t, db, sid); c != 1 {
		t.Fatalf(
			"expected 1 event (escalated heartbeat), got %d", c,
		)
	}

	// Verify the event has the correct type
	types := getEventTypes(t, db, sid)
	// health.check_fail maps to health_ping_fail (critical)
	if types[0] != "health_ping_fail" {
		t.Fatalf(
			"expected type health_ping_fail, got %q", types[0],
		)
	}
}

// 8. TestAutoEvent_InvalidType — forced invalid type → drop
func TestAutoEvent_InvalidType(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-invalid"

	// Manually inject an invalid type into toolTypeMap
	h.toolTypeMap["bad.tool"] = "nonexistent_type"
	// Do NOT add to validTypes

	err := h.HandleToolResult(
		ctx, "bad", "tool", false, sid, 1,
	)
	if err != nil {
		t.Fatal(err)
	}

	if c := countEvents(t, db, sid); c != 0 {
		t.Fatalf("expected 0 events (invalid type), got %d", c)
	}
}

// 9. TestAutoEvent_ValidateStartup — orphan eventClass → warning
func TestAutoEvent_ValidateStartup(t *testing.T) {
	h, _ := setupAutoEventTest(t)

	// Add an orphan event class
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
			"expected warning about orphan_type, got: %v",
			warnings,
		)
	}
}

// 10. TestAutoEvent_CoverageCheck — tool without mapping → warning
func TestAutoEvent_CoverageCheck(t *testing.T) {
	h, _ := setupAutoEventTest(t)

	// Register a write-op that has no mapping
	h.SetRegisteredWriteOps([]string{
		"sandbox.run",       // mapped — ok
		"unknown.write_op",  // NOT mapped — warning
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
			"expected unmapped write-op warning, got: %v",
			warnings,
		)
	}
}

// 11. TestAutoEvent_BrainTools — search_memory.search → memory_search
func TestAutoEvent_BrainTools(t *testing.T) {
	h, db := setupAutoEventTest(t)
	ctx := context.Background()
	sid := "test-brain"

	err := h.HandleToolResult(
		ctx, "search_memory", "search", false, sid, 1,
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
			"expected type memory_search, got %q", types[0],
		)
	}
}
