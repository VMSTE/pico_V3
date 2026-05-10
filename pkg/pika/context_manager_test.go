package pika

import (
	"context"
	"testing"
)

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
