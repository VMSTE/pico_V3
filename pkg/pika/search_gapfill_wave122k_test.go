package pika

// ТЗ-122 (срез К): gap-fill тянет ±1 сообщение за край цепочки.
// Корень (бенч 1-2 окт): кластер ответов 3095-3099 всплыл, а слово
// цели жило в вопросе 3094 — за краем. Края важны: вопрос пользователя
// обычно ПЕРЕД цепочкой ответов.

import (
	"context"
	"strings"
	"testing"
)

func TestSearchMessages_GapFillExtendsEdges(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	msgs := []string{
		"краевой якорь до цепочки",    // id-1 от первого члена
		"перенос документов — член",   // член 1
		"перенос завершён — член",     // член 2
		"краевой якорь после цепочки", // id+1 от последнего
	}
	for _, c := range msgs {
		if _, err := bm.SaveMessage(ctx, MessageRow{
			ChatID: "s1", PikaSessionID: "1", Role: "user",
			Content: c, Tokens: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := ms.searchMessages(ctx, "перенос", 10, "s1", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("want one merged cluster, got %d", len(res))
	}
	if !strings.Contains(res[0].Summary, "якорь до") ||
		!strings.Contains(res[0].Summary, "якорь после") {
		t.Fatalf("edges not gap-filled: %q", res[0].Summary)
	}
}
