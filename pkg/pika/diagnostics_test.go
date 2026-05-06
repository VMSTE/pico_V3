// PIKA-V3: diagnostics_test.go — Tests for Diagnostics Engine (ТЗ-v2-7a)
package pika

import (
	"context"
	"fmt"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock TelegramSender
// ---------------------------------------------------------------------------

type mockDiagSender struct {
	mu       sync.Mutex
	messages []string
}

func (m *mockDiagSender) SendMessage(ctx context.Context, text string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, text)
	return "msg-1", nil
}

func (m *mockDiagSender) EditMessage(ctx context.Context, messageID string, text string) error {
	return nil
}

func (m *mockDiagSender) DeleteMessage(ctx context.Context, messageID string) error {
	return nil
}

func (m *mockDiagSender) SendConfirmation(ctx context.Context, text string) (bool, error) {
	return true, nil
}

func (m *mockDiagSender) sentMessages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(m.messages))
	copy(cp, m.messages)
	return cp
}

// ---------------------------------------------------------------------------
// Helper: create DiagnosticsEngine with test DB
// ---------------------------------------------------------------------------

func newTestDiagnostics(t *testing.T, sender TelegramSender, promptPaths map[string]string) (*DiagnosticsEngine, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Migrate(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mem, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("botmemory: %v", err)
	}

	de := NewDiagnosticsEngine(mem, sender, promptPaths)
	cleanup := func() {
		mem.Close()
		db.Close()
	}
	return de, cleanup
}

// ---------------------------------------------------------------------------
// Test: Diagnose — error span found
// ---------------------------------------------------------------------------

func TestDiagnose_ErrorFound(t *testing.T) {
	de, cleanup := newTestDiagnostics(t, nil, nil)
	defer cleanup()
	ctx := context.Background()

	// Insert an error span.
	_, err := de.mem.db.ExecContext(ctx,
		`INSERT INTO trace_spans (span_id, trace_id, component, operation, started_at, status)
		 VALUES ('span-1', 'trace-100', 'archivist', 'build_prompt', datetime('now'), 'error')`)
	if err != nil {
		t.Fatalf("insert span: %v", err)
	}

	result := de.Diagnose(ctx, "trace-100")

	if result.Component != "archivist" {
		t.Errorf("component = %q, want archivist", result.Component)
	}
	if result.ErrorKind != "error" {
		t.Errorf("error_kind = %q, want error", result.ErrorKind)
	}
	if result.RootSpan == nil {
		t.Fatal("root_span is nil")
	}
	if result.RootSpan.SpanID != "span-1" {
		t.Errorf("span_id = %q, want span-1", result.RootSpan.SpanID)
	}
}

// ---------------------------------------------------------------------------
// Test: Diagnose — no error spans
// ---------------------------------------------------------------------------

func TestDiagnose_NoErrors(t *testing.T) {
	de, cleanup := newTestDiagnostics(t, nil, nil)
	defer cleanup()
	ctx := context.Background()

	result := de.Diagnose(ctx, "trace-nonexistent")

	if result.Component != "" {
		t.Errorf("component = %q, want empty", result.Component)
	}
	if result.RootSpan != nil {
		t.Error("root_span should be nil")
	}
}

// ---------------------------------------------------------------------------
// Test: Diagnose — 2+ similar errors → SuggestedCR
// ---------------------------------------------------------------------------

func TestDiagnose_SuggestedCR(t *testing.T) {
	de, cleanup := newTestDiagnostics(t, nil, nil)
	defer cleanup()
	ctx := context.Background()

	// Insert 3 error spans for archivist (same component, different traces).
	for i := 0; i < 3; i++ {
		_, err := de.mem.db.ExecContext(ctx,
			`INSERT INTO trace_spans (span_id, trace_id, component, operation, started_at, status)
			 VALUES (?, ?, 'archivist', 'build_prompt', datetime('now'), 'error')`,
			fmt.Sprintf("span-%d", i), fmt.Sprintf("trace-%d", i))
		if err != nil {
			t.Fatalf("insert span %d: %v", i, err)
		}
	}

	result := de.Diagnose(ctx, "trace-0")

	if result.SuggestedCR == nil {
		t.Fatal("suggested_cr should not be nil with 2+ similar errors")
	}
	if result.Component != "archivist" {
		t.Errorf("component = %q, want archivist", result.Component)
	}
}

// ---------------------------------------------------------------------------
// Test: CreateCR — valid component
// ---------------------------------------------------------------------------

func TestCreateCR_Valid(t *testing.T) {
	sender := &mockDiagSender{}
	de, cleanup := newTestDiagnostics(t, sender, nil)
	defer cleanup()
	ctx := context.Background()

	err := de.CreateCR(ctx, "archivist", "Если build_prompt timeout → retry с reduced limit")
	if err != nil {
		t.Fatalf("CreateCR: %v", err)
	}

	// Verify it was inserted.
	var count int
	de.mem.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM registry WHERE kind = 'correction_rule'`).Scan(&count)
	if count != 1 {
		t.Errorf("registry count = %d, want 1", count)
	}

	// Verify TG notification sent.
	msgs := sender.sentMessages()
	if len(msgs) != 1 {
		t.Errorf("sent %d messages, want 1", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// Test: CreateCR — invalid component
// ---------------------------------------------------------------------------

func TestCreateCR_InvalidComponent(t *testing.T) {
	de, cleanup := newTestDiagnostics(t, nil, nil)
	defer cleanup()
	ctx := context.Background()

	err := de.CreateCR(ctx, "nonexistent", "some rule")
	if err == nil {
		t.Fatal("expected error for invalid component")
	}
}

// ---------------------------------------------------------------------------
// Test: BuildSubagentPrompt — 0 CRs
// ---------------------------------------------------------------------------

func TestBuildSubagentPrompt_NoCRs(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "archivist.md")
	os.WriteFile(promptPath, []byte("You are Archivist."), 0o644)

	de, cleanup := newTestDiagnostics(t, nil, map[string]string{
		"archivist": promptPath,
	})
	defer cleanup()
	ctx := context.Background()

	prompt, err := de.BuildSubagentPrompt(ctx, "archivist")
	if err != nil {
		t.Fatalf("BuildSubagentPrompt: %v", err)
	}
	if prompt != "You are Archivist." {
		t.Errorf("prompt = %q, want base prompt only", prompt)
	}
}

// ---------------------------------------------------------------------------
// Test: BuildSubagentPrompt — 3 CRs appended
// ---------------------------------------------------------------------------

func TestBuildSubagentPrompt_WithCRs(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "archivist.md")
	os.WriteFile(promptPath, []byte("You are Archivist."), 0o644)

	de, cleanup := newTestDiagnostics(t, nil, map[string]string{
		"archivist": promptPath,
	})
	defer cleanup()
	ctx := context.Background()

	// Insert 3 CRs.
	for i := 0; i < 3; i++ {
		cr := CorrectionRule{
			Component:     "archivist",
			RuleText:      fmt.Sprintf("Rule %d: do thing %d", i, i),
			CreatedAt:     time.Now().UTC().Format(time.RFC3339),
			VerifiedCount: 0,
			Status:        "active",
		}
		data, _ := json.Marshal(cr)
		de.mem.db.ExecContext(ctx,
			`INSERT INTO registry (kind, key, summary, data, verified, tags)
			 VALUES ('correction_rule', ?, ?, ?, 0, '["archivist"]')`,
			fmt.Sprintf("cr-arch-%d", i), cr.RuleText, string(data))
	}

	prompt, err := de.BuildSubagentPrompt(ctx, "archivist")
	if err != nil {
		t.Fatalf("BuildSubagentPrompt: %v", err)
	}

	if !diagContains(prompt, "You are Archivist.") {
		t.Error("base prompt missing")
	}
	if !diagContains(prompt, "CORRECTION RULES") {
		t.Error("CR block missing")
	}
	if !diagContains(prompt, "Rule 0") || !diagContains(prompt, "Rule 1") || !diagContains(prompt, "Rule 2") {
		t.Error("not all 3 rules present")
	}
}

// ---------------------------------------------------------------------------
// Test: BuildSubagentPrompt — token overflow trims oldest
// ---------------------------------------------------------------------------

func TestBuildSubagentPrompt_TokenOverflow(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "archivist.md")
	os.WriteFile(promptPath, []byte("Base."), 0o644)

	de, cleanup := newTestDiagnostics(t, nil, map[string]string{
		"archivist": promptPath,
	})
	defer cleanup()
	ctx := context.Background()

	// Insert a CR with very long text (~2000+ chars → ~500+ tokens).
	longRule := make([]byte, 2100)
	for i := range longRule {
		longRule[i] = 'A'
	}
	cr := CorrectionRule{
		Component: "archivist",
		RuleText:  string(longRule),
		CreatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		Status:    "active",
	}
	data, _ := json.Marshal(cr)
	de.mem.db.ExecContext(ctx,
		`INSERT INTO registry (kind, key, summary, data, verified, tags)
		 VALUES ('correction_rule', 'cr-big', ?, ?, 0, '["archivist"]')`,
		cr.RuleText, string(data))

	// Insert a small CR (newer, will be first in DESC order).
	cr2 := CorrectionRule{
		Component: "archivist",
		RuleText:  "Small rule",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Status:    "active",
	}
	data2, _ := json.Marshal(cr2)
	de.mem.db.ExecContext(ctx,
		`INSERT INTO registry (kind, key, summary, data, verified, tags)
		 VALUES ('correction_rule', 'cr-small', ?, ?, 0, '["archivist"]')`,
		cr2.RuleText, string(data2))

	prompt, err := de.BuildSubagentPrompt(ctx, "archivist")
	if err != nil {
		t.Fatalf("BuildSubagentPrompt: %v", err)
	}

	// Small rule should be present (newest, fits budget).
	if !diagContains(prompt, "Small rule") {
		t.Error("small rule should be present")
	}
	// Big rule should be trimmed (would exceed 500 token budget).
	if diagContains(prompt, string(longRule)) {
		t.Error("big rule should be trimmed by token budget")
	}
}

// ---------------------------------------------------------------------------
// Test: BuildSubagentPrompt — missing file → error
// ---------------------------------------------------------------------------

func TestBuildSubagentPrompt_MissingFile(t *testing.T) {
	de, cleanup := newTestDiagnostics(t, nil, map[string]string{
		"archivist": "/nonexistent/path.md",
	})
	defer cleanup()
	ctx := context.Background()

	_, err := de.BuildSubagentPrompt(ctx, "archivist")
	if err == nil {
		t.Fatal("expected error for missing prompt file")
	}
}

// ---------------------------------------------------------------------------
// Test: IncrementVerified — count reaches threshold → verified
// ---------------------------------------------------------------------------

func TestIncrementVerified(t *testing.T) {
	de, cleanup := newTestDiagnostics(t, nil, nil)
	defer cleanup()
	ctx := context.Background()

	// Insert CR with verified_count = 4 (one more → verified).
	cr := CorrectionRule{
		Component:     "atomizer",
		RuleText:      "Check token count before atomization",
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		VerifiedCount: 4,
		Status:        "active",
	}
	data, _ := json.Marshal(cr)
	de.mem.db.ExecContext(ctx,
		`INSERT INTO registry (kind, key, summary, data, verified, tags)
		 VALUES ('correction_rule', 'cr-atom-1', ?, ?, 0, '["atomizer"]')`,
		cr.RuleText, string(data))

	err := de.IncrementVerified(ctx, "atomizer")
	if err != nil {
		t.Fatalf("IncrementVerified: %v", err)
	}

	// Read back and check status.
	var dataStr string
	de.mem.db.QueryRowContext(ctx,
		`SELECT data FROM registry WHERE key = 'cr-atom-1'`).Scan(&dataStr)

	var updated CorrectionRule
	json.Unmarshal([]byte(dataStr), &updated)

	if updated.VerifiedCount != 5 {
		t.Errorf("verified_count = %d, want 5", updated.VerifiedCount)
	}
	if updated.Status != "verified" {
		t.Errorf("status = %q, want verified", updated.Status)
	}
}

// ---------------------------------------------------------------------------
// Test: ReviewCRs — promote + deactivate
// ---------------------------------------------------------------------------

func TestReviewCRs(t *testing.T) {
	de, cleanup := newTestDiagnostics(t, nil, nil)
	defer cleanup()
	ctx := context.Background()

	// 1. Verified CR, 10 days old → should be promoted.
	cr1 := CorrectionRule{
		Component:     "archivist",
		RuleText:      "Old verified rule",
		CreatedAt:     time.Now().Add(-10 * 24 * time.Hour).UTC().Format(time.RFC3339),
		VerifiedCount: 5,
		Status:        "verified",
	}
	data1, _ := json.Marshal(cr1)
	de.mem.db.ExecContext(ctx,
		`INSERT INTO registry (kind, key, summary, data, verified, tags)
		 VALUES ('correction_rule', 'cr-promote', ?, ?, 0, '["archivist"]')`,
		cr1.RuleText, string(data1))

	// 2. Active CR, 35 days old, 0 verifications → should be deactivated.
	cr2 := CorrectionRule{
		Component:     "atomizer",
		RuleText:      "Stale unverified rule",
		CreatedAt:     time.Now().Add(-35 * 24 * time.Hour).UTC().Format(time.RFC3339),
		VerifiedCount: 0,
		Status:        "active",
	}
	data2, _ := json.Marshal(cr2)
	de.mem.db.ExecContext(ctx,
		`INSERT INTO registry (kind, key, summary, data, verified, tags)
		 VALUES ('correction_rule', 'cr-deactivate', ?, ?, 0, '["atomizer"]')`,
		cr2.RuleText, string(data2))

	actions := de.ReviewCRs(ctx)

	if len(actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(actions))
	}

	promoteFound := false
	deactivateFound := false
	for _, a := range actions {
		switch a.Action {
		case "promote":
			promoteFound = true
			if a.Key != "cr-promote" {
				t.Errorf("promote key = %q", a.Key)
			}
		case "deactivate":
			deactivateFound = true
			if a.Key != "cr-deactivate" {
				t.Errorf("deactivate key = %q", a.Key)
			}
		}
	}
	if !promoteFound {
		t.Error("promote action not found")
	}
	if !deactivateFound {
		t.Error("deactivate action not found")
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func diagContains(s, substr string) bool {
	return len(s) >= len(substr) && diagContainsStr(s, substr)
}

func diagContainsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
