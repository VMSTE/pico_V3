package pika

// ТЗ-122 (срез М4): вопросная нога находит документ, невидимый лексике.

import (
	"context"
	"strings"
	"testing"
)

func TestQuestions_LegFindsLexicallyInvisible(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	// В документе НЕТ слова «авторизация» — лексические ноги слепы.
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID:  "s1",
		Role:    "assistant",
		Content: "Сохраняю токен в registry и проверяю доступ.",
	}); err != nil {
		t.Fatal(err)
	}
	caller := func(_ context.Context, _, _ string) (string, error) {
		return "1.1 как настроили авторизацию?\n1.2 что сделали с токеном?", nil
	}
	n, err := bm.GenerateQuestionsPending(ctx, caller, 8)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no docs questioned")
	}
	if err := bm.SetMemoryScope(ctx, "s1", "all"); err != nil {
		t.Fatal(err)
	}
	res := ms.Search(ctx, "как настроили авторизацию", 10, "s1")
	var viaQ, found bool
	for _, r := range res {
		if r.Source == "questions_fts" {
			viaQ = true
		}
		if strings.Contains(r.Summary, "registry") {
			found = true
		}
	}
	if !viaQ {
		t.Fatalf("questions leg contributed nothing: %+v", res)
	}
	if !found {
		t.Fatalf("doc not found via questions: %+v", res)
	}
}

func TestParseQuestionsBatch(t *testing.T) {
	got := parseQuestionsBatch(
		"1.1 как дела?\n1.2 нормально?\n2.1 кто пришёл?\nmусор без номера", 2)
	if len(got[1]) != 2 || len(got[2]) != 1 {
		t.Fatalf("parse: %v", got)
	}
}

func TestQuestionsFTSQuery(t *testing.T) {
	q := questionsFTSQuery("что там с токеном?")
	if !strings.Contains(q, "токен*") {
		t.Fatalf("нет префиксного терма: %q", q)
	}
	if questionsFTSQuery("а и") != "" {
		t.Fatal("коротыши должны отсекаться")
	}
}
