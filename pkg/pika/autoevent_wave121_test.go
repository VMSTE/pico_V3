package pika

// Волна 121 (срез Б): events пишутся для ВСЕХ тулов (D-AUDIT-121, events=0).

import (
	"context"
	"testing"
)

func newWave121Handler(t *testing.T) (*AutoEventHandler, func(string) int) {
	t.Helper()
	db, err := Migrate(":memory:")
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("NewBotMemory: %v", err)
	}
	tm, tg, classes := BuildAutoEventConfig(nil)
	h := NewAutoEventHandler(bm, tm, tg, classes)
	count := func(eventType string) int {
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM events WHERE type = ?`, eventType,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", eventType, err)
		}
		return n
	}
	return h, count
}

func TestAutoEvent_GenericToolCallWritten(t *testing.T) {
	h, count := newWave121Handler(t)
	if err := h.HandleToolResult(context.Background(), "exec", "call", false, "s1", "1"); err != nil {
		t.Fatalf("HandleToolResult: %v", err)
	}
	if n := count("tool_call"); n != 1 {
		t.Errorf("tool_call rows = %d, want 1 (generic fallback)", n)
	}
}

func TestAutoEvent_GenericFailWritten(t *testing.T) {
	h, count := newWave121Handler(t)
	if err := h.HandleToolResult(context.Background(), "web_search", "call", true, "s1", "1"); err != nil {
		t.Fatalf("HandleToolResult: %v", err)
	}
	if n := count("tool_call_fail"); n != 1 {
		t.Errorf("tool_call_fail rows = %d, want 1", n)
	}
}

func TestAutoEvent_BrainKeyStillWins(t *testing.T) {
	h, count := newWave121Handler(t)
	if err := h.HandleToolResult(context.Background(), "search_memory", "search", false, "s1", "1"); err != nil {
		t.Fatalf("HandleToolResult: %v", err)
	}
	if n := count("memory_search"); n != 1 {
		t.Errorf("memory_search rows = %d, want 1 (brain map, не generic)", n)
	}
	if n := count("tool_call"); n != 0 {
		t.Errorf("tool_call rows = %d, want 0 (brain key приоритетнее fallback)", n)
	}
}
