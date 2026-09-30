package pika

// ТЗ-122 (срез 1.5): кластерный буст сообщений + RRF-слияние слоёв.

import (
	"context"
	"testing"
	"time"
)

// Эталон №1 (30 сен): ответ размазан по кластеру сообщений 3127-3129,
// поодиночке слабые. Соседи в выборке -> буст; одиночный хит -> без буста.
func TestSearchMessages_NeighborClusterBoost(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	cluster := []string{
		"обсуждаем переезд документы",
		"короткое решение про документы",
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
	// Разделитель без терма, затем одиночный далёкий хит.
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
	var clusterHits, spamHits int
	for _, r := range res {
		if r.ChatID == "s1" {
			clusterHits++
			if r.Boost <= 0 {
				t.Fatalf("cluster hit without boost: %+v", r)
			}
		}
		if r.ChatID == "s2" {
			spamHits++
			if r.Boost != 0 {
				t.Fatalf("lonely hit got boost: %+v", r)
			}
		}
	}
	if clusterHits != 3 || spamHits != 1 {
		t.Fatalf("cluster=%d spam=%d, want 3/1", clusterHits, spamHits)
	}
}

// RRF: лучший хит КАЖДОГО слоя получает свой полный вес независимо от
// чужой шкалы bm25 (старый общий min-max котёл давал знаниям ~0.15).
func TestScoreResults_RRFPerLayer(t *testing.T) {
	results := []rawResult{
		{Type: "session", Source: "messages", RawBM25: -10, IsFTS: true,
			LayerPrio: prioMessages, CreatedAt: time.Now()},
		{Type: "session", Source: "messages", RawBM25: -5, IsFTS: true,
			LayerPrio: prioMessages, CreatedAt: time.Now()},
		{Type: "knowledge", Source: "knowledge_atoms", RawBM25: -1, IsFTS: true,
			LayerPrio: prioKnowledge, CreatedAt: time.Now()},
		{Type: "knowledge", Source: "knowledge_atoms", RawBM25: -0.5, IsFTS: true,
			LayerPrio: prioKnowledge, CreatedAt: time.Now()},
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
	// messages best: 0.5 * 61/61 + recency(0.1) = 0.6
	if msgBest < 0.55 || msgBest > 0.65 {
		t.Fatalf("messages best = %v, want ~0.6", msgBest)
	}
	// knowledge best: 1.0 * 61/61 + 0.1 = 1.1
	if knowBest < 1.0 {
		t.Fatalf("knowledge best = %v, want >=1.0", knowBest)
	}
}
