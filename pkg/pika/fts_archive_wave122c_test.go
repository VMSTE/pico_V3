package pika

// ТЗ-122 (срез 6): прямой FTS по messages_archive — холодный архив
// доступен лексически, без атома-посредника (кейс №4 стенда).

import (
	"context"
	"strings"
	"testing"
)

func TestSearchMessagesArchive_DirectFTS(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "геморрой с oauth токеном при подключении гитхаба", Tokens: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if err := bm.ArchiveAndDeleteTurns(ctx, "s1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	// hot пуст — сообщение уехало в архив; на старом коде архив
	// без атома был лексически недостижим.
	res, err := ms.searchMessagesArchive(ctx, "oauth токеном", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !strings.Contains(res[0].Summary, "oauth") {
		t.Fatalf("archive FTS miss: %+v", res)
	}
	if res[0].Source != "messages_archive" {
		t.Fatalf("source = %q, want messages_archive", res[0].Source)
	}
}

func TestBackfillMessagesArchiveFTS(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "assistant",
		Content: "подняли searxng контейнер для поиска", Tokens: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if err := bm.ArchiveAndDeleteTurns(ctx, "s1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	// Симулируем архив до-индекса: строка есть, FTS пуст.
	if _, err := bm.db.Exec("INSERT INTO messages_archive_fts(messages_archive_fts) VALUES('delete-all')"); err != nil {
		t.Fatal(err)
	}
	n, err := bm.BackfillMessagesArchiveFTS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("backfilled %d, want 1", n)
	}
	// Повтор — идемпотентно.
	n2, err := bm.BackfillMessagesArchiveFTS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("second backfill = %d, want 0", n2)
	}
	res, err := ms.searchMessagesArchive(ctx, "searxng", 10)
	if err != nil || len(res) != 1 {
		t.Fatalf("after backfill: res=%v err=%v", res, err)
	}
}
