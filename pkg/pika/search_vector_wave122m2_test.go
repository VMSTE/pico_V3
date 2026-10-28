package pika

// ТЗ-122 (этап 2, срез М2): векторная нога в fanOut.
// Мок-эмбеддер: тексты с «кошка» или «питомец» → e1, прочее → e2.
// Запрос «домашний питомец» лексически НЕ пересекается с сообщением
// «кошка сидит на окне» — BM25 его не найдёт, векторная нога обязана.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mockVecSearchServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var sb strings.Builder
		sb.WriteString(`{"data":[`)
		for i, text := range req.Input {
			if i > 0 {
				sb.WriteByte(',')
			}
			first, second := 0.0, 1.0
			if strings.Contains(text, "кошка") || strings.Contains(text, "питомец") {
				first, second = 1.0, 0.0
			}
			zeros := strings.Repeat(",0", 1022)
			fmt.Fprintf(&sb, `{"index":%d,"embedding":[%g,%g%s]}`, i, first, second, zeros)
		}
		sb.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sb.String()))
	}))
}

func TestSearchVector_FindsZeroLexicalOverlap(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	srv := mockVecSearchServer(t)
	defer srv.Close()

	catID, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "кошка сидит на окне", Tokens: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s2", PikaSessionID: "2", Role: "user",
		Content: "совершенно посторонний разговор", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.EmbedPending(ctx, NewEmbedder(srv.URL, "k", ""), 32); err != nil {
		t.Fatal(err)
	}

	// Без эмбеддера — нога молча выключена, семантический хит не находится.
	res := ms.Search(ctx, "домашний питомец", 10, "s1")
	for _, r := range res {
		if strings.Contains(r.Summary, "кошка") {
			t.Fatalf("lexical-only search found zero-overlap doc: %+v", r)
		}
	}

	// С эмбеддером — векторная нога спасает нулевое пересечение.
	ms.SetEmbedder(NewEmbedder(srv.URL, "k", ""))
	res = ms.Search(ctx, "домашний питомец", 10, "s1")
	found := false
	for _, r := range res {
		if strings.Contains(r.Summary, "кошка") && r.MsgID == catID {
			found = true
			if r.Source != "vec_messages" {
				t.Errorf("source = %q, want vec_messages", r.Source)
			}
		}
	}
	if !found {
		t.Fatalf("vector leg missed zero-overlap doc: %+v", res)
	}
}

func TestSearchVector_SilentFallbackWhenAPIDown(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "лексически находимое сообщение", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	// Мёртвый сервер — нога WARN, BM25 продолжает работать.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	ms.SetEmbedder(NewEmbedder(deadURL, "k", ""))
	// scope=session (дефолт) требует живой sessionID — иначе слой
	// messages пуст по D-AUDIT-104 (это и сжёг первую версию теста).
	res := ms.Search(ctx, "лексически находимое", 10, "s1")
	found := false
	for _, r := range res {
		if strings.Contains(r.Summary, "находимое") {
			found = true
		}
	}
	if !found {
		t.Fatalf("BM25 must work with dead vector API: %+v", res)
	}
}

func TestSearchVector_MessageClusteringAndAtom(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	srv := mockVecSearchServer(t)
	defer srv.Close()

	// Два соседних «кошачьих» сообщения → один кластер с gap-fill.
	for _, c := range []string{"кошка первая", "кошка вторая"} {
		if _, err := bm.SaveMessage(ctx, MessageRow{
			ChatID: "s1", PikaSessionID: "1", Role: "user",
			Content: c, Tokens: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "S-1", ChatID: "s1", PikaSessionID: "1",
		Category: "summary", Summary: "кошка любит сидеть на подоконнике",
		Confidence: 0.9, Polarity: "neutral",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.EmbedPending(ctx, NewEmbedder(srv.URL, "k", ""), 32); err != nil {
		t.Fatal(err)
	}

	ms.SetEmbedder(NewEmbedder(srv.URL, "k", ""))
	res := ms.Search(ctx, "питомец", 10, "s1")
	var vecMsg, vecAtom int
	for _, r := range res {
		switch r.Source {
		case "vec_messages":
			vecMsg++
			if !strings.Contains(r.Summary, "[диалог") {
				t.Errorf("adjacent vector hits must merge into cluster: %q", r.Summary)
			}
		case "vec_knowledge":
			vecAtom++
		}
	}
	if vecMsg != 1 {
		t.Errorf("vec message clusters = %d, want 1 (merged)", vecMsg)
	}
	if vecAtom != 1 {
		t.Errorf("vec atom hits = %d, want 1", vecAtom)
	}
}

func TestEmbedPending_SkipsEmptyArchive(t *testing.T) {
	bm, _, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	srv := mockEmbedderServer(t)
	defer srv.Close()

	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "", Tokens: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := bm.ArchiveAndDeleteTurns(ctx, "s1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	n, err := bm.EmbedPending(ctx, NewEmbedder(srv.URL, "k", ""), 32)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("embedded %d, want 0 (empty archive skipped)", n)
	}
	var model string
	if qErr := bm.db.QueryRowContext(ctx,
		`SELECT model FROM embeddings_meta`).Scan(&model); qErr != nil {
		t.Fatal(qErr)
	}
	if model != "skipped-empty" {
		t.Fatalf("model = %q, want skipped-empty", model)
	}
	_, pending, err := bm.EmbeddingCoverage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("pending = %d, want 0 (skipped не врёт покрытию)", pending)
	}
}

func TestSearchVector_MaxDistCutoff(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	srv := mockVecSearchServer(t)
	defer srv.Close()

	// Постороннее — в ДРУГОМ чате: gap-fill кластера дотягивает
	// соседей по chat_id (срез К) — соседний id в том же чате
	// загрязнял ассерт текстом (первый FAIL был именно об этом).
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "кошка на окне", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s2", PikaSessionID: "2", Role: "user",
		Content: "совсем посторонний текст", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.EmbedPending(ctx, NewEmbedder(srv.URL, "k", ""), 32); err != nil {
		t.Fatal(err)
	}
	ms.SetEmbedder(NewEmbedder(srv.URL, "k", ""))
	ms.SetVectorMaxDist(1.0) // «посторонний» на d=√2 отсекается
	// scope=all: тест изолирует порог, а не scope-фильтр (М2).
	if err := bm.SetMemoryScope(ctx, "s1", "all"); err != nil {
		t.Fatal(err)
	}

	res := ms.Search(ctx, "питомец", 10, "s1")
	for _, r := range res {
		if r.Source == "vec_messages" && strings.Contains(r.Summary, "посторонний") {
			t.Fatalf("cutoff leaked distant hit: %+v", r)
		}
	}
	found := false
	for _, r := range res {
		if r.Source == "vec_messages" && strings.Contains(r.Summary, "кошка") {
			found = true
		}
	}
	if !found {
		t.Fatalf("cutoff dropped near hit: %+v", res)
	}
}

func TestSearchVector_CrossLegFusion(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	srv := mockVecSearchServer(t)
	defer srv.Close()

	// A: лексика («питомец») И вектор (e1). B: только вектор (e1, чат s2 —
	// отдельно от кластера A). Канон RRF: A обязан быть выше B.
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "мой питомец — это кошка", Tokens: 6,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s2", PikaSessionID: "2", Role: "user",
		Content: "кошка мурлычет громко", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.EmbedPending(ctx, NewEmbedder(srv.URL, "k", ""), 32); err != nil {
		t.Fatal(err)
	}
	ms.SetEmbedder(NewEmbedder(srv.URL, "k", ""))

	res := ms.Search(ctx, "питомец", 10, "s1")
	rankA, rankB := -1, -1
	for j, r := range res {
		if strings.Contains(r.Summary, "питомец") && rankA < 0 {
			rankA = j
		}
		if strings.Contains(r.Summary, "мурлычет") && rankB < 0 {
			rankB = j
		}
	}
	if rankA < 0 {
		t.Fatalf("A (both legs) not found: %+v", res)
	}
	if rankB >= 0 && rankB < rankA {
		t.Fatalf("vector-only doc outranked two-leg doc: A=%d B=%d", rankA, rankB)
	}
}

func TestSearchVector_ProximityWeighsCloser(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()
	srv := mockVecSearchServer(t)
	defer srv.Close()

	// «кошка» = e1 (d=0 от запроса e1, prox=1), «посторонний» = e2
	// (d=√2, prox=0 → вклад нулевой, остаётся только recency).
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "кошка близко к запросу", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s2", PikaSessionID: "2", Role: "user",
		Content: "посторонний и далёкий текст", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.EmbedPending(ctx, NewEmbedder(srv.URL, "k", ""), 32); err != nil {
		t.Fatal(err)
	}
	ms.SetEmbedder(NewEmbedder(srv.URL, "k", ""))
	// scope=all: тест меряет proximity-вес, а не scope-фильтр (М2).
	if err := bm.SetMemoryScope(ctx, "s1", "all"); err != nil {
		t.Fatal(err)
	}

	res := ms.Search(ctx, "питомец", 10, "s1")
	rankNear, rankFar := -1, -1
	for j, r := range res {
		if strings.Contains(r.Summary, "близко") && rankNear < 0 {
			rankNear = j
		}
		if strings.Contains(r.Summary, "далёкий") && rankFar < 0 {
			rankFar = j
		}
	}
	if rankNear < 0 {
		t.Fatalf("near vec hit missing: %+v", res)
	}
	if rankFar >= 0 && rankFar < rankNear {
		t.Fatalf("far hit (prox=0) outranked near: near=%d far=%d", rankNear, rankFar)
	}
}
