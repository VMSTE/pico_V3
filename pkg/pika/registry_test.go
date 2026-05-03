package pika

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestAtomIDGenerator_Sequential(t *testing.T) {
	bm := setupTestDB(t)
	gen := NewAtomIDGenerator(bm)
	ctx := context.Background()

	ids := make([]string, 3)
	for i := range ids {
		id, err := gen.Next(ctx, "pattern")
		if err != nil {
			t.Fatalf("Next(%d): %v", i, err)
		}
		ids[i] = id
	}
	want := []string{"P-1", "P-2", "P-3"}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf(
				"ids[%d] = %q, want %q", i, ids[i], w,
			)
		}
	}
}

func TestAtomIDGenerator_MultiCategory(t *testing.T) {
	bm := setupTestDB(t)
	gen := NewAtomIDGenerator(bm)
	ctx := context.Background()

	p1, err := gen.Next(ctx, "pattern")
	if err != nil {
		t.Fatal(err)
	}
	d1, err := gen.Next(ctx, "decision")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := gen.Next(ctx, "pattern")
	if err != nil {
		t.Fatal(err)
	}

	if p1 != "P-1" {
		t.Errorf("p1 = %q, want P-1", p1)
	}
	if d1 != "D-1" {
		t.Errorf("d1 = %q, want D-1", d1)
	}
	if p2 != "P-2" {
		t.Errorf("p2 = %q, want P-2", p2)
	}
}

func TestAtomIDGenerator_RecoveryFromDB(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()

	// Insert P-1..P-5 directly via BotMemory
	for i := 1; i <= 5; i++ {
		err := bm.InsertAtom(ctx, KnowledgeAtomRow{
			AtomID:     fmt.Sprintf("P-%d", i),
			SessionID:  "s1",
			TurnID:     1,
			Category:   "pattern",
			Summary:    fmt.Sprintf("pattern %d", i),
			Confidence: 0.8,
			Polarity:   "positive",
		})
		if err != nil {
			t.Fatalf("InsertAtom P-%d: %v", i, err)
		}
	}

	// Create NEW generator — should recover from DB
	gen := NewAtomIDGenerator(bm)
	id, err := gen.Next(ctx, "pattern")
	if err != nil {
		t.Fatal(err)
	}
	if id != "P-6" {
		t.Errorf("id = %q, want P-6", id)
	}
}

func TestAtomIDGenerator_UnknownCategory(t *testing.T) {
	bm := setupTestDB(t)
	gen := NewAtomIDGenerator(bm)
	ctx := context.Background()

	_, err := gen.Next(ctx, "invalid")
	if err == nil {
		t.Error("expected error for unknown category")
	}
}

func TestHandleWrite_Created(t *testing.T) {
	bm := setupTestDB(t)
	h := NewRegistryHandler(bm)
	ctx := context.Background()

	created, err := h.HandleWrite(
		ctx, "runbook", "deploy_web", "deploy web app",
		json.RawMessage(`{"steps":["build","push"]}`),
		json.RawMessage(`["ops","deploy"]`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("expected created=true")
	}
}

func TestHandleWrite_Updated(t *testing.T) {
	bm := setupTestDB(t)
	h := NewRegistryHandler(bm)
	ctx := context.Background()

	_, err := h.HandleWrite(
		ctx, "runbook", "deploy_web", "v1", nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	created, err := h.HandleWrite(
		ctx, "runbook", "deploy_web", "v2", nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("expected created=false on update")
	}

	// Verify data updated
	r, err := h.HandleRead(
		ctx, "runbook", "deploy_web",
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.Summary != "v2" {
		t.Errorf("summary = %q, want v2", r.Summary)
	}
}

func TestHandleWrite_InvalidKind(t *testing.T) {
	bm := setupTestDB(t)
	h := NewRegistryHandler(bm)
	ctx := context.Background()

	_, err := h.HandleWrite(
		ctx, "invalid", "key1", "s", nil, nil,
	)
	if err == nil {
		t.Error("expected error for invalid kind")
	}
}

func TestHandleWrite_EmptyKey(t *testing.T) {
	bm := setupTestDB(t)
	h := NewRegistryHandler(bm)
	ctx := context.Background()

	_, err := h.HandleWrite(
		ctx, "runbook", "", "s", nil, nil,
	)
	if err == nil {
		t.Error("expected error for empty key")
	}
}

func TestHandleWrite_InvalidJSON(t *testing.T) {
	bm := setupTestDB(t)
	h := NewRegistryHandler(bm)
	ctx := context.Background()

	_, err := h.HandleWrite(
		ctx, "runbook", "k1", "s",
		json.RawMessage(`{broken`), nil,
	)
	if err == nil {
		t.Error("expected error for invalid JSON data")
	}
}

func TestHandleWrite_InvalidTags(t *testing.T) {
	bm := setupTestDB(t)
	h := NewRegistryHandler(bm)
	ctx := context.Background()

	_, err := h.HandleWrite(
		ctx, "runbook", "k1", "s",
		nil, json.RawMessage(`"not array"`),
	)
	if err == nil {
		t.Error("expected error for non-array tags")
	}
}

func TestHandleRead_NotFound(t *testing.T) {
	bm := setupTestDB(t)
	h := NewRegistryHandler(bm)
	ctx := context.Background()

	r, err := h.HandleRead(ctx, "runbook", "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if r != nil {
		t.Error("expected nil for not found")
	}
}

func TestHandleRead_UpdatesLastUsed(t *testing.T) {
	bm := setupTestDB(t)
	h := NewRegistryHandler(bm)
	ctx := context.Background()

	//nolint:errcheck
	h.HandleWrite(
		ctx, "runbook", "k1", "s", nil, nil,
	)

	// Read should update last_used
	r, err := h.HandleRead(ctx, "runbook", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil")
	}

	// Verify last_used is set by querying directly
	r2, err := bm.GetRegistry(ctx, "runbook", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if r2.LastUsed == nil {
		t.Error(
			"expected last_used to be set after read",
		)
	}
}

func TestHandleSearch(t *testing.T) {
	bm := setupTestDB(t)
	h := NewRegistryHandler(bm)
	ctx := context.Background()

	//nolint:errcheck
	h.HandleWrite(
		ctx, "runbook", "web_search", "ws", nil, nil,
	)
	//nolint:errcheck
	h.HandleWrite(
		ctx, "runbook", "web_browse", "wb", nil, nil,
	)
	//nolint:errcheck
	h.HandleWrite(
		ctx, "script", "gpt4", "g4", nil, nil,
	)

	results, err := h.HandleSearch(
		ctx, "runbook", "%",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf(
			"expected 2 results, got %d", len(results),
		)
	}
}
