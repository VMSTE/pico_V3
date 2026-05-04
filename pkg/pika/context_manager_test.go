package pika

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSystemPrompt_EmptyWorkspace(t *testing.T) {
	dir := t.TempDir()
	trail := NewTrail()
	meta := NewMeta()
	cm := NewPikaContextManager(dir, trail, meta, nil, nil)

	prompt, err := cm.BuildSystemPrompt(
		context.Background(), "test-session",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain TRAIL and META even without files
	if !strings.Contains(prompt, "TRAIL") {
		t.Error("expected TRAIL in prompt")
	}
	if !strings.Contains(prompt, "META") {
		t.Error("expected META in prompt")
	}
}

func TestBuildSystemPrompt_WithCoreAndContext(t *testing.T) {
	dir := t.TempDir()

	coreContent := "You are Pika, a personal AI assistant."
	ctxContent := "User prefers concise answers."
	_ = os.WriteFile(
		filepath.Join(dir, "CORE.md"),
		[]byte(coreContent), 0o644,
	)
	_ = os.WriteFile(
		filepath.Join(dir, "CONTEXT.md"),
		[]byte(ctxContent), 0o644,
	)

	trail := NewTrail()
	meta := NewMeta()
	cm := NewPikaContextManager(dir, trail, meta, nil, nil)

	prompt, err := cm.BuildSystemPrompt(
		context.Background(), "test-session",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(prompt, coreContent) {
		t.Errorf(
			"expected CORE.md content, got: %s", prompt,
		)
	}
	if !strings.Contains(prompt, ctxContent) {
		t.Errorf(
			"expected CONTEXT.md content, got: %s", prompt,
		)
	}
}

func TestBuildSystemPrompt_CachesFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(
		filepath.Join(dir, "CORE.md"),
		[]byte("version1"), 0o644,
	)

	cm := NewPikaContextManager(
		dir, NewTrail(), NewMeta(), nil, nil,
	)

	p1, err := cm.BuildSystemPrompt(
		context.Background(), "s1",
	)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !strings.Contains(p1, "version1") {
		t.Error("expected version1 in first prompt")
	}

	cm.mu.RLock()
	if cm.cachedCore != "version1" {
		t.Error("expected cachedCore to be set")
	}
	cm.mu.RUnlock()
}

func TestBuildSystemPrompt_TrailEntries(t *testing.T) {
	dir := t.TempDir()
	trail := NewTrail()
	trail.Add(TrailEntry{
		ToolName:   "compose",
		Operation:  "restart",
		Result:     "ok",
		OK:         true,
		DurationMs: 230,
	})

	meta := NewMeta()
	meta.IncrementMsgCount()
	meta.UpdateContextPct(50000, 256000)

	cm := NewPikaContextManager(dir, trail, meta, nil, nil)

	prompt, err := cm.BuildSystemPrompt(
		context.Background(), "s1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(prompt, "compose.restart") {
		t.Error("expected trail entry in prompt")
	}
	if !strings.Contains(prompt, "MSG_COUNT: 1") {
		t.Error("expected MSG_COUNT in prompt")
	}
}

func TestBuildSystemPrompt_DegradationBlock(t *testing.T) {
	dir := t.TempDir()
	trail := NewTrail()
	meta := NewMeta()

	dp := &mockDegradedProvider{
		state: SystemState{
			Status: "degraded",
			DegradedComponents: []string{
				"archivist", "mcp_guard",
			},
		},
	}

	cm := NewPikaContextManager(
		dir, trail, meta, dp, nil,
	)

	prompt, err := cm.BuildSystemPrompt(
		context.Background(), "s1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(prompt, "DEGRADATION") {
		t.Error("expected DEGRADATION block in prompt")
	}
	if !strings.Contains(prompt, "search_memory") {
		t.Error(
			"expected archivist degradation instruction",
		)
	}
	if !strings.Contains(prompt, "DO NOT call MCP") {
		t.Error(
			"expected mcp_guard degradation instruction",
		)
	}
}

func TestBuildSystemPrompt_HealthyNoDegradation(
	t *testing.T,
) {
	dir := t.TempDir()
	cm := NewPikaContextManager(
		dir, NewTrail(), NewMeta(), nil, nil,
	)

	prompt, err := cm.BuildSystemPrompt(
		context.Background(), "s1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(prompt, "DEGRADATION") {
		t.Error(
			"healthy system should NOT have DEGRADATION",
		)
	}
}

func TestCompact_NoOp(t *testing.T) {
	cm := NewPikaContextManager(
		t.TempDir(), NewTrail(), NewMeta(), nil, nil,
	)
	if err := cm.Compact("s1", "proactive"); err != nil {
		t.Fatalf("Compact should be no-op: %v", err)
	}
}

func TestIngest_NoOp(t *testing.T) {
	cm := NewPikaContextManager(
		t.TempDir(), NewTrail(), NewMeta(), nil, nil,
	)
	if err := cm.Ingest("s1"); err != nil {
		t.Fatalf("Ingest should be no-op: %v", err)
	}
}

func TestClear_NoOp(t *testing.T) {
	cm := NewPikaContextManager(
		t.TempDir(), NewTrail(), NewMeta(), nil, nil,
	)
	if err := cm.Clear("s1"); err != nil {
		t.Fatalf("Clear should be no-op: %v", err)
	}
}

func TestInvalidateCache(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(
		filepath.Join(dir, "CORE.md"),
		[]byte("test"), 0o644,
	)

	cm := NewPikaContextManager(
		dir, NewTrail(), NewMeta(), nil, nil,
	)

	// Populate cache
	_, _ = cm.BuildSystemPrompt(
		context.Background(), "s1",
	)

	cm.mu.RLock()
	if cm.cachedCore == "" {
		t.Fatal("cache should be populated")
	}
	cm.mu.RUnlock()

	cm.InvalidateCache()

	cm.mu.RLock()
	if cm.cachedCore != "" {
		t.Error("cache should be cleared")
	}
	cm.mu.RUnlock()
}

func TestAlwaysHealthyProvider(t *testing.T) {
	p := NewAlwaysHealthyProvider()
	s := p.GetSystemState()
	if s.Status != "healthy" {
		t.Errorf("expected healthy, got %q", s.Status)
	}
	if len(s.DegradedComponents) != 0 {
		t.Error("expected no degraded components")
	}
}

func TestNoopArchivistCaller(t *testing.T) {
	a := NewNoopArchivistCaller()
	result, err := a.BuildPrompt(
		context.Background(), ArchivistInput{SessionKey: "s1"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil && result.BriefText != "" {
		t.Errorf("expected empty brief, got %q", result.BriefText)
	}
}

// --- test helpers ---

type mockDegradedProvider struct {
	state SystemState
}

func (p *mockDegradedProvider) GetSystemState() SystemState {
	return p.state
}
