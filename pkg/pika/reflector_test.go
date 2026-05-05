package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"

	_ "modernc.org/sqlite"
)

// --- Mock LLM Provider ---

type mockReflectorProvider struct {
	response string
	err      error
	calls    int
}

func (m *mockReflectorProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return &providers.LLMResponse{
		Content: m.response,
	}, nil
}

func (m *mockReflectorProvider) GetDefaultModel() string {
	return "test"
}

// --- Helpers ---

func setupReflectorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func insertTestAtom(
	t *testing.T, db *sql.DB,
	atomID, category, summary, polarity string,
	conf float64, tags []string,
) {
	t.Helper()
	var tagsJSON []byte
	if tags != nil {
		tagsJSON, _ = json.Marshal(tags)
	}
	_, err := db.Exec(
		`INSERT INTO knowledge_atoms
		(atom_id, session_id, turn_id, category,
		summary, confidence, polarity, tags)
		VALUES(?,?,?,?,?,?,?,?)`,
		atomID, "test-session", 1, category,
		summary, conf, polarity,
		func() any {
			if tagsJSON == nil {
				return nil
			}
			return string(tagsJSON)
		}(),
	)
	if err != nil {
		t.Fatalf("insert atom %s: %v", atomID, err)
	}
}

func countAtoms(t *testing.T, db *sql.DB) int {
	t.Helper()
	var c int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM knowledge_atoms`,
	).Scan(&c)
	if err != nil {
		t.Fatalf("count atoms: %v", err)
	}
	return c
}

func getAtomConfidence(
	t *testing.T, db *sql.DB, atomID string,
) float64 {
	t.Helper()
	var c float64
	err := db.QueryRow(
		`SELECT confidence FROM knowledge_atoms
		WHERE atom_id=?`, atomID,
	).Scan(&c)
	if err != nil {
		t.Fatalf("get confidence %s: %v", atomID, err)
	}
	return c
}

func atomExists(
	t *testing.T, db *sql.DB, atomID string,
) bool {
	t.Helper()
	var c int
	_ = db.QueryRow(
		`SELECT COUNT(*) FROM knowledge_atoms
		WHERE atom_id=?`, atomID,
	).Scan(&c)
	return c > 0
}

func newReflectorPipeline(
	t *testing.T, db *sql.DB,
	prov providers.LLMProvider,
) *ReflectorPipeline {
	t.Helper()
	mem, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	atomGen := NewAtomIDGenerator(mem)
	tel := NewTelemetry(
		TelemetryConfig{}, mem, nil,
	)
	cfg := DefaultReflectorConfig()
	cfg.PromptFile = "" // use default prompt
	return NewReflectorPipeline(
		mem, atomGen, prov, tel, cfg,
	)
}

// --- Tests ---

func TestReflector_EmptyDB_SkipsLLM(t *testing.T) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	prov := &mockReflectorProvider{}
	rp := newReflectorPipeline(t, db, prov)

	err := rp.Run(context.Background(), ReflectorDaily)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if prov.calls != 0 {
		t.Errorf(
			"expected 0 LLM calls, got %d",
			prov.calls,
		)
	}
}

func TestReflector_Disabled_NoOp(t *testing.T) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	insertTestAtom(
		t, db, "S-1", "summary",
		"test", "positive", 0.5, nil,
	)

	prov := &mockReflectorProvider{}
	rp := newReflectorPipeline(t, db, prov)
	rp.cfg.Enabled = false

	err := rp.Run(context.Background(), ReflectorDaily)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if prov.calls != 0 {
		t.Errorf(
			"expected 0 LLM calls, got %d",
			prov.calls,
		)
	}
}

func TestReflector_HappyPath_MergeAndConfidence(
	t *testing.T,
) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	// Insert 3 atoms
	insertTestAtom(
		t, db, "S-1", "summary",
		"Port 8081 is used", "positive", 0.5,
		[]string{"config"},
	)
	insertTestAtom(
		t, db, "S-2", "summary",
		"Port 8081 configured", "positive", 0.6,
		[]string{"config", "deploy"},
	)
	insertTestAtom(
		t, db, "D-1", "decision",
		"Use port 8081", "positive", 0.5,
		[]string{"config"},
	)

	initialCount := countAtoms(t, db)
	if initialCount != 3 {
		t.Fatalf(
			"expected 3 atoms, got %d",
			initialCount,
		)
	}

	// Mock LLM response
	llmResp := `{
		"merges": [{
			"source_atom_ids": ["S-1", "S-2"],
			"summary": "Port 8081 is used and configured",
			"detail": "Merged from two similar atoms",
			"category": "summary",
			"polarity": "positive",
			"reason": "semantic overlap"
		}],
		"patterns": [],
		"confidence_updates": [{
			"atom_id": "D-1",
			"delta": 0.1,
			"reason": "confirmed by merge evidence"
		}],
		"runbook_drafts": []
	}`

	prov := &mockReflectorProvider{response: llmResp}
	rp := newReflectorPipeline(t, db, prov)

	err := rp.Run(context.Background(), ReflectorDaily)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// S-1 and S-2 should be deleted, merged atom added
	if atomExists(t, db, "S-1") {
		t.Error("S-1 should be deleted after merge")
	}
	if atomExists(t, db, "S-2") {
		t.Error("S-2 should be deleted after merge")
	}

	// Should have 2 atoms: merged + D-1
	finalCount := countAtoms(t, db)
	if finalCount != 2 {
		t.Errorf(
			"expected 2 atoms after merge, got %d",
			finalCount,
		)
	}

	// D-1 confidence should be 0.6
	conf := getAtomConfidence(t, db, "D-1")
	if conf < 0.59 || conf > 0.61 {
		t.Errorf(
			"D-1 confidence = %.2f, want ~0.6",
			conf,
		)
	}

	// LLM called exactly once
	if prov.calls != 1 {
		t.Errorf(
			"expected 1 LLM call, got %d",
			prov.calls,
		)
	}
}

func TestReflector_PatternInsertion(t *testing.T) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	insertTestAtom(
		t, db, "S-10", "summary",
		"Deploy failed", "negative", 0.5,
		[]string{"deploy"},
	)
	insertTestAtom(
		t, db, "S-11", "summary",
		"Deploy timeout", "negative", 0.5,
		[]string{"deploy"},
	)
	insertTestAtom(
		t, db, "S-12", "summary",
		"Deploy OOM", "negative", 0.5,
		[]string{"deploy"},
	)

	llmResp := `{
		"merges": [],
		"patterns": [{
			"type": "antipattern",
			"summary": "Recurring deploy failures",
			"tags": ["deploy"],
			"polarity": "negative",
			"source_atoms": ["S-10", "S-11", "S-12"]
		}],
		"confidence_updates": [],
		"runbook_drafts": []
	}`

	prov := &mockReflectorProvider{response: llmResp}
	rp := newReflectorPipeline(t, db, prov)

	err := rp.Run(context.Background(), ReflectorDaily)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Should have 4 atoms: 3 original + 1 pattern
	if countAtoms(t, db) != 4 {
		t.Errorf(
			"expected 4 atoms, got %d",
			countAtoms(t, db),
		)
	}

	// Check pattern exists with category 'pattern'
	var cat string
	err = db.QueryRow(
		`SELECT category FROM knowledge_atoms
		WHERE atom_id LIKE 'P-%'`,
	).Scan(&cat)
	if err != nil {
		t.Fatalf("query pattern: %v", err)
	}
	if cat != "pattern" {
		t.Errorf("pattern category = %q, want 'pattern'", cat)
	}
}

func TestReflector_RunbookDraft(t *testing.T) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	insertTestAtom(
		t, db, "S-20", "summary",
		"Grafana crash", "negative", 0.5,
		[]string{"grafana"},
	)

	llmResp := `{
		"merges": [],
		"patterns": [],
		"confidence_updates": [],
		"runbook_drafts": [{
			"tag": "grafana-crash",
			"steps": ["Check logs", "Restart service"],
			"trigger": "3+ restart failures",
			"rollback": "docker compose down && up"
		}]
	}`

	prov := &mockReflectorProvider{response: llmResp}
	rp := newReflectorPipeline(t, db, prov)

	err := rp.Run(context.Background(), ReflectorDaily)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Should have 2 atoms: original + runbook_draft
	if countAtoms(t, db) != 2 {
		t.Errorf(
			"expected 2 atoms, got %d",
			countAtoms(t, db),
		)
	}

	// Verify runbook_draft atom
	var summary, cat string
	err = db.QueryRow(
		`SELECT category, summary
		FROM knowledge_atoms
		WHERE atom_id LIKE 'R-%'`,
	).Scan(&cat, &summary)
	if err != nil {
		t.Fatalf("query runbook: %v", err)
	}
	if cat != "runbook_draft" {
		t.Errorf(
			"runbook category = %q, want 'runbook_draft'",
			cat,
		)
	}
}

func TestReflector_ConfidenceClamp(t *testing.T) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	insertTestAtom(
		t, db, "S-30", "summary",
		"Near max", "positive", 0.95,
		nil,
	)
	insertTestAtom(
		t, db, "S-31", "summary",
		"Near min", "negative", 0.05,
		nil,
	)

	llmResp := `{
		"merges": [],
		"patterns": [],
		"confidence_updates": [
			{"atom_id": "S-30", "delta": 0.1,
			 "reason": "confirmed"},
			{"atom_id": "S-31", "delta": -0.2,
			 "reason": "contradicted"}
		],
		"runbook_drafts": []
	}`

	prov := &mockReflectorProvider{response: llmResp}
	rp := newReflectorPipeline(t, db, prov)

	err := rp.Run(context.Background(), ReflectorDaily)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// S-30: 0.95 + 0.1 = clamped to 1.0
	c1 := getAtomConfidence(t, db, "S-30")
	if c1 != 1.0 {
		t.Errorf("S-30 confidence = %.2f, want 1.0", c1)
	}

	// S-31: 0.05 - 0.2 = clamped to 0.0
	c2 := getAtomConfidence(t, db, "S-31")
	if c2 != 0.0 {
		t.Errorf("S-31 confidence = %.2f, want 0.0", c2)
	}
}

func TestReflector_InvalidJSON_Retry(t *testing.T) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	insertTestAtom(
		t, db, "S-40", "summary",
		"test", "positive", 0.5, nil,
	)

	callCount := 0
	prov := &mockReflectorProvider{}
	// First call returns invalid, second returns valid
	origChat := prov.Chat
	_ = origChat

	// Use a custom provider that returns invalid then valid
	customProv := &sequentialProvider{
		responses: []string{
			"not valid json at all",
			`{"merges":[], "patterns":[], "confidence_updates":[], "runbook_drafts":[]}`,
		},
		callCount: &callCount,
	}

	rp := newReflectorPipeline(t, db, customProv)

	err := rp.Run(context.Background(), ReflectorDaily)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Should have called LLM twice (1 fail + 1 retry)
	if callCount != 2 {
		t.Errorf(
			"expected 2 LLM calls, got %d",
			callCount,
		)
	}
}

type sequentialProvider struct {
	responses []string
	callCount *int
}

func (s *sequentialProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	idx := *s.callCount
	*s.callCount++
	if idx < len(s.responses) {
		return &providers.LLMResponse{
			Content: s.responses[idx],
		}, nil
	}
	return nil, fmt.Errorf("no more responses")
}

func (s *sequentialProvider) GetDefaultModel() string {
	return "test"
}

func TestReflector_LLMError_ReportsFailure(
	t *testing.T,
) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	insertTestAtom(
		t, db, "S-50", "summary",
		"test", "positive", 0.5, nil,
	)

	prov := &mockReflectorProvider{
		err: fmt.Errorf("LLM unavailable"),
	}
	rp := newReflectorPipeline(t, db, prov)

	err := rp.Run(context.Background(), ReflectorDaily)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if prov.calls != 2 {
		// 1 original + 1 retry
		t.Errorf(
			"expected 2 LLM calls (with retry), got %d",
			prov.calls,
		)
	}
}

func TestReflector_MergePolarityMismatch_Skipped(
	t *testing.T,
) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	insertTestAtom(
		t, db, "S-60", "summary",
		"Good thing", "positive", 0.5,
		[]string{"test"},
	)
	insertTestAtom(
		t, db, "S-61", "summary",
		"Bad thing", "negative", 0.5,
		[]string{"test"},
	)

	// LLM tries to merge positive+negative (bad)
	llmResp := `{
		"merges": [{
			"source_atom_ids": ["S-60", "S-61"],
			"summary": "Merged",
			"category": "summary",
			"polarity": "positive",
			"reason": "overlap"
		}],
		"patterns": [],
		"confidence_updates": [],
		"runbook_drafts": []
	}`

	prov := &mockReflectorProvider{response: llmResp}
	rp := newReflectorPipeline(t, db, prov)

	err := rp.Run(context.Background(), ReflectorDaily)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// Both atoms should still exist (merge was skipped)
	if !atomExists(t, db, "S-60") {
		t.Error("S-60 should still exist")
	}
	if !atomExists(t, db, "S-61") {
		t.Error("S-61 should still exist")
	}
	if countAtoms(t, db) != 2 {
		t.Errorf(
			"expected 2 atoms, got %d",
			countAtoms(t, db),
		)
	}
}

func TestReflector_InvalidAtomID_Skipped(
	t *testing.T,
) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	insertTestAtom(
		t, db, "S-70", "summary",
		"test", "positive", 0.5, nil,
	)

	// LLM references non-existent atom
	llmResp := `{
		"merges": [],
		"patterns": [],
		"confidence_updates": [{
			"atom_id": "S-999",
			"delta": 0.1,
			"reason": "hallucinated"
		}],
		"runbook_drafts": []
	}`

	prov := &mockReflectorProvider{response: llmResp}
	rp := newReflectorPipeline(t, db, prov)

	err := rp.Run(context.Background(), ReflectorDaily)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// S-70 confidence unchanged (update for S-999 skipped)
	conf := getAtomConfidence(t, db, "S-70")
	if conf != 0.5 {
		t.Errorf(
			"S-70 confidence = %.2f, want 0.5",
			conf,
		)
	}
}

func TestReflector_WeeklyScope(t *testing.T) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	// Insert atoms with different timestamps
	// Recent atom (within 7 days)
	insertTestAtom(
		t, db, "S-80", "summary",
		"Recent atom", "positive", 0.5,
		nil,
	)

	llmResp := `{
		"merges": [],
		"patterns": [],
		"confidence_updates": [],
		"runbook_drafts": []
	}`

	prov := &mockReflectorProvider{response: llmResp}
	rp := newReflectorPipeline(t, db, prov)

	err := rp.Run(context.Background(), ReflectorWeekly)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	// LLM should be called (atom within scope)
	if prov.calls != 1 {
		t.Errorf(
			"expected 1 LLM call, got %d",
			prov.calls,
		)
	}
}

func TestReflector_MonthlyScope(t *testing.T) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	insertTestAtom(
		t, db, "S-90", "summary",
		"Old atom", "positive", 0.5,
		nil,
	)

	llmResp := `{
		"merges": [],
		"patterns": [],
		"confidence_updates": [],
		"runbook_drafts": []
	}`

	prov := &mockReflectorProvider{response: llmResp}
	rp := newReflectorPipeline(t, db, prov)

	err := rp.Run(context.Background(), ReflectorMonthly)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if prov.calls != 1 {
		t.Errorf(
			"expected 1 LLM call, got %d",
			prov.calls,
		)
	}
}

func TestReflector_ParseOutput_EmptyArrays(
	t *testing.T,
) {
	raw := `{"merges":[], "patterns":[], "confidence_updates":[], "runbook_drafts":[]}`
	parsed, err := parseReflectorOutput(raw)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(parsed.Merges) != 0 {
		t.Error("expected 0 merges")
	}
	if len(parsed.Patterns) != 0 {
		t.Error("expected 0 patterns")
	}
	if len(parsed.ConfidenceUpdates) != 0 {
		t.Error("expected 0 confidence_updates")
	}
	if len(parsed.RunbookDrafts) != 0 {
		t.Error("expected 0 runbook_drafts")
	}
}

func TestReflector_ParseOutput_NoJSON(t *testing.T) {
	_, err := parseReflectorOutput(
		"no json here at all",
	)
	if err == nil {
		t.Error("expected error for no JSON")
	}
}

func TestReflector_ClampConfidence(t *testing.T) {
	tests := []struct {
		input float64
		want  float64
	}{
		{1.5, 1.0},
		{-0.5, 0.0},
		{0.5, 0.5},
		{0.0, 0.0},
		{1.0, 1.0},
	}
	for _, tc := range tests {
		got := clampConfidence(tc.input)
		if got != tc.want {
			t.Errorf(
				"clampConfidence(%.1f) = %.1f, want %.1f",
				tc.input, got, tc.want,
			)
		}
	}
}

func TestReflector_DefaultConfig(t *testing.T) {
	cfg := DefaultReflectorConfig()
	if !cfg.Enabled {
		t.Error("should be enabled by default")
	}
	if cfg.Model != "background" {
		t.Errorf(
			"model = %q, want 'background'",
			cfg.Model,
		)
	}
	if cfg.MaxRetries != 1 {
		t.Errorf(
			"MaxRetries = %d, want 1",
			cfg.MaxRetries,
		)
	}
	if cfg.Schedule.Daily != "03:00" {
		t.Errorf(
			"Daily = %q, want '03:00'",
			cfg.Schedule.Daily,
		)
	}
}

func TestReflector_BuildUserContent(t *testing.T) {
	db := setupReflectorTestDB(t)
	defer db.Close()

	prov := &mockReflectorProvider{}
	rp := newReflectorPipeline(t, db, prov)

	atoms := []KnowledgeAtomRow{
		{
			AtomID:   "S-1",
			Category: "summary",
			Summary:  "Test summary",
			Polarity: "positive",
			Confidence: 0.5,
			CreatedAt: time.Date(
				2026, 5, 1, 10, 0, 0, 0,
				time.UTC,
			),
		},
	}

	content := rp.buildUserContent(
		atoms, ReflectorDaily,
	)

	if !contains(content, "\"scope\": \"daily\"") {
		t.Error("expected scope in content")
	}
	if !contains(content, "S-1") {
		t.Error("expected atom ID in content")
	}
	if !contains(content, "Test summary") {
		t.Error("expected summary in content")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) &&
		containsStr(s, sub)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
