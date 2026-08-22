package pika

import (
	"context"
	"strings"
	"testing"
)

func TestSearchReasoning_FtsFindsThoughtText(t *testing.T) {
	bm := setupTestDB(t)
	defer bm.Close()
	ctx := context.Background()
	if _, err := bm.InsertReasoningLog(ctx, ReasoningLogRow{
		ChatID:          "s1",
		ReasoningText:   "load_image вернул путь, но не содержимое — подожду, может система покажет",
		ReasoningTokens: 10,
		PikaSessionID:   "1",
	}); err != nil {
		t.Fatal(err)
	}
	ms := NewMemorySearch(bm)
	res, err := ms.searchReasoning(ctx, "подожду система покажет", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range res {
		if strings.Contains(r.Summary, "подожду") {
			found = true
		}
	}
	if !found {
		t.Fatalf("thought text not found in %#v", res)
	}
}
