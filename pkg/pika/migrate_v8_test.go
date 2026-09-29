package pika

import (
	"context"
	"path/filepath"
	"testing"
)

// Волна 124 (срез В): миграция v8 — prompt_snapshots.full_prompt.
// Expand-only: колонка добавляется, старые строки остаются NULL.
func TestMigrateV8_FullPromptColumn(t *testing.T) {
	db, mErr := Migrate(filepath.Join(t.TempDir(), "test.db"))
	if mErr != nil {
		t.Fatalf("Migrate: %v", mErr)
	}
	defer db.Close()

	var cnt int
	if qErr := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('prompt_snapshots')
		WHERE name='full_prompt'`,
	).Scan(&cnt); qErr != nil {
		t.Fatal(qErr)
	}
	if cnt != 1 {
		t.Fatalf("full_prompt column count = %d, want 1", cnt)
	}

	// Полный промпт пишется и читается обратно.
	bm, bmErr := NewBotMemory(db)
	if bmErr != nil {
		t.Fatal(bmErr)
	}
	defer bm.Close()
	full := "SYSTEM PROMPT\n--- MEMORY BRIEF ---\nbrief body"
	if iErr := bm.InsertPromptSnapshot(
		context.Background(), "snap-v8", "trace-v8", "chat1", "1",
		"", "", "",
		map[string]int{"core": 10, "brief": 2},
		"hash", "preview", 5,
		full, map[string]string{"archivarius": "ARCHIVIST_BUILD/v2"},
	); iErr != nil {
		t.Fatal(iErr)
	}
	var gotPrompt, gotArch string
	if sErr := db.QueryRow(
		`SELECT coalesce(full_prompt,''), coalesce(archivarius_version,'')
		FROM prompt_snapshots WHERE snapshot_id='snap-v8'`,
	).Scan(&gotPrompt, &gotArch); sErr != nil {
		t.Fatal(sErr)
	}
	if gotPrompt != full {
		t.Fatalf("full_prompt = %q, want %q", gotPrompt, full)
	}
	if gotArch != "ARCHIVIST_BUILD/v2" {
		t.Fatalf("archivarius_version = %q", gotArch)
	}
}
