package pika

// ТЗ-122 (этап 2, срез М1): векторный фундамент. Мок-эмбеддер (httptest)
// отдаёт детерминированные 1024-dim векторы: «кошка» в тексте → e1,
// прочее → e2. Проверяем: миграция v10 (vec0 живой на modernc),
// EmbedPending (hot + атом, идемпотентность), перепрописка при архивации
// без пересчёта, KNN-связность embeddings_vec.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mockEmbedderServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var sb strings.Builder
		sb.WriteString(`{"data":[`)
		for i, text := range req.Input {
			if i > 0 {
				sb.WriteByte(',')
			}
			first, second := 0.0, 1.0
			if strings.Contains(text, "кошка") {
				first, second = 1.0, 0.0
			}
			zeros := strings.Repeat(",0", 1022)
			fmt.Fprintf(&sb, `{"index":%d,"embedding":[%g,%g%s]}`, i, first, second, zeros)
		}
		sb.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sb.String()))
	}))
}

func TestMigrateV10_VectorTables(t *testing.T) {
	bm, _, cleanup := setupSearchTest(t)
	defer cleanup()
	var n int
	if err := bm.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE name IN ('embeddings_meta','embeddings_vec')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("vector tables = %d, want 2 (vec0 must register)", n)
	}
}

func TestEmbedPending_HotAndAtom_Idempotent(t *testing.T) {
	bm, _, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	srv := mockEmbedderServer(t)
	defer srv.Close()
	emb := NewEmbedder(srv.URL, "test-key", "")

	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "кошка сидит на окне", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "S-1", ChatID: "s1", PikaSessionID: "1",
		Category: "summary", Summary: "собака гуляет по двору",
		Confidence: 0.9, Polarity: "neutral",
	}); err != nil {
		t.Fatal(err)
	}

	n, err := bm.EmbedPending(ctx, emb, 32)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("embedded %d, want 2 (hot msg + atom)", n)
	}
	n2, err := bm.EmbedPending(ctx, emb, 32)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("second pass = %d, want 0 (идемпотентность)", n2)
	}

	// KNN: вектор e1 («кошачий» запрос) обязан быть ближе к hot-сообщению.
	ones := "[1" + strings.Repeat(",0", 1023) + "]"
	rows, err := bm.db.QueryContext(ctx,
		`SELECT rowid FROM embeddings_vec WHERE embedding MATCH ? AND k = 2`, ones)
	if err != nil {
		t.Fatalf("knn: %v (vec0 KNN broken)", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatal("knn returned no rows")
	}
	var firstRowID int64
	if err := rows.Scan(&firstRowID); err != nil {
		t.Fatal(err)
	}
	var src string
	if err := bm.db.QueryRowContext(ctx,
		`SELECT source FROM embeddings_meta WHERE id=?`, firstRowID).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if src != "hot" {
		t.Fatalf("nearest source = %q, want hot (кошка-сообщение)", src)
	}
}

func TestEmbedPending_RetagOnArchive(t *testing.T) {
	bm, _, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	srv := mockEmbedderServer(t)
	defer srv.Close()
	emb := NewEmbedder(srv.URL, "test-key", "")

	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "архивация переносит вектор без пересчёта", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.EmbedPending(ctx, emb, 32); err != nil {
		t.Fatal(err)
	}
	if err := bm.ArchiveAndDeleteTurns(ctx, "s1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	var src string
	if err := bm.db.QueryRowContext(ctx,
		`SELECT source FROM embeddings_meta`).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if src != "archive" {
		t.Fatalf("source = %q, want archive (перепрописка, не пересчёт)", src)
	}
	n, err := bm.EmbedPending(ctx, emb, 32)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("pending after archive = %d, want 0 (вектор уже на месте)", n)
	}
}
