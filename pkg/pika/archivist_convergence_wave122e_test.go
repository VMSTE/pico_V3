package pika

// ТЗ-122 (срез Д): конвергенция Архивариуса на движок search_memory.
// До среза: свой fan-out без messages_archive / reasoning_fts /
// artifacts; hot FTS был мёртв всю жизнь (ambiguous column, срез Г).

import (
	"context"
	"strings"
	"testing"
)

// archSearchMessages — адаптер для тестов эпохи до конвергенции:
// searchMessages Архивариуса удалён, семантика — messages-аспект
// executeSearchContext (движок + recency-хвост).
func archSearchMessages(
	t *testing.T, a *Archivist, ctx context.Context, query string,
) []MessageHit {
	t.Helper()
	res, err := a.executeSearchContext(ctx, SearchContextParams{
		Query: query, Aspects: []string{"messages"},
	}, false)
	if err != nil {
		t.Fatalf("executeSearchContext: %v", err)
	}
	return res.Messages
}

// Главная цифра среза: холодный архив доезжает до Архивариуса дословно.
// На старом коде (свой fan-out без messages_archive) — MISS.
func TestExecuteSearchContext_ArchiveVisibleViaEngine(t *testing.T) {
	a, cleanup := newTestArchivist(t, newMockProvider())
	defer cleanup()
	ctx := context.Background()

	if _, err := a.mem.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "конвергенция движка поиска архивариуса", Tokens: 6,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.mem.ArchiveAndDeleteTurns(ctx, "s1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if err := a.mem.SetMemoryScope(ctx, "s1", "all"); err != nil {
		t.Fatal(err)
	}
	a.currentSessionKey = "s1"

	res, err := a.executeSearchContext(ctx, SearchContextParams{
		Query: "конвергенция", Aspects: []string{"messages"},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range res.Messages {
		if strings.Contains(m.Content, "конвергенция") {
			found = true
		}
	}
	if !found {
		t.Fatalf("archived message not visible to archivist: %+v", res.Messages)
	}
}

// Polarity-фильтр атомов сохранён после конвергенции (negative-first).
func TestExecuteSearchContext_PolarityPreserved(t *testing.T) {
	a, cleanup := newTestArchivist(t, newMockProvider())
	defer cleanup()
	ctx := context.Background()

	if err := a.mem.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "S-1", ChatID: "s1", Category: "summary",
		Summary: "конвергенция положительный факт", Polarity: "positive",
		Confidence: 0.9, Verified: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.mem.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "S-2", ChatID: "s1", Category: "constraint",
		Summary: "конвергенция негативный запрет", Polarity: "negative",
		Confidence: 0.9, Verified: 1,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := a.executeSearchContext(ctx, SearchContextParams{
		Query:    "конвергенция",
		Aspects:  []string{"knowledge"},
		Polarity: "negative",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Knowledge) != 1 || res.Knowledge[0].Polarity != "negative" {
		t.Fatalf("polarity filter broken: %+v", res.Knowledge)
	}
}
