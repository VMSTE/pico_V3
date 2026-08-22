package pika

import (
	"context"
	"strings"
	"testing"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// D-AUDIT-126 (волна 103): full=true снимает обрезку — модель получает
// сырой текст хита целиком (двухстадийный retrieval: сниппет → fetch).
func TestSearchMemory_FullReturnsUntruncatedContent(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	long := strings.Repeat("детали инцидента номер сорок два ", 20)
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user", Content: long,
	}); err != nil {
		t.Fatal(err)
	}
	toolCtx := toolshared.WithToolSessionContext(
		context.Background(), "main", "s1", nil,
	)
	res := ms.Execute(toolCtx, map[string]any{"query": "инцидента", "full": true})
	if res == nil || res.IsError {
		t.Fatalf("Execute full: %+v", res)
	}
	if !strings.Contains(res.ForLLM, long) {
		t.Fatalf("full=true must return raw content, got: %.300s", res.ForLLM)
	}
	res2 := ms.Execute(toolCtx, map[string]any{"query": "инцидента"})
	if res2 == nil || res2.IsError {
		t.Fatalf("Execute default: %+v", res2)
	}
	if strings.Contains(res2.ForLLM, long) {
		t.Fatal("default mode must stay truncated")
	}
}
