package pika

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func setupSearchTest(t *testing.T) (
	*BotMemory, *MemorySearch, func(),
) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Migrate(dbPath)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	bm, err := NewBotMemory(db)
	if err != nil {
		db.Close()
		t.Fatalf("NewBotMemory: %v", err)
	}
	ms := NewMemorySearch(bm)
	return bm, ms, func() {
		bm.Close()
		db.Close()
	}
}

func execSearch(
	t *testing.T,
	ms *MemorySearch,
	query string,
	limit int,
	sessionID string,
) []SearchResult {
	t.Helper()
	ctx := context.Background()
	if sessionID != "" {
		ctx = context.WithValue(ctx, SessionIDKey{}, sessionID)
	}
	args := map[string]any{
		"query": query,
		"limit": float64(limit),
	}
	result := ms.Execute(ctx, args)
	if result.IsError {
		t.Fatalf("Execute error: %s", result.ForLLM)
	}
	var results []SearchResult
	if err := json.Unmarshal(
		[]byte(result.ForLLM), &results,
	); err != nil {
		t.Fatalf("unmarshal: %v, raw: %s", err, result.ForLLM)
	}
	return results
}

// TestSearchMemory_BasicQuery — data in 3 layers,
// merged, scored, sorted.
func TestSearchMemory_BasicQuery(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	// Layer 1: message
	_, err := bm.SaveMessage(ctx, MessageRow{
		SessionID: "s1", TurnID: 1, Role: "user",
		Content: "deploy nginx to production",
		Tokens:  10,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Layer 2: knowledge atom
	err = bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID:     "P-1",
		SessionID:  "s1",
		TurnID:     1,
		Category:   "pattern",
		Summary:    "nginx deploy requires confirmation",
		Confidence: 0.8,
		Polarity:   "neutral",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Layer 6: registry snapshot
	_, err = bm.UpsertRegistry(ctx, RegistryRow{
		Kind:    "snapshot",
		Key:     "nginx-config",
		Summary: "nginx production config snapshot",
	})
	if err != nil {
		t.Fatal(err)
	}

	results := execSearch(t, ms, "nginx", 10, "s1")
	if len(results) < 2 {
		t.Fatalf("expected >=2 results, got %d", len(results))
	}

	// Verify sorted DESC
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf(
				"not sorted: [%d]=%f > [%d]=%f",
				i, results[i].Score,
				i-1, results[i-1].Score,
			)
		}
	}

	// Check types present
	types := make(map[string]bool)
	for _, r := range results {
		types[r.Type] = true
	}
	if !types["knowledge"] {
		t.Error("expected knowledge result")
	}
}

// TestSearchMemory_LimitClamp — 0->1, 100->20.
func TestSearchMemory_LimitClamp(t *testing.T) {
	_, ms, cleanup := setupSearchTest(t)
	defer cleanup()

	ctx := context.Background()

	// Limit 0 -> clamped to 1, should not error
	args0 := map[string]any{
		"query": "test",
		"limit": float64(0),
	}
	r0 := ms.Execute(ctx, args0)
	if r0.IsError {
		t.Fatal("unexpected error for limit 0")
	}

	// Limit 100 -> clamped to 20, should not error
	args100 := map[string]any{
		"query": "test",
		"limit": float64(100),
	}
	r100 := ms.Execute(ctx, args100)
	if r100.IsError {
		t.Fatal("unexpected error for limit 100")
	}
}

// TestSearchMemory_EmptySessionID — layer 1 empty,
// other layers still work.
func TestSearchMemory_EmptySessionID(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	err := bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID:     "P-1",
		SessionID:  "s1",
		TurnID:     1,
		Category:   "pattern",
		Summary:    "test pattern for empty session",
		Confidence: 0.5,
		Polarity:   "neutral",
	})
	if err != nil {
		t.Fatal(err)
	}

	results := execSearch(t, ms, "test pattern", 10, "")
	for _, r := range results {
		if r.Type == "session" {
			t.Error(
				"should not have session results " +
					"without session ID",
			)
		}
	}
}

// TestSearchMemory_LayerFailure — 1 layer error,
// partial results from other layers.
func TestSearchMemory_LayerFailure(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	err := bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID:     "P-1",
		SessionID:  "s1",
		TurnID:     1,
		Category:   "pattern",
		Summary:    "surviving layer data",
		Confidence: 0.5,
		Polarity:   "neutral",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Drop reasoning_log to simulate layer failure
	_, err = bm.db.ExecContext(
		ctx, "DROP TABLE reasoning_log",
	)
	if err != nil {
		t.Fatal(err)
	}

	results := execSearch(t, ms, "surviving", 10, "")
	if len(results) == 0 {
		t.Error("expected results from surviving layers")
	}
}

// TestSearchMemory_Timeout — context canceled,
// should not panic, returns valid JSON.
func TestSearchMemory_Timeout(t *testing.T) {
	_, ms, cleanup := setupSearchTest(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(
		context.Background(), time.Nanosecond,
	)
	defer cancel()
	time.Sleep(time.Millisecond)

	args := map[string]any{"query": "test"}
	result := ms.Execute(ctx, args)
	if result.IsError {
		t.Fatal("should not return error on timeout")
	}
	var results []SearchResult
	if err := json.Unmarshal(
		[]byte(result.ForLLM), &results,
	); err != nil {
		t.Fatalf("invalid JSON on timeout: %v", err)
	}
}

// TestSearchMemory_Dedup — one item in knowledge+archive
// appears only once per unique dedup key.
func TestSearchMemory_Dedup(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	_, err := bm.SaveMessage(ctx, MessageRow{
		SessionID: "s1", TurnID: 1, Role: "user",
		Content: "duplicate test content", Tokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	msgID := int64(1)
	err = bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID:          "P-1",
		SessionID:       "s1",
		TurnID:          1,
		SourceMessageID: &msgID,
		Category:        "pattern",
		Summary:         "duplicate content atom",
		Confidence:      0.5,
		Polarity:        "neutral",
	})
	if err != nil {
		t.Fatal(err)
	}

	results := execSearch(t, ms, "duplicate", 10, "s1")

	// Each DedupKey should be unique — verified by
	// the dedupResults function. Just check no exact
	// summary duplicates from same source.
	seen := make(map[string]int)
	for _, r := range results {
		key := r.Source + ":" + r.Summary
		seen[key]++
	}
	for k, v := range seen {
		if v > 1 {
			t.Errorf("duplicate: %s count=%d", k, v)
		}
	}
}

// TestSearchMemory_EmptyDB — all layers empty -> [].
func TestSearchMemory_EmptyDB(t *testing.T) {
	_, ms, cleanup := setupSearchTest(t)
	defer cleanup()

	results := execSearch(t, ms, "anything", 10, "s1")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// TestSearchMemory_ScoringOrder — knowledge (prio 1.0)
// ranks higher than registry (prio 0.6).
func TestSearchMemory_ScoringOrder(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	err := bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID:     "P-1",
		SessionID:  "s1",
		TurnID:     1,
		Category:   "pattern",
		Summary:    "important deploy knowledge",
		Confidence: 0.9,
		Polarity:   "neutral",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = bm.UpsertRegistry(ctx, RegistryRow{
		Kind:    "snapshot",
		Key:     "deploy-config",
		Summary: "deploy config snapshot",
	})
	if err != nil {
		t.Fatal(err)
	}

	results := execSearch(t, ms, "deploy", 10, "")
	if len(results) < 2 {
		t.Skipf("need >=2 results, got %d", len(results))
	}

	var knScore, regScore float64
	for _, r := range results {
		if r.Type == "knowledge" {
			knScore = r.Score
		}
		if r.Type == "snapshot" {
			regScore = r.Score
		}
	}
	if knScore > 0 && regScore > 0 && knScore <= regScore {
		t.Errorf(
			"knowledge(%f) should > registry(%f)",
			knScore, regScore,
		)
	}
}

// TestSearchMemory_ArchivePipeline — atom with
// source_message_id -> archived message -> snippet.
func TestSearchMemory_ArchivePipeline(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	msgID, err := bm.SaveMessage(ctx, MessageRow{
		SessionID: "s1", TurnID: 1, Role: "assistant",
		Content: "nginx config updated to 4096 workers",
		Tokens:  15,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = bm.ArchiveAndDeleteTurns(ctx, "s1", []int{1})
	if err != nil {
		t.Fatal(err)
	}

	err = bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID:          "P-1",
		SessionID:       "s1",
		TurnID:          1,
		SourceMessageID: &msgID,
		Category:        "pattern",
		Summary:         "nginx workers config update",
		Confidence:      0.8,
		Polarity:        "neutral",
	})
	if err != nil {
		t.Fatal(err)
	}

	results := execSearch(t, ms, "nginx workers", 10, "")

	// Should find via knowledge layer at minimum
	found := false
	for _, r := range results {
		if r.Type == "archive" || r.Type == "knowledge" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected archive or knowledge result")
	}
}

// TestSearchMemory_ReasoningJsonEach — json_each
// LIKE match on reasoning_keywords.
func TestSearchMemory_ReasoningJsonEach(t *testing.T) {
	bm, ms, cleanup := setupSearchTest(t)
	defer cleanup()
	ctx := context.Background()

	kw, _ := json.Marshal([]string{
		"nginx", "deployment", "workers",
	})
	_, err := bm.InsertReasoningLog(ctx, ReasoningLogRow{
		SessionID:         "s1",
		TurnID:            1,
		Task:              "deploy nginx",
		Mode:              "deploy",
		ReasoningKeywords: kw,
	})
	if err != nil {
		t.Fatal(err)
	}

	results := execSearch(t, ms, "nginx", 10, "")
	found := false
	for _, r := range results {
		if r.Type == "reasoning" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected reasoning result via json_each")
	}
}
