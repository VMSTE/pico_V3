package pika

import (
	"context"
	"testing"
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
