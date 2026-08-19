package pika

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/session"
)

func TestSessionMetadata_EnsureAndGetScope(t *testing.T) {
	store, bm := newTestSessionStore(t)
	key := "sk_v1_test123"
	scope := &session.SessionScope{
		Version:    session.ScopeVersionV1,
		AgentID:    "main",
		Channel:    "pico",
		Account:    "default",
		Dimensions: []string{"chat"},
		Values:     map[string]string{"chat": "direct:pico:uuid-42"},
	}
	store.EnsureSessionMetadata(key, scope, nil)

	r, err := bm.GetRegistry(context.Background(), "snapshot", chatScopeKey(key))
	if err != nil || r == nil {
		t.Fatalf("registry row missing: %v", err)
	}
	if r.Summary != "pico:uuid-42" {
		t.Fatalf("summary = %q, want pico:uuid-42", r.Summary)
	}

	got := store.GetSessionScope(key)
	if got == nil {
		t.Fatal("GetSessionScope returned nil")
	}
	if got.Channel != "pico" || got.Values["chat"] != "direct:pico:uuid-42" {
		t.Fatalf("scope = %+v", got)
	}
	if store.ResolveSessionKey(key) != key {
		t.Fatal("ResolveSessionKey must be identity")
	}
}

func TestSessionMetadata_EmptyNoop(t *testing.T) {
	store, _ := newTestSessionStore(t)
	store.EnsureSessionMetadata("", nil, nil) // must not panic or write
	if got := store.GetSessionScope("sk_v1_nope"); got != nil {
		t.Fatalf("want nil scope, got %+v", got)
	}
}
