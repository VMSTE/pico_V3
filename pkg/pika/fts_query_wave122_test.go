package pika

// ТЗ-122 (срез 1): buildFTSQuery — чистка/дедуп термов, гард пустого
// запроса во всех FTS-слоях.

import (
	"context"
	"strings"
	"testing"
)

// Старый код: запрос `а "" б` давал `"а" OR  OR "б"` (дыра от continue
// в заранее аллоцированном слайсе) -> syntax error -> слой молча пустой.
func TestBuildFTSQuery_NoHolesFromQuotedWords(t *testing.T) {
	got := buildFTSQuery(`а "" б`)
	if got != `"а" OR "б"` {
		t.Fatalf("got %q, want %q", got, `"а" OR "б"`)
	}
}

func TestBuildFTSQuery_CleansAndDedupes(t *testing.T) {
	got := buildFTSQuery(`Репо, "документы" репо! Ноушена...`)
	for _, want := range []string{`"репо"`, `"документы"`, `"ноушена"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %q", want, got)
		}
	}
	if strings.Count(got, `"репо"`) != 1 {
		t.Fatalf("dup term not removed: %q", got)
	}
	if strings.Contains(got, `""`) {
		t.Fatalf("empty term leaked: %q", got)
	}
	// Срез 2: у кириллических термов добавляются стем-префиксы.
	if strings.Count(got, " OR ") < 2 {
		t.Fatalf("want several OR-ed terms, got %q", got)
	}
	if !strings.Contains(got, "ноуш*") {
		t.Fatalf("stem prefix missing in %q", got)
	}
}

func TestBuildFTSQuery_PunctuationOnly(t *testing.T) {
	if got := buildFTSQuery("?! ..."); got != "" {
		t.Fatalf("punctuation-only query must yield empty, got %q", got)
	}
}

// Гард: запрос без единого пригодного терма не роняет ни один FTS-слой.
func TestSearchLayers_EmptyFTSQueryNoError(t *testing.T) {
	_, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := ms.searchMessages(ctx, "?!", 10, "s1", "all"); err != nil {
		t.Fatalf("messages: %v", err)
	}
	if _, err := ms.searchKnowledge(ctx, "?!", 10); err != nil {
		t.Fatalf("knowledge: %v", err)
	}
	if _, err := ms.searchArchive(ctx, "?!", 10); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := ms.searchEventsArchive(ctx, "?!", 10); err != nil {
		t.Fatalf("events: %v", err)
	}
	if _, err := ms.searchReasoning(ctx, "?!", 10); err != nil {
		t.Fatalf("reasoning: %v", err)
	}
}
