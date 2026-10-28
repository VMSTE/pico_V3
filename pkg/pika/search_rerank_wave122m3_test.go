package pika

// ТЗ-122 (срез М3): реранкер — подъём ответа над эхо, фолбэки.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func rerankFixture() []SearchResult {
	return []SearchResult{
		{Summary: "какой мой любимый цвет?", Type: "session"},
		{Summary: "укакой мой любимый цвет", Type: "session"},
		{Summary: "напомни про цвет", Type: "session"},
		{Summary: "твой любимый цвет — синий, ты говорил в мае", Type: "session"},
		{Summary: "обсуждали вкусы", Type: "session"},
		{Summary: "посторонняя запись", Type: "session"},
	}
}

func TestRerank_LiftsAnswerOverEcho(t *testing.T) {
	ms := &MemorySearch{}
	ms.SetReranker(func(_ context.Context, _, user string) (string, error) {
		if !strings.Contains(user, "какой мой любимый цвет") {
			t.Errorf("судье не передан вопрос: %q", user[:60])
		}
		return "4, 1, 2, 3, 5, 6", nil // судья ставит ответ первым
	})
	out := ms.maybeRerank(context.Background(), "какой мой любимый цвет", rerankFixture())
	if !strings.Contains(out[0].Summary, "синий") {
		t.Fatalf("ответ не поднят наверх: %q", out[0].Summary)
	}
	if len(out) != 6 {
		t.Fatalf("потеряны хиты: %d из 6", len(out))
	}
}

func TestRerank_GarbageKeepsOrder(t *testing.T) {
	ms := &MemorySearch{}
	ms.SetReranker(func(_ context.Context, _, _ string) (string, error) {
		return "извини, не могу помочь с этим", nil
	})
	in := rerankFixture()
	out := ms.maybeRerank(context.Background(), "q", in)
	for i := range in {
		if out[i].Summary != in[i].Summary {
			t.Fatalf("мусорный ответ изменил порядок на позиции %d", i)
		}
	}
}

func TestRerank_ErrorKeepsOrder(t *testing.T) {
	ms := &MemorySearch{}
	ms.SetReranker(func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("api down")
	})
	in := rerankFixture()
	out := ms.maybeRerank(context.Background(), "q", in)
	if out[0].Summary != in[0].Summary {
		t.Fatal("сбой вызова изменил порядок")
	}
}

func TestRerank_NilIsNoop(t *testing.T) {
	ms := &MemorySearch{}
	in := rerankFixture()
	out := ms.maybeRerank(context.Background(), "q", in)
	if out[0].Summary != in[0].Summary {
		t.Fatal("nil-реранкер изменил порядок")
	}
}

func TestParseRerankOrder(t *testing.T) {
	got := parseRerankOrder("3, 1, 2", 5)
	if len(got) != 3 || got[0] != 2 || got[1] != 0 || got[2] != 1 {
		t.Fatalf("parse: %v", got)
	}
	if parseRerankOrder("да", 5) != nil {
		t.Fatal("мусор должен давать nil")
	}
	if parseRerankOrder("1, 1, 1", 5) != nil {
		t.Fatal("дубли без покрытия должны давать nil")
	}
}
