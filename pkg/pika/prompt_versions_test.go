package pika

import (
	"context"
	"testing"
)

// Волна 124 (срез В): версия по содержимому — идемпотентна,
// монотонна, независима между компонентами. Имена компонентов —
// по CHECK схемы (archivist → ARCHIVIST_BUILD); INSERT OR IGNORE
// глотает CHECK молча, поэтому тест считает СТРОКИ, не только id.
func TestEnsurePromptVersion(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()

	id1, err := bm.EnsurePromptVersion(ctx, "archivist", "prompt v1 body")
	if err != nil {
		t.Fatal(err)
	}
	if id1 != "ARCHIVIST_BUILD/v1" {
		t.Fatalf("id = %q, want ARCHIVIST_BUILD/v1", id1)
	}

	// То же содержимое → та же версия, 0 новых строк.
	id2, err2 := bm.EnsurePromptVersion(ctx, "archivist", "prompt v1 body")
	if err2 != nil {
		t.Fatal(err2)
	}
	if id2 != id1 {
		t.Fatalf("same content: id = %q, want %q", id2, id1)
	}
	var cnt int
	if qErr := bm.db.QueryRow(
		`SELECT COUNT(*) FROM prompt_versions
		WHERE component='ARCHIVIST_BUILD'`,
	).Scan(&cnt); qErr != nil {
		t.Fatal(qErr)
	}
	if cnt != 1 {
		t.Fatalf("rows = %d, want 1 (idempotent, CHECK не съел)", cnt)
	}

	// Новое содержимое → версия растёт.
	id3, err3 := bm.EnsurePromptVersion(ctx, "archivist", "prompt v2 body")
	if err3 != nil {
		t.Fatal(err3)
	}
	if id3 != "ARCHIVIST_BUILD/v2" {
		t.Fatalf("id = %q, want ARCHIVIST_BUILD/v2", id3)
	}
	if got := bm.LatestPromptVersion(ctx, "archivist"); got != "ARCHIVIST_BUILD/v2" {
		t.Fatalf("latest = %q, want ARCHIVIST_BUILD/v2", got)
	}

	// Другой компонент — своя нумерация.
	id4, err4 := bm.EnsurePromptVersion(ctx, "atomizer", "atomizer body")
	if err4 != nil {
		t.Fatal(err4)
	}
	if id4 != "ATOMIZER/v1" {
		t.Fatalf("id = %q, want ATOMIZER/v1", id4)
	}

	// Полный текст промпта лежит в content.
	var content string
	if cErr := bm.db.QueryRow(
		`SELECT content FROM prompt_versions
		WHERE prompt_id='ARCHIVIST_BUILD/v2'`,
	).Scan(&content); cErr != nil {
		t.Fatal(cErr)
	}
	if content != "prompt v2 body" {
		t.Fatalf("content = %q", content)
	}

	// Нет версий → пусто, а не ошибка.
	if got := bm.LatestPromptVersion(ctx, "reflexor"); got != "" {
		t.Fatalf("latest unknown = %q, want empty", got)
	}
}
