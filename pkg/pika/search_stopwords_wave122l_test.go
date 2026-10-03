package pika

// ТЗ-122 (срез Л): стоп-слова RU/EN в buildFTSQuery + символический
// буст размера кластера (small-to-big: скор кластера = лучший член).
// Корни (стенд 3 окт): OR по «у/нас/что/не» рождал кластеры-хабы;
// сумма BM25 по членам давала кластеру 4+ сообщений 0.95*1.6=1.52
// против потолка атома 1.0 — LOW-пояс кейсов 2/3/4/7/11.

import (
	"context"
	"strings"
	"testing"
)

func TestBuildFTSQuery_StopwordsDropped(t *testing.T) {
	q := string(rune(34)) // кавычка без экранов в исходнике
	got := buildFTSQuery("что мы делали с ноушена после того")
	for _, stop := range []string{"что", "мы", "с", "после", "того"} {
		if strings.Contains(got, q+stop+q) {
			t.Fatalf("stopword %s leaked into query: %q", stop, got)
		}
	}
	if !strings.Contains(got, q+"делали"+q) {
		t.Fatalf("content word missing: %q", got)
	}
	if !strings.Contains(got, "ноушен") {
		t.Fatalf("stem for ноушена missing: %q", got)
	}
}

func TestBuildFTSQuery_OnlyStopwordsEmpty(t *testing.T) {
	if got := buildFTSQuery("что да как и мы"); got != "" {
		t.Fatalf("want empty query for stopwords-only input, got %q", got)
	}
}

func TestMergeCluster_SymbolicSizeBoost(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	msgs := []string{
		"постороннее сообщение без маркера",
		"калибровка весов ранжирования — яркий член",
		"калибровка — слабый член",
		"ещё один слабый член про калибровку",
	}
	for _, c := range msgs {
		if _, err := bm.SaveMessage(ctx, MessageRow{
			ChatID: "s1", PikaSessionID: "1", Role: "user",
			Content: c, Tokens: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := ms.searchMessages(ctx, "калибровка", 10, "s1", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("want one merged cluster, got %d", len(res))
	}
	if res[0].Boost > 0.1001 {
		t.Fatalf("size boost must be symbolic (cap 0.1), got %v", res[0].Boost)
	}
}
