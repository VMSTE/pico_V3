// PIKA-V3: reflector_test.go — Tests for Reflector pipeline.

package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/providers"

	_ "modernc.org/sqlite"
)

// --- Mock LLM Provider ---

type mockReflectorLLM struct {
	response string
	err      error
}

func (m *mockReflectorLLM) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &providers.LLMResponse{
		Content: m.response,
	}, nil
}

func (m *mockReflectorLLM) GetDefaultModel() string {
	return "mock"
}

// --- Test Helpers ---

func setupReflectorTestDB(
	t *testing.T,
) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertTestAtom(
	t *testing.T,
	db *sql.DB,
	atomID, category, summary, polarity string,
	conf float64,
	tags []string,
) {
	t.Helper()
	var tagsJSON []byte
	if len(tags) > 0 {
		tagsJSON, _ = json.Marshal(tags)
	}
	_, err := db.Exec(
		`INSERT INTO knowledge_atoms
		(atom_id, session_id, turn_id, category,
		 summary, confidence, polarity, tags)
		VALUES (?,?,?,?,?,?,?,?)`,
		atomID, "test-session", 1,
		category, summary, conf, polarity,
		string(tagsJSON))
	if err != nil {
		t.Fatalf("insert atom %s: %v", atomID, err)
	}
}

// --- Tests ---

func TestReflector_EmptyKnowledge(t *testing.T) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()

	mock := &mockReflectorLLM{response: "{}"}
	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), mock, nil,
		DefaultReflectorConfig(),
	)

	// Empty knowledge_atoms → skip, no error
	err = pipeline.Run(
		context.Background(), ReflectorDaily,
	)
	if err != nil {
		t.Fatalf("Run with empty DB: %v", err)
	}
}

func TestReflector_ParseValidJSON(t *testing.T) {
	input := `{
		"duplicates": [],
		"patterns": [],
		"confidence_updates": [],
		"runbook_drafts": []
	}`
	resp, err := parseReflectorOutput(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp == nil {
		t.Fatal("resp is nil")
	}
	if len(resp.Duplicates) != 0 {
		t.Errorf("expected 0 duplicates")
	}
}

func TestReflector_ParseInvalidJSON(t *testing.T) {
	_, err := parseReflectorOutput("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestReflector_ValidateHallucinatedID(t *testing.T) {
	resp := &reflectorLLMResponse{
		Duplicates: []reflectorDuplicate{
			{KeepID: "FAKE-1", MergeIDs: []string{"S-1"}},
		},
	}
	valid := map[string]bool{"S-1": true}
	err := validateReflectorOutput(resp, valid)
	if err == nil {
		t.Fatal("expected error for hallucinated ID")
	}
}

func TestReflector_ConfidenceClamp(t *testing.T) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()

	insertTestAtom(
		t, db, "D-5", "decision",
		"test decision", "positive", 0.5, nil,
	)

	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), nil, nil,
		DefaultReflectorConfig(),
	)

	atoms, _ := pipeline.fetchAtoms(
		context.Background(), ReflectorMonthly,
	)
	atomMap := map[string]*KnowledgeAtomRow{}
	for i := range atoms {
		atomMap[atoms[i].AtomID] = &atoms[i]
	}

	// Test +0.1 confirmed
	err = pipeline.applyConfUpdate(
		context.Background(),
		reflectorConfUpdate{
			AtomID:  "D-5",
			NewConf: 0.6,
		},
		atomMap,
	)
	if err != nil {
		t.Fatalf("apply conf +0.1: %v", err)
	}

	// Verify
	var conf float64
	db.QueryRow(
		`SELECT confidence FROM knowledge_atoms
		WHERE atom_id=?`, "D-5",
	).Scan(&conf)
	if conf != 0.6 {
		t.Errorf("expected 0.6, got %f", conf)
	}

	// Test clamp > 1.0
	err = pipeline.applyConfUpdate(
		context.Background(),
		reflectorConfUpdate{
			AtomID:  "D-5",
			NewConf: 1.5,
		},
		atomMap,
	)
	if err != nil {
		t.Fatalf("apply conf >1: %v", err)
	}
	db.QueryRow(
		`SELECT confidence FROM knowledge_atoms
		WHERE atom_id=?`, "D-5",
	).Scan(&conf)
	if conf != 1.0 {
		t.Errorf("expected clamp to 1.0, got %f", conf)
	}
}

func TestReflector_MergePolarityMismatch(
	t *testing.T,
) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()

	insertTestAtom(
		t, db, "S-1", "summary",
		"positive atom", "positive", 0.5, nil,
	)
	insertTestAtom(
		t, db, "S-2", "summary",
		"negative atom", "negative", 0.5, nil,
	)

	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), nil, nil,
		DefaultReflectorConfig(),
	)

	atoms, _ := pipeline.fetchAtoms(
		context.Background(), ReflectorMonthly,
	)
	atomMap := map[string]*KnowledgeAtomRow{}
	for i := range atoms {
		atomMap[atoms[i].AtomID] = &atoms[i]
	}

	// Merge positive + negative → should skip
	err = pipeline.applyMerge(
		context.Background(),
		reflectorDuplicate{
			KeepID:   "S-1",
			MergeIDs: []string{"S-2"},
		},
		atomMap,
	)
	if err != nil {
		t.Fatalf("merge should not error: %v", err)
	}

	// Both atoms should still exist
	var count int
	db.QueryRow(
		`SELECT COUNT(*) FROM knowledge_atoms`,
	).Scan(&count)
	if count != 2 {
		t.Errorf(
			"expected 2 atoms (merge skipped), got %d",
			count,
		)
	}
}

func TestReflector_MergeSuccess(t *testing.T) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()

	insertTestAtom(
		t, db, "S-10", "summary",
		"atom A", "positive", 0.6,
		[]string{"deploy"},
	)
	insertTestAtom(
		t, db, "S-11", "summary",
		"atom B", "positive", 0.4,
		[]string{"docker"},
	)

	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), nil, nil,
		DefaultReflectorConfig(),
	)

	atoms, _ := pipeline.fetchAtoms(
		context.Background(), ReflectorMonthly,
	)
	atomMap := map[string]*KnowledgeAtomRow{}
	for i := range atoms {
		atomMap[atoms[i].AtomID] = &atoms[i]
	}

	err = pipeline.applyMerge(
		context.Background(),
		reflectorDuplicate{
			KeepID:   "S-10",
			MergeIDs: []string{"S-11"},
			Reason:   "semantic overlap",
		},
		atomMap,
	)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	// Originals deleted, 1 new merged
	var count int
	db.QueryRow(
		`SELECT COUNT(*) FROM knowledge_atoms`,
	).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 merged atom, got %d", count)
	}

	// Check merged confidence = AVG(0.6, 0.4) = 0.5
	var conf float64
	db.QueryRow(
		`SELECT confidence FROM knowledge_atoms
		LIMIT 1`,
	).Scan(&conf)
	if conf != 0.5 {
		t.Errorf(
			"expected merged confidence 0.5, got %f",
			conf,
		)
	}
}

func TestReflector_RunbookDraft(t *testing.T) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()

	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), nil, nil,
		DefaultReflectorConfig(),
	)

	err = pipeline.applyRunbook(
		context.Background(),
		reflectorRunbook{
			Trigger:         "Deploy fails after config",
			Tags:            []string{"deploy", "config"},
			EvidenceAtomIDs: []string{"S-1", "S-2", "S-3"},
			Steps:           []string{"1. Check logs"},
			Rollback:        "Revert config",
		},
	)
	if err != nil {
		t.Fatalf("runbook: %v", err)
	}

	var count int
	db.QueryRow(
		`SELECT COUNT(*) FROM knowledge_atoms
		WHERE category='runbook_draft'`,
	).Scan(&count)
	if count != 1 {
		t.Errorf(
			"expected 1 runbook_draft atom, got %d",
			count,
		)
	}
}

func TestReflector_DailyPipeline(t *testing.T) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()

	insertTestAtom(
		t, db, "S-1", "summary",
		"deploy worked", "positive", 0.5,
		[]string{"deploy"},
	)
	insertTestAtom(
		t, db, "S-2", "summary",
		"deploy worked again", "positive", 0.5,
		[]string{"deploy"},
	)

	mockResp := `{
		"duplicates": [{
			"keep_id": "S-1",
			"merge_ids": ["S-2"],
			"reason": "same deploy topic"
		}],
		"patterns": [],
		"confidence_updates": [],
		"runbook_drafts": []
	}`

	mock := &mockReflectorLLM{response: mockResp}
	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), mock, nil,
		ReflectorConfig{
			Enabled:    true,
			MaxRetries: 1,
			Model:      "mock",
		},
	)

	err = pipeline.Run(
		context.Background(), ReflectorDaily,
	)
	if err != nil {
		t.Fatalf("Run daily: %v", err)
	}

	// After merge: 1 atom remains
	var count int
	db.QueryRow(
		`SELECT COUNT(*) FROM knowledge_atoms`,
	).Scan(&count)
	if count != 1 {
		t.Errorf(
			"expected 1 atom after merge, got %d",
			count,
		)
	}
}

func TestReflector_PromptHotReload(t *testing.T) {
	tmpDir := t.TempDir()
	promptPath := filepath.Join(
		tmpDir, "reflexor.md",
	)
	err := os.WriteFile(
		promptPath, []byte("custom prompt"), 0o644,
	)
	if err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	pipeline := &ReflectorPipeline{
		cfg: ReflectorConfig{PromptFile: promptPath},
	}

	text, err := pipeline.loadPromptFile()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if text != "custom prompt" {
		t.Errorf(
			"expected 'custom prompt', got %q", text,
		)
	}

	// Update file → next load sees new content
	os.WriteFile(
		promptPath, []byte("updated"), 0o644,
	)
	text, _ = pipeline.loadPromptFile()
	if text != "updated" {
		t.Errorf("hot-reload failed: %q", text)
	}
}

func TestReflector_PromptFileMissing(t *testing.T) {
	pipeline := &ReflectorPipeline{
		cfg: ReflectorConfig{
			PromptFile: "/nonexistent/path.md",
		},
	}
	text, err := pipeline.loadPromptFile()
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if text != defaultReflectorPrompt {
		t.Error("expected default prompt fallback")
	}
}

// --- Cron Tests ---

func TestScheduleToCronExpr(t *testing.T) {
	tests := []struct {
		raw, mode, want string
		wantErr         bool
	}{
		{"03:00", ReflectorDaily, "0 3 * * *", false},
		{"Sun 04:00", ReflectorWeekly,
			"0 4 * * 0", false},
		{"1st 05:00", ReflectorMonthly,
			"0 5 1 * *", false},
		{"Mon 08:30", ReflectorWeekly,
			"30 8 * * 1", false},
		{"15th 12:00", ReflectorMonthly,
			"0 12 15 * *", false},
		{"bad", ReflectorDaily, "", true},
		{"", ReflectorDaily, "", true},
	}
	for _, tc := range tests {
		got, err := schedToCronExpr(tc.raw, tc.mode)
		if tc.wantErr {
			if err == nil {
				t.Errorf(
					"schedToCronExpr(%q, %q): "+
						"expected error",
					tc.raw, tc.mode,
				)
			}
			continue
		}
		if err != nil {
			t.Errorf(
				"schedToCronExpr(%q, %q): %v",
				tc.raw, tc.mode, err,
			)
			continue
		}
		if got != tc.want {
			t.Errorf(
				"schedToCronExpr(%q, %q) = %q, want %q",
				tc.raw, tc.mode, got, tc.want,
			)
		}
	}
}

func TestRegisterReflectorJobs_Valid(t *testing.T) {
	tmpFile := filepath.Join(
		t.TempDir(), "cron.json",
	)
	cronSvc := cron.NewCronService(tmpFile, nil)

	err := RegisterReflectorJobs(
		cronSvc, nil,
		ReflectorSchedule{
			Daily:   "03:00",
			Weekly:  "Sun 04:00",
			Monthly: "1st 05:00",
		},
	)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	jobs := cronSvc.ListJobs(true)
	if len(jobs) != 3 {
		t.Errorf(
			"expected 3 jobs, got %d", len(jobs),
		)
	}
}

func TestRegisterReflectorJobs_Empty(t *testing.T) {
	tmpFile := filepath.Join(
		t.TempDir(), "cron.json",
	)
	cronSvc := cron.NewCronService(tmpFile, nil)

	err := RegisterReflectorJobs(
		cronSvc, nil,
		ReflectorSchedule{},
	)
	if err != nil {
		t.Fatalf("register empty: %v", err)
	}

	jobs := cronSvc.ListJobs(true)
	if len(jobs) != 0 {
		t.Errorf(
			"expected 0 jobs, got %d", len(jobs),
		)
	}
}

func TestRegisterReflectorJobs_Invalid(
	t *testing.T,
) {
	tmpFile := filepath.Join(
		t.TempDir(), "cron.json",
	)
	cronSvc := cron.NewCronService(tmpFile, nil)

	err := RegisterReflectorJobs(
		cronSvc, nil,
		ReflectorSchedule{Daily: "bad"},
	)
	if err == nil {
		t.Fatal("expected error for invalid schedule")
	}
}

func TestHandleReflectorJob(t *testing.T) {
	db := setupReflectorTestDB(t)
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	defer bm.Close()

	mock := &mockReflectorLLM{response: `{
		"duplicates":[],"patterns":[],
		"confidence_updates":[],"runbook_drafts":[]}`}
	pipeline := NewReflectorPipeline(
		bm, NewAtomIDGenerator(bm), mock, nil,
		DefaultReflectorConfig(),
	)

	// Reflector job
	job := &cron.CronJob{
		Payload: cron.CronPayload{
			Message: "reflector:daily",
		},
	}
	handled, err := HandleReflectorJob(pipeline, job)
	if !handled {
		t.Error("expected job to be handled")
	}
	if err != nil {
		t.Errorf("handle error: %v", err)
	}

	// Non-reflector job
	job2 := &cron.CronJob{
		Payload: cron.CronPayload{
			Message: "some-other-job",
		},
	}
	handled2, _ := HandleReflectorJob(pipeline, job2)
	if handled2 {
		t.Error("should not handle non-reflector job")
	}
}
