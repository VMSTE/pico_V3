package pika

// ТЗ-122 (срез И): ось времени — after/before на слое messages.
// datetime() на обеих сторонах: hot хранит "2026-09-30 13:01:42",
// archive — RFC3339 (нормализует сравнение, данные не трогаем).

import (
	"context"
	"strings"
	"testing"
)

func TestSearchMessages_TimeFilter(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	idOld, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "ось времени старый май", Tokens: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s2", PikaSessionID: "2", Role: "user",
		Content: "ось времени свежий сентябрь", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.db.Exec(
		"UPDATE messages SET ts='2026-05-12 10:00:00' WHERE id=?", idOld,
	); err != nil {
		t.Fatal(err)
	}

	all, err := ms.searchMessages(ctx, "ось времени", 10, "s1", "all")
	if err != nil || len(all) != 2 {
		t.Fatalf("no filter: %d hits, err %v", len(all), err)
	}

	after, err := ms.searchMessages(
		ctx, "ось времени", 10, "s1", "all", [2]string{"2026-09-01", ""},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || !strings.Contains(after[0].Summary, "свежий") {
		t.Fatalf("after filter: %+v", after)
	}

	before, err := ms.searchMessages(
		ctx, "ось времени", 10, "s1", "all", [2]string{"", "2026-06-01"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || !strings.Contains(before[0].Summary, "старый") {
		t.Fatalf("before filter: %+v", before)
	}
}
