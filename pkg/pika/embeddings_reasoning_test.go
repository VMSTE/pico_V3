package pika

// PIKA-V3: ТЗ-122 (срез М2.1) — тесты эмбеддинга мыслей.

import (
	"context"
	"testing"
)

// TestEmbedPendingReasoning_Idempotent — мысль эмбеддится как
// source='reasoning', повторный проход пуст (идемпотентность).
func TestEmbedPendingReasoning_Idempotent(t *testing.T) {
	bm := setupTestDB(t)
	defer bm.Close()
	ctx := context.Background()
	srv := mockEmbedderServer(t)
	defer srv.Close()
	emb := NewEmbedder(srv.URL, "test-key", "")

	if _, err := bm.InsertReasoningLog(ctx, ReasoningLogRow{
		ChatID:          "s1",
		ReasoningText:   "пользователь спросил про цвет — вспоминаю: любимый цвет синий",
		ReasoningTokens: 10,
		PikaSessionID:   "1",
	}); err != nil {
		t.Fatal(err)
	}
	n, err := bm.EmbedPendingReasoning(ctx, emb, 32)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("embedded %d, want 1 (reasoning)", n)
	}
	n2, err := bm.EmbedPendingReasoning(ctx, emb, 32)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("second pass = %d, want 0 (идемпотентность)", n2)
	}
	var src string
	if err := bm.db.QueryRowContext(ctx,
		`SELECT source FROM embeddings_meta WHERE model != 'skipped-empty'`,
	).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if src != "reasoning" {
		t.Fatalf("source = %q, want reasoning", src)
	}
}

// TestEmbedPendingReasoning_SkipsShort — короткая мысль помечается
// skipped-empty и не висит в pending вечно.
func TestEmbedPendingReasoning_SkipsShort(t *testing.T) {
	bm := setupTestDB(t)
	defer bm.Close()
	ctx := context.Background()
	srv := mockEmbedderServer(t)
	defer srv.Close()
	emb := NewEmbedder(srv.URL, "test-key", "")

	if _, err := bm.InsertReasoningLog(ctx, ReasoningLogRow{
		ChatID:          "s1",
		ReasoningText:   "ок",
		ReasoningTokens: 1,
		PikaSessionID:   "1",
	}); err != nil {
		t.Fatal(err)
	}
	n, err := bm.EmbedPendingReasoning(ctx, emb, 32)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("embedded %d, want 0 (короткий текст пропущен)", n)
	}
	var model string
	if err := bm.db.QueryRowContext(ctx,
		`SELECT model FROM embeddings_meta WHERE source='reasoning'`,
	).Scan(&model); err != nil {
		t.Fatal(err)
	}
	if model != "skipped-empty" {
		t.Fatalf("model = %q, want skipped-empty", model)
	}
}
