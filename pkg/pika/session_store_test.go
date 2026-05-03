// PIKA-V3: Tests for PikaSessionStore.

package pika

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func newTestSessionStore(t *testing.T) (
	*PikaSessionStore, *BotMemory,
) {
	t.Helper()
	db, err := Migrate(":memory:")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new bot memory: %v", err)
	}
	t.Cleanup(func() { bm.Close() })
	return NewPikaSessionStore(bm), bm
}

func TestAddAndGetHistory(t *testing.T) {
	store, _ := newTestSessionStore(t)
	key := "test-session-1"

	// 1. user message
	store.AddFullMessage(key, providers.Message{
		Role:    "user",
		Content: "hello",
	})

	// 2. assistant with tool calls
	store.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: "calling tool",
		ToolCalls: []providers.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: &providers.FunctionCall{
					Name:      "web_search",
					Arguments: `{"q":"test"}`,
				},
			},
		},
	})

	// 3. tool result
	store.AddFullMessage(key, providers.Message{
		Role:       "tool",
		Content:    `{"ok":true}`,
		ToolCallID: "call_1",
	})

	history := store.GetHistory(key)
	if len(history) != 3 {
		t.Fatalf(
			"expected 3 messages, got %d", len(history),
		)
	}

	// Check user message
	if history[0].Role != "user" ||
		history[0].Content != "hello" {
		t.Errorf("msg[0] = %+v", history[0])
	}

	// Check assistant tool calls preserved
	if len(history[1].ToolCalls) != 1 {
		t.Fatalf(
			"expected 1 tool call, got %d",
			len(history[1].ToolCalls),
		)
	}
	tc := history[1].ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf(
			"tool call ID = %q, want call_1", tc.ID,
		)
	}
	if tc.Function == nil ||
		tc.Function.Name != "web_search" {
		t.Errorf("tool call function = %+v", tc.Function)
	}

	// Check tool result tool_call_id preserved
	if history[2].ToolCallID != "call_1" {
		t.Errorf(
			"tool_call_id = %q, want call_1",
			history[2].ToolCallID,
		)
	}
}

func TestAttachmentsRoundTrip(t *testing.T) {
	store, _ := newTestSessionStore(t)
	key := "test-attach"

	msg := providers.Message{
		Role:    "user",
		Content: "see attached",
		Attachments: []providers.Attachment{
			{
				Type:     "file",
				Filename: "doc.pdf",
				URL:      "https://example.com/doc.pdf",
			},
		},
	}
	store.AddFullMessage(key, msg)

	history := store.GetHistory(key)
	if len(history) != 1 {
		t.Fatalf(
			"expected 1 message, got %d", len(history),
		)
	}
	if len(history[0].Attachments) != 1 {
		t.Fatalf(
			"expected 1 attachment, got %d",
			len(history[0].Attachments),
		)
	}
	att := history[0].Attachments[0]
	if att.Filename != "doc.pdf" {
		t.Errorf(
			"filename = %q, want doc.pdf", att.Filename,
		)
	}
	if att.URL != "https://example.com/doc.pdf" {
		t.Errorf("url = %q", att.URL)
	}
}

func TestTurnIDIncrement(t *testing.T) {
	store, bm := newTestSessionStore(t)
	key := "test-turns"

	// user -> turn_id=1
	store.AddMessage(key, "user", "msg1")
	// assistant -> turn_id=1 (same turn)
	store.AddMessage(key, "assistant", "resp1")
	// user -> turn_id=2
	store.AddMessage(key, "user", "msg2")

	rows, err := bm.GetMessages(
		context.Background(), key,
	)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].TurnID != 1 {
		t.Errorf(
			"row[0] turn_id = %d, want 1",
			rows[0].TurnID,
		)
	}
	if rows[1].TurnID != 1 {
		t.Errorf(
			"row[1] turn_id = %d, want 1",
			rows[1].TurnID,
		)
	}
	if rows[2].TurnID != 2 {
		t.Errorf(
			"row[2] turn_id = %d, want 2",
			rows[2].TurnID,
		)
	}
}

func TestTurnIDRecovery(t *testing.T) {
	_, bm := newTestSessionStore(t)
	key := "test-recover"

	// Insert directly via BotMemory with turn_id=5
	_, err := bm.SaveMessage(
		context.Background(),
		MessageRow{
			SessionID: key,
			TurnID:    5,
			Role:      "user",
			Content:   "old msg",
			Tokens:    10,
		},
	)
	if err != nil {
		t.Fatalf("save message: %v", err)
	}

	// Create a NEW store (simulates process restart)
	store2 := NewPikaSessionStore(bm)

	// user msg -> should recover turn_id=5, then +1 = 6
	store2.AddMessage(key, "user", "new msg")

	rows, err := bm.GetMessages(
		context.Background(), key,
	)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[1].TurnID != 6 {
		t.Errorf(
			"recovered turn_id = %d, want 6",
			rows[1].TurnID,
		)
	}
}

func TestEmptySession(t *testing.T) {
	store, _ := newTestSessionStore(t)
	history := store.GetHistory("nonexistent")
	if history == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(history) != 0 {
		t.Errorf(
			"expected 0 messages, got %d", len(history),
		)
	}
}

func TestGetSetSummary(t *testing.T) {
	store, _ := newTestSessionStore(t)
	key := "test-summary"

	if s := store.GetSummary(key); s != "" {
		t.Errorf("expected empty summary, got %q", s)
	}
	store.SetSummary(key, "test summary")
	if s := store.GetSummary(key); s != "test summary" {
		t.Errorf(
			"expected 'test summary', got %q", s,
		)
	}
}

func TestSetHistory(t *testing.T) {
	store, _ := newTestSessionStore(t)
	key := "test-set-history"

	msgs := []providers.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
		{Role: "user", Content: "three"},
	}
	store.SetHistory(key, msgs)

	history := store.GetHistory(key)
	if len(history) != 3 {
		t.Fatalf(
			"expected 3 messages, got %d", len(history),
		)
	}
	for i, want := range msgs {
		if history[i].Role != want.Role {
			t.Errorf(
				"msg[%d].Role = %q, want %q",
				i, history[i].Role, want.Role,
			)
		}
		if history[i].Content != want.Content {
			t.Errorf(
				"msg[%d].Content = %q, want %q",
				i, history[i].Content, want.Content,
			)
		}
	}
}

func TestListSessions(t *testing.T) {
	store, _ := newTestSessionStore(t)

	store.AddMessage("session-a", "user", "hello a")
	store.AddMessage("session-b", "user", "hello b")

	sessions := store.ListSessions()
	if len(sessions) != 2 {
		t.Fatalf(
			"expected 2 sessions, got %d: %v",
			len(sessions), sessions,
		)
	}
	found := map[string]bool{}
	for _, s := range sessions {
		found[s] = true
	}
	if !found["session-a"] || !found["session-b"] {
		t.Errorf("missing sessions: got %v", sessions)
	}
}
