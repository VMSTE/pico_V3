package pika

// ТЗ-122: срез 1.5 (RRF-слияние слоёв) + срез 4 (кластер как единица
// выдачи с gap-fill).

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Эталон №1 (30 сен): ответ размазан по кластеру 3127-3129; дословная
// цитата была в 3128, которая сама в топ-10 не входила, а соседи входили.
// Кластер сливается в один результат: текст всех сообщений цепочки
// (gap-fill, включая нематчнувшиеся), скор — лучший член + буст за размер.
func TestSearchMessages_NeighborClusterMerge(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	cluster := []string{
		"обсуждаем переезд документы",
		"между хитами лежит ответ без термов запроса",
		"итог: документы переезжают в новое место",
	}
	for _, c := range cluster {
		if _, err := bm.SaveMessage(ctx, MessageRow{
			ChatID: "s1", PikaSessionID: "1", Role: "user",
			Content: c, Tokens: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Разделитель без терма, затем одиночный хит в ДРУГОМ чате.
	for _, c := range []string{
		"посторонний разговор",
		"документы документы документы — старый спам",
	} {
		if _, err := bm.SaveMessage(ctx, MessageRow{
			ChatID: "s2", PikaSessionID: "9", Role: "user",
			Content: c, Tokens: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := ms.searchMessages(ctx, "документы", 10, "s1", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2 (merged cluster + spam)", len(res))
	}
	var cl, spam *rawResult
	for i := range res {
		switch res[i].ChatID {
		case "s1":
			cl = &res[i]
		case "s2":
			spam = &res[i]
		}
	}
	if cl == nil || spam == nil {
		t.Fatalf("missing cluster or spam: %+v", res)
	}
	if cl.Boost <= 0 {
		t.Fatalf("cluster without size boost: %+v", *cl)
	}
	if !strings.Contains(cl.Summary, "переезд") ||
		!strings.Contains(cl.Summary, "итог") {
		t.Fatalf("cluster summary missing members: %q", cl.Summary)
	}
	// gap-fill: нематчнувшееся сообщение между хитами попало в кластер
	if !strings.Contains(cl.Summary, "между хитами лежит ответ") {
		t.Fatalf("gap-fill missing: %q", cl.Summary)
	}
	if spam.Boost != 0 {
		t.Fatalf("lonely hit got boost: %+v", *spam)
	}
}

// RRF: лучший хит КАЖДОГО слоя получает свой полный вес независимо от
// чужой шкалы bm25 (старый общий min-max котёл давал знаниям ~0.15).
func TestScoreResults_RRFPerLayer(t *testing.T) {
	results := []rawResult{
		{
			Type: "session", Source: "messages", RawBM25: -10, IsFTS: true,
			LayerPrio: prioMessages, CreatedAt: time.Now(),
		},
		{
			Type: "session", Source: "messages", RawBM25: -5, IsFTS: true,
			LayerPrio: prioMessages, CreatedAt: time.Now(),
		},
		{
			Type: "knowledge", Source: "knowledge_atoms", RawBM25: -1, IsFTS: true,
			LayerPrio: prioKnowledge, CreatedAt: time.Now(),
		},
		{
			Type: "knowledge", Source: "knowledge_atoms", RawBM25: -0.5, IsFTS: true,
			LayerPrio: prioKnowledge, CreatedAt: time.Now(),
		},
	}
	scored := scoreResults(results)
	var msgBest, knowBest float64
	for _, s := range scored {
		if s.Source == "messages" && s.Score > msgBest {
			msgBest = s.Score
		}
		if s.Source == "knowledge_atoms" && s.Score > knowBest {
			knowBest = s.Score
		}
	}
	// Срез 3 (мягкий вес): messages best = 0.95 * 61/61 + 0.1 = 1.05
	if msgBest < 1.0 || msgBest > 1.1 {
		t.Fatalf("messages best = %v, want ~1.05", msgBest)
	}
	// knowledge best: 1.0 * 61/61 + 0.1 = 1.1
	if knowBest < 1.0 {
		t.Fatalf("knowledge best = %v, want >=1.0", knowBest)
	}
}

// Срез 5: слой с хитами не исчезает из выдачи целиком (и наоборот —
// слабый слой не впрыскивается).
func TestEnsureLayerDiversity(t *testing.T) {
	var scored []SearchResult
	for i := 0; i < 10; i++ {
		scored = append(scored, SearchResult{
			Type: "session", Source: "messages",
			Score: 1.5 - float64(i)*0.01,
		})
	}
	scored = append(scored, SearchResult{
		Type: "knowledge", Source: "knowledge_atoms", Score: 1.0,
	})
	out := ensureLayerDiversity(scored, 10)
	found := false
	for _, r := range out {
		if r.Source == "knowledge_atoms" {
			found = true
		}
	}
	if !found {
		t.Fatal("knowledge layer not injected")
	}
	if len(out) != 10 {
		t.Fatalf("len = %d, want 10", len(out))
	}

	// Свежий слайс: ensureLayerDiversity мутирует окно (общий backing).
	var weak []SearchResult
	for i := 0; i < 10; i++ {
		weak = append(weak, SearchResult{
			Type: "session", Source: "messages", Score: 1.5,
		})
	}
	weak = append(weak, SearchResult{
		Type: "knowledge", Source: "knowledge_atoms", Score: 0.5,
	})
	out2 := ensureLayerDiversity(weak, 10)
	for _, r := range out2 {
		if r.Source == "knowledge_atoms" {
			t.Fatal("weak layer must not be injected")
		}
	}
}
