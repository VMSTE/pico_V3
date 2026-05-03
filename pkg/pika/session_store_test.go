// PIKA-V3: session_store_test.go — tests for PikaSessionStore

package pika

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func setupSessionStore(
	t *testing.T,
) *PikaSessionStore {
	t.Helper()
	bm := setupTestDB(t)
	return NewPikaSessionStore(bm)
}

func TestAddAndGetHistory(t *testing.T) {
	ss := setupSessionStore(t)

	// user message
	ss.AddFullMessage("s1", providers.Message{
		Role:    "user",
		Content: "hello",
	})

	// assistant with tool call
	ss.AddFullMessage("s1", providers.Message{
		Role:    "assistant",
		Content: "",
		ToolCalls: []providers.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: &providers.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"/tmp/x"}`,
				},
			},
		},
	})

	// tool result
	ss.AddFullMessage("s1", providers.Message{
		Role:       "tool",
		Content:    "file content here",
		ToolCallID: "call_1",
	})

	history := ss.GetHistory("s1")
	if len(history) != 3 {
		t.Fatalf(
			"expected 3 messages, got %d",
			len(history))
	}

	// Check user message
	if history[0].Role != "user" ||
		history[0].Content != "hello" {
		t.Errorf("msg[0] = %+v", history[0])
	}

	// Check assistant tool calls
	if len(history[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d",
			len(history[1].ToolCalls))
	}
	tc := history[1].ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf(
			"tool call ID = %q, want call_1", tc.ID)
	}
	if tc.Function == nil ||
		tc.Function.Name != "read_file" {
		t.Errorf(
			"tool call func = %+v", tc.Function)
	}

	// Check tool result
	if history[2].ToolCallID != "call_1" {
		t.Errorf("tool_call_id = %q, want call_1",
			history[2].ToolCallID)
	}
}

func TestTurnIDIncrement(t *testing.T) {
	ss := setupSessionStore(t)

	ss.AddFullMessage("s1", providers.Message{
		Role: "user", Content: "first",
	})
	ss.AddFullMessage("s1", providers.Message{
		Role: "assistant", Content: "reply1",
	})
	ss.AddFullMessage("s1", providers.Message{
		Role: "user", Content: "second",
	})

	// Verify turn IDs via BotMemory
	ctx := context.Background()
	rows, err := ss.mem.GetMessages(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf(
			"expected 3 rows, got %d", len(rows))
	}
	// user→1, assistant→1, user→2
	if rows[0].TurnID != 1 {
		t.Errorf(
			"row[0].TurnID=%d, want 1",
			rows[0].TurnID)
	}
	if rows[1].TurnID != 1 {
		t.Errorf(
			"row[1].TurnID=%d, want 1",
			rows[1].TurnID)
	}
	if rows[2].TurnID != 2 {
		t.Errorf(
			"row[2].TurnID=%d, want 2",
			rows[2].TurnID)
	}
}

func TestTurnIDRecovery(t *testing.T) {
	ss := setupSessionStore(t)
	ctx := context.Background()

	// Insert messages directly with turn_id=5
	_, err := ss.mem.SaveMessage(ctx, MessageRow{
		SessionID: "s1", TurnID: 5, Role: "user",
		Content: "old msg", Tokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create NEW PikaSessionStore (simulates restart)
	ss2 := NewPikaSessionStore(ss.mem)
	ss2.AddFullMessage("s1", providers.Message{
		Role: "user", Content: "new msg",
	})

	rows, err := ss.mem.GetMessages(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	last := rows[len(rows)-1]
	if last.TurnID != 6 {
		t.Errorf(
			"recovered turn_id=%d, want 6",
			last.TurnID)
	}
}

func TestEmptySession(t *testing.T) {
	ss := setupSessionStore(t)
	history := ss.GetHistory("nonexistent")
	if history == nil {
		t.Error(
			"GetHistory should return empty " +
				"slice, not nil")
	}
	if len(history) != 0 {
		t.Errorf(
			"expected 0 messages, got %d",
			len(history))
	}
}

func TestGetSetSummary(t *testing.T) {
	ss := setupSessionStore(t)
	if got := ss.GetSummary("s1"); got != "" {
		t.Errorf(
			"initial summary = %q, want empty", got)
	}
	ss.SetSummary("s1", "test summary")
	if got := ss.GetSummary("s1"); got != "test summary" {
		t.Errorf(
			"summary = %q, want 'test summary'",
			got)
	}
}

func TestSetHistory(t *testing.T) {
	ss := setupSessionStore(t)

	// Add initial messages
	ss.AddMessage("s1", "user", "old message")
	ss.AddMessage("s1", "assistant", "old reply")

	// Replace with new history
	newHistory := []providers.Message{
		{Role: "user", Content: "new1"},
		{Role: "assistant", Content: "new2"},
		{Role: "user", Content: "new3"},
	}
	ss.SetHistory("s1", newHistory)

	history := ss.GetHistory("s1")
	if len(history) != 3 {
		t.Fatalf(
			"expected 3 messages, got %d",
			len(history))
	}
	if history[0].Content != "new1" {
		t.Errorf(
			"msg[0] = %q, want new1",
			history[0].Content)
	}
	if history[2].Content != "new3" {
		t.Errorf(
			"msg[2] = %q, want new3",
			history[2].Content)
	}
}

func TestListSessions(t *testing.T) {
	ss := setupSessionStore(t)
	ss.AddMessage("sess-a", "user", "hi")
	ss.AddMessage("sess-b", "user", "hello")

	sessions := ss.ListSessions()
	if len(sessions) != 2 {
		t.Fatalf(
			"expected 2 sessions, got %d",
			len(sessions))
	}
	found := map[string]bool{}
	for _, sid := range sessions {
		found[sid] = true
	}
	if !found["sess-a"] || !found["sess-b"] {
		t.Errorf("sessions = %v", sessions)
	}
}

func TestTokenEstimation(t *testing.T) {
	ss := setupSessionStore(t)
	ss.AddFullMessage("s1", providers.Message{
		Role:    "user",
		Content: "hello world this is a test message",
	})

	ctx := context.Background()
	rows, err := ss.mem.GetMessages(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf(
			"expected 1 row, got %d", len(rows))
	}
	if rows[0].Tokens <= 0 {
		t.Errorf(
			"tokens = %d, want > 0", rows[0].Tokens)
	}
}
