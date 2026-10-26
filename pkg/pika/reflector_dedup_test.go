package pika

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// Реальная пара дублей из боя: P-463 vs P-478.
const (
	dedupSummaryA = "Model returns empty responses, often preceded " +
		"by WebSocket read errors, indicating instability with the " +
		"model provider (e.g., OpenRouter) or network connectivity."
	dedupSummaryB = "Model frequently returns 'empty responses' " +
		"preceded by 'WebSocket read errors', indicating instability " +
		"with the model provider or network connectivity, " +
		"especially after intensive operations."
	dedupSummaryOther = "Failed to proxy WebSocket: dial tcp " +
		"connection refused on port 18790 during launcher startup"
)

func TestSummarySimilarity_NearDupVsOtherTheme(t *testing.T) {
	if got := summarySimilarity(dedupSummaryA, dedupSummaryB); got <
		dedupSimilarityThreshold {
		t.Fatalf(
			"near-dup scored %.2f, want >= %.2f",
			got, dedupSimilarityThreshold,
		)
	}
	if got := summarySimilarity(dedupSummaryA, dedupSummaryOther); got >=
		dedupSimilarityThreshold {
		t.Fatalf(
			"different theme scored %.2f, want < %.2f",
			got, dedupSimilarityThreshold,
		)
	}
}

func TestApplyPattern_DedupGateConfirmsExisting(t *testing.T) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()
	ctx := context.Background()

	insertTestAtom(
		t, db, "P-1", "pattern", dedupSummaryA, "negative", 0.5, nil,
	)

	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), nil, nil,
		DefaultReflectorConfig(),
	)
	err = pipeline.applyPattern(ctx, reflectorPattern{
		Summary:         dedupSummaryB, // переформулировка
		Polarity:        "negative",
		EvidenceAtomIDs: []string{"S-9"},
	}, nil)
	if err != nil {
		t.Fatalf("applyPattern: %v", err)
	}

	var count int
	var conf float64
	var hist string
	db.QueryRow(
		`SELECT COUNT(*), MAX(confidence) FROM knowledge_atoms
		WHERE category='pattern'`,
	).Scan(&count, &conf)
	if count != 1 {
		t.Fatalf("expected 1 pattern (no dup), got %d", count)
	}
	if conf < 0.59 || conf > 0.61 {
		t.Errorf("confidence = %.2f, want 0.6 (D-59 +0.1)", conf)
	}
	db.QueryRow(
		`SELECT history FROM knowledge_atoms WHERE atom_id='P-1'`,
	).Scan(&hist)
	if !strings.Contains(hist, "reflector_dedup") {
		t.Errorf("history missing reflector_dedup entry: %s", hist)
	}
}

func TestApplyPattern_InsertWhenThemeIsNew(t *testing.T) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()
	ctx := context.Background()

	insertTestAtom(
		t, db, "P-1", "pattern", dedupSummaryOther, "negative", 0.5, nil,
	)

	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), nil, nil,
		DefaultReflectorConfig(),
	)
	smid := int64(42)
	atomMap := map[string]*KnowledgeAtomRow{
		"S-1": {AtomID: "S-1", SourceMessageID: &smid},
	}
	err = pipeline.applyPattern(ctx, reflectorPattern{
		Summary:         dedupSummaryA,
		Polarity:        "negative",
		EvidenceAtomIDs: []string{"S-1"},
	}, atomMap)
	if err != nil {
		t.Fatalf("applyPattern: %v", err)
	}

	var count int
	db.QueryRow(
		`SELECT COUNT(*) FROM knowledge_atoms WHERE category='pattern'`,
	).Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 patterns (new inserted), got %d", count)
	}
	// Provenance: новый атом наследует source_message_id evidence.
	var gotSmid sql.NullInt64
	db.QueryRow(
		`SELECT source_message_id FROM knowledge_atoms
		WHERE category='pattern' AND atom_id != 'P-1'`,
	).Scan(&gotSmid)
	if !gotSmid.Valid || gotSmid.Int64 != 42 {
		t.Errorf(
			"source_message_id = %v, want 42 (from evidence S-1)",
			gotSmid,
		)
	}
}

func TestApplyRunbook_DedupGateConfirmsExisting(t *testing.T) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()
	ctx := context.Background()

	trigger := "Model returns 'empty response' or WebSocket read " +
		"errors, especially during or after intensive operations."
	insertTestAtom(
		t, db, "R-1", "runbook_draft", trigger, "negative", 0.5, nil,
	)

	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), nil, nil,
		DefaultReflectorConfig(),
	)
	err = pipeline.applyRunbook(ctx, reflectorRunbook{
		Trigger:         trigger,
		Steps:           []string{"1. Check logs"},
		EvidenceAtomIDs: []string{"S-1", "S-2", "S-3"},
	}, nil)
	if err != nil {
		t.Fatalf("applyRunbook: %v", err)
	}

	var count int
	var conf float64
	db.QueryRow(
		`SELECT COUNT(*), MAX(confidence) FROM knowledge_atoms
		WHERE category='runbook_draft'`,
	).Scan(&count, &conf)
	if count != 1 {
		t.Fatalf("expected 1 runbook (no dup), got %d", count)
	}
	if conf < 0.59 || conf > 0.61 {
		t.Errorf("confidence = %.2f, want 0.6", conf)
	}
}

func TestApplyMerge_PreservesProvenance(t *testing.T) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()
	ctx := context.Background()

	insertTestAtom(t, db, "S-1", "summary", "a", "negative", 0.5, nil)
	insertTestAtom(t, db, "S-2", "summary", "b", "negative", 0.5, nil)
	smid := int64(42)
	atomMap := map[string]*KnowledgeAtomRow{
		"S-1": {
			AtomID: "S-1", Category: "summary", Summary: "a",
			Polarity: "negative", Confidence: 0.5,
			ChatID: "s", SourceMessageID: &smid,
		},
		"S-2": {
			AtomID: "S-2", Category: "summary", Summary: "b",
			Polarity: "negative", Confidence: 0.5, ChatID: "s",
		},
	}

	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), nil, nil,
		DefaultReflectorConfig(),
	)
	err = pipeline.applyMerge(ctx, reflectorDuplicate{
		KeepID: "S-1", MergeIDs: []string{"S-2"},
	}, atomMap)
	if err != nil {
		t.Fatalf("applyMerge: %v", err)
	}

	var gotSmid sql.NullInt64
	db.QueryRow(
		`SELECT source_message_id FROM knowledge_atoms`,
	).Scan(&gotSmid)
	if !gotSmid.Valid || gotSmid.Int64 != 42 {
		t.Errorf("merged atom source_message_id = %v, want 42", gotSmid)
	}
}

func TestBuildUserContent_MarksExisting(t *testing.T) {
	pipeline := NewReflectorPipeline(
		nil, nil, nil, nil, DefaultReflectorConfig(),
	)
	atoms := []KnowledgeAtomRow{
		{AtomID: "S-1", Category: "summary", Summary: "new"},
		{AtomID: "P-1", Category: "pattern", Summary: "old"},
	}
	out := pipeline.buildUserContent(
		atoms, ReflectorDaily, map[string]bool{"P-1": true},
	)
	if !strings.Contains(out, `"existing":true`) {
		t.Errorf("existing flag missing in payload: %s", out)
	}
}
