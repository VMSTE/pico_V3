package pika

// ТЗ-122 (срез Г): legacy-пустые reasoning_keywords ('' вместо JSON)
// не должны ронять слой reasoning (json_each -> malformed JSON).
// На старом коде тест падает.

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSearchReasoning_LegacyEmptyKeywordsNoError(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := bm.InsertReasoningLog(ctx, ReasoningLogRow{
		ChatID:            "s1",
		PikaSessionID:     "1",
		ReasoningText:     "старая мысль эпохи без keywords",
		ReasoningTokens:   5,
		ReasoningKeywords: json.RawMessage(""),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := ms.searchReasoning(ctx, "legacy keywords", 10); err != nil {
		t.Fatalf("reasoning layer died on legacy empty keywords: %v", err)
	}
}
