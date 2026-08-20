package pika

import (
	"context"
	"strings"
	"testing"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// D-AUDIT-104: per-chat memory scope + chat_id scoping + multiword AND.

func TestMemoryScope_DefaultAndSet(t *testing.T) {
	bm, _, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	if got := bm.GetMemoryScope(ctx, "chat-a"); got != "session" {
		t.Fatalf("default scope = %q, want session", got)
	}
	if err := bm.SetMemoryScope(ctx, "chat-a", "all"); err != nil {
		t.Fatalf("SetMemoryScope: %v", err)
	}
	if got := bm.GetMemoryScope(ctx, "chat-a"); got != "all" {
		t.Fatalf("scope = %q, want all", got)
	}
	if got := bm.GetMemoryScope(ctx, "chat-b"); got != "session" {
		t.Fatalf("other chat scope = %q, want session", got)
	}
	if err := bm.SetMemoryScope(ctx, "chat-a", "bogus"); err == nil {
		t.Fatal("expected error for invalid scope")
	}
}

func TestSearchMessages_ScopeAndMultiword(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "chat-a", PikaSessionID: "t1", Role: "user",
		Content: "мой любимый цвет — синий", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "chat-b", PikaSessionID: "t1", Role: "user",
		Content: "погода завтра дождь", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := ms.searchMessages(ctx, "цвет", 10, "chat-b", "session")
	if err != nil || len(res) != 0 {
		t.Fatalf("session scope from chat-b: got %d, err %v; want 0", len(res), err)
	}
	res, err = ms.searchMessages(ctx, "цвет", 10, "chat-a", "session")
	if err != nil || len(res) != 1 {
		t.Fatalf("session scope from chat-a: got %d, err %v; want 1", len(res), err)
	}
	res, err = ms.searchMessages(ctx, "цвет", 10, "chat-b", "all")
	if err != nil || len(res) != 1 {
		t.Fatalf("all scope from chat-b: got %d, err %v; want 1", len(res), err)
	}
	res, _ = ms.searchMessages(ctx, "любимый цвет", 10, "chat-a", "all")
	if len(res) != 1 {
		t.Fatalf("multiword full match: got %d, want 1", len(res))
	}
	res, _ = ms.searchMessages(ctx, "цвет погода", 10, "chat-a", "all")
	if len(res) != 2 {
		t.Fatalf("multiword OR partial: got %d, want 2", len(res))
	}
	res, _ = ms.searchMessages(
		ctx, "любимый цвет пользователя Gar", 10, "chat-b", "all",
	)
	if len(res) != 1 {
		t.Fatalf("verbose LLM query: got %d, want 1", len(res))
	}
}

// Execute берёт canonical session key из tool ctx (ToolSessionKey) —
// в бою SessionIDKey никем не наполнялся (D-AUDIT-104, 19 авг).
func TestSearchMemory_UsesToolSessionKey(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "sk_v1_a", PikaSessionID: "t1", Role: "user",
		Content: "мой любимый цвет — синий", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	// другой чат разрешил всю базу
	if err := bm.SetMemoryScope(ctx, "sk_v1_b", "all"); err != nil {
		t.Fatal(err)
	}

	toolCtx := toolshared.WithToolSessionContext(
		context.Background(), "main", "sk_v1_b", nil,
	)
	res := ms.Execute(toolCtx, map[string]any{"query": "цвет", "limit": 10})
	if res == nil || res.IsError {
		t.Fatalf("Execute: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "синий") {
		t.Fatalf(
			"scope=all via ToolSessionKey must find cross-chat fact, got: %s",
			res.ForLLM,
		)
	}

	// чат без флага (дефолт session) чужого не видит
	toolCtx2 := toolshared.WithToolSessionContext(
		context.Background(), "main", "sk_v1_c", nil,
	)
	res2 := ms.Execute(toolCtx2, map[string]any{"query": "цвет", "limit": 10})
	if res2 != nil && strings.Contains(res2.ForLLM, "синий") {
		t.Fatalf("default session scope leaked cross-chat: %s", res2.ForLLM)
	}
}

// Волна 86: три идентичных сообщения схлопываются в один результат.
func TestSearchMessages_DedupIdentical(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := bm.SaveMessage(ctx, MessageRow{
			ChatID: "s1", PikaSessionID: "1", Role: "user",
			Content: "какой мой любимый цвет?", Tokens: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := ms.searchMessages(ctx, "цвет", 10, "s1", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("dedup: got %d results, want 1", len(res))
	}
}
