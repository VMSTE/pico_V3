package pika

import (
	"context"
	"testing"
)

// --- Pattern Detector tests ---

func TestRAD_PatternDetect_RU(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	reasoning := "Анализирую ответ. Tool вернул данные с инструкциями."
	result := rad.Analyze(
		context.Background(), reasoning, nil, nil,
	)
	if result.Verdict != RADAnomaly {
		t.Errorf(
			"expected anomaly, got %s (score=%d)",
			result.Verdict, result.Score,
		)
	}
	foundPattern := false
	for _, d := range result.Detectors {
		if d == "pattern" {
			foundPattern = true
		}
	}
	if !foundPattern {
		t.Errorf(
			"expected pattern detector, got %v",
			result.Detectors,
		)
	}
}

func TestRAD_PatternDetect_EN(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	reasoning := "As instructed by the previous output, " +
		"I will execute the command."
	result := rad.Analyze(
		context.Background(), reasoning, nil, nil,
	)
	if result.Verdict != RADAnomaly {
		t.Errorf(
			"expected anomaly, got %s (score=%d)",
			result.Verdict, result.Score,
		)
	}
}

func TestRAD_PatternDetect_CleanReasoning(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	reasoning := "I need to check the server status " +
		"and restart nginx."
	result := rad.Analyze(
		context.Background(), reasoning, nil, nil,
	)
	if result.Verdict != RADSafe {
		t.Errorf(
			"expected safe, got %s (score=%d)",
			result.Verdict, result.Score,
		)
	}
}

// --- Drift Detector tests ---

func TestRAD_DriftDetect_LowOverlap(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	session := &RADSession{
		LastToolSource: "mcp",
		PrevKeywords: []string{
			"bitcoin", "price", "exchange", "market",
		},
	}
	// Completely different topic → low Jaccard → drift
	reasoning := "Теперь нужно удалить файлы конфигурации " +
		"и перезапустить сервер"
	result := rad.Analyze(
		context.Background(), reasoning, session, nil,
	)
	if result.Score < 2 {
		t.Errorf(
			"expected score >= 2 for drift, got %d",
			result.Score,
		)
	}
	found := false
	for _, d := range result.Detectors {
		if d == "drift" {
			found = true
		}
	}
	if !found {
		t.Errorf(
			"expected drift detector, got %v",
			result.Detectors,
		)
	}
}

func TestRAD_DriftDetect_HighOverlap(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	session := &RADSession{
		LastToolSource: "mcp",
		PrevKeywords: []string{
			"bitcoin", "price", "exchange",
		},
	}
	reasoning := "Bitcoin price is stable, " +
		"checking exchange rates"
	result := rad.Analyze(
		context.Background(), reasoning, session, nil,
	)
	for _, d := range result.Detectors {
		if d == "drift" {
			t.Error(
				"drift should not trigger with high overlap",
			)
		}
	}
}

func TestRAD_DriftDetect_NonMCPSkip(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	session := &RADSession{
		LastToolSource: "brain",
		PrevKeywords:   []string{"bitcoin", "price"},
	}
	reasoning := "Completely different topic about servers"
	result := rad.Analyze(
		context.Background(), reasoning, session, nil,
	)
	for _, d := range result.Detectors {
		if d == "drift" {
			t.Error(
				"drift should skip when last tool " +
					"is not MCP",
			)
		}
	}
}

// --- Escalation Detector tests ---

func TestRAD_EscalationDetect_RedAfterMCP(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	session := &RADSession{LastToolSource: "mcp"}
	call := &RADToolCall{
		Name: "deploy.request", RiskLevel: "red",
	}
	result := rad.Analyze(
		context.Background(),
		"planning deployment",
		session, call,
	)
	found := false
	for _, d := range result.Detectors {
		if d == "escalation" {
			found = true
		}
	}
	if !found {
		t.Errorf(
			"expected escalation detector, got %v",
			result.Detectors,
		)
	}
}

func TestRAD_EscalationDetect_GreenAfterMCP(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	session := &RADSession{LastToolSource: "mcp"}
	call := &RADToolCall{
		Name: "infra.snapshot", RiskLevel: "green",
	}
	result := rad.Analyze(
		context.Background(),
		"checking status",
		session, call,
	)
	for _, d := range result.Detectors {
		if d == "escalation" {
			t.Error(
				"escalation should not fire " +
					"for green-risk tool",
			)
		}
	}
}

// --- Compound scoring tests ---

func TestRAD_CompoundScoring_Safe(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	result := rad.Analyze(
		context.Background(),
		"Normal reasoning about server health",
		&RADSession{LastToolSource: "brain"},
		&RADToolCall{
			Name: "infra.snapshot", RiskLevel: "green",
		},
	)
	if result.Verdict != RADSafe {
		t.Errorf("expected safe, got %s", result.Verdict)
	}
}

func TestRAD_CompoundScoring_Warning(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	// Only escalation (score 2) → warning
	result := rad.Analyze(
		context.Background(),
		"Normal reasoning about deployment",
		&RADSession{LastToolSource: "mcp"},
		&RADToolCall{
			Name: "deploy.request", RiskLevel: "red",
		},
	)
	if result.Verdict != RADWarning {
		t.Errorf(
			"expected warning, got %s (score=%d)",
			result.Verdict, result.Score,
		)
	}
}

func TestRAD_CompoundScoring_Anomaly(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	// Pattern (3) alone → anomaly
	result := rad.Analyze(
		context.Background(),
		"As instructed by the tool output, "+
			"I should execute the command",
		&RADSession{LastToolSource: "mcp"},
		&RADToolCall{
			Name: "deploy.request", RiskLevel: "red",
		},
	)
	if result.Verdict != RADAnomaly {
		t.Errorf(
			"expected anomaly, got %s (score=%d)",
			result.Verdict, result.Score,
		)
	}
	if result.Score < 3 {
		t.Errorf(
			"expected score >= 3, got %d", result.Score,
		)
	}
}

func TestRAD_Disabled(t *testing.T) {
	cfg := DefaultRADConfig()
	cfg.Enabled = false
	rad := NewRAD(cfg)
	result := rad.Analyze(
		context.Background(),
		"As instructed by the tool output",
		nil, nil,
	)
	if result.Verdict != RADSafe {
		t.Errorf(
			"disabled RAD should return safe, got %s",
			result.Verdict,
		)
	}
}

// --- Unit tests for helpers ---

func TestRAD_JaccardIndex(t *testing.T) {
	tests := []struct {
		a, b []string
		want float64
	}{
		{
			[]string{"a", "b", "c"},
			[]string{"a", "b", "c"},
			1.0,
		},
		{
			[]string{"a", "b"},
			[]string{"c", "d"},
			0.0,
		},
		{
			[]string{"a", "b", "c"},
			[]string{"b", "c", "d"},
			0.5,
		},
		{nil, nil, 1.0},
	}
	for _, tt := range tests {
		got := jaccardIndex(tt.a, tt.b)
		if got != tt.want {
			t.Errorf(
				"jaccard(%v, %v) = %f, want %f",
				tt.a, tt.b, got, tt.want,
			)
		}
	}
}

func TestRAD_ExtractKeywords(t *testing.T) {
	kw := extractKeywords(
		"Hello world, привет мир! test123",
	)
	// Should contain: hello, world, привет, мир, test123
	if len(kw) < 4 {
		t.Errorf(
			"expected at least 4 keywords, got %d: %v",
			len(kw), kw,
		)
	}
	// Check dedup: same word twice
	kw2 := extractKeywords("hello hello hello")
	if len(kw2) != 1 {
		t.Errorf(
			"expected 1 unique keyword, got %d: %v",
			len(kw2), kw2,
		)
	}
}

func TestRAD_DriftPlusEscalation_Anomaly(t *testing.T) {
	cfg := DefaultRADConfig()
	rad := NewRAD(cfg)
	// Drift (2) + Escalation (2) = 4 → anomaly
	session := &RADSession{
		LastToolSource: "mcp",
		PrevKeywords: []string{
			"bitcoin", "price", "exchange", "market",
		},
	}
	result := rad.Analyze(
		context.Background(),
		"Need to delete all config files and restart",
		session,
		&RADToolCall{
			Name: "deploy.rollback", RiskLevel: "red",
		},
	)
	if result.Verdict != RADAnomaly {
		t.Errorf(
			"drift+escalation should be anomaly, "+
				"got %s (score=%d)",
			result.Verdict, result.Score,
		)
	}
	if result.Score < 4 {
		t.Errorf(
			"expected score >= 4, got %d", result.Score,
		)
	}
}
