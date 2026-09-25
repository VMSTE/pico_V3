package agent

// Волна 121 (срез Б): ключи событий из реестра серверов + дефолтные операции.

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/pika"
)

func TestDefaultEventOperation(t *testing.T) {
	cases := map[string]string{
		"search_memory":  "search",
		"search_logs":    "search",
		"registry_write": "write",
		"clarify":        "ask",
		"exec":           "call",
		"":               "call",
	}
	for tool, want := range cases {
		if got := defaultEventOperation(tool); got != want {
			t.Errorf("defaultEventOperation(%q) = %q, want %q", tool, got, want)
		}
	}
}

func TestAutoEventAdapter_KeyDerivation(t *testing.T) {
	db, err := pika.Migrate(":memory:")
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	bm, err := pika.NewBotMemory(db)
	if err != nil {
		t.Fatalf("NewBotMemory: %v", err)
	}
	tm, tg, classes := pika.BuildAutoEventConfig([]string{"notion"})
	h := pika.NewAutoEventHandler(bm, tm, tg, classes)
	a := &autoEventAdapter{
		handler: h,
		parseMCPTool: func(name string) (string, string, bool) {
			if name == "mcp_notion_fetch" {
				return "notion", "fetch", true
			}
			return "", "", false
		},
	}

	emit := func(tool string, blocked bool) {
		t.Helper()
		if err := a.OnEvent(context.Background(), Event{
			Kind:    EventKindToolExecEnd,
			Payload: ToolExecEndPayload{Tool: tool, Blocked: blocked},
			Meta:    EventMeta{SessionKey: "s1", TurnID: "1"},
		}); err != nil {
			t.Fatalf("OnEvent %s: %v", tool, err)
		}
	}

	emit("mcp_notion_fetch", false)
	emit("search_memory", false)
	emit("exec", false)

	count := func(eventType string) int {
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM events WHERE type = ?`, eventType,
		).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	if n := count("mcp_call"); n != 1 {
		t.Errorf("mcp_call = %d, want 1 (ключ mcp.notion.call из реестра)", n)
	}
	if n := count("memory_search"); n != 1 {
		t.Errorf("memory_search = %d, want 1 (brain-дефолт search)", n)
	}
	if n := count("tool_call"); n != 1 {
		t.Errorf("tool_call = %d, want 1 (generic для builtin)", n)
	}
}
