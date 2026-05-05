package pika

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// mockProgress records NotifyDegradation/NotifyRecovery calls.
type mockProgress struct {
	degradations []degradationEvent
	recoveries   []string
}

type degradationEvent struct {
	component string
	status    string
}

func (m *mockProgress) NotifyDegradation(
	component, status string,
) {
	m.degradations = append(
		m.degradations,
		degradationEvent{component, status},
	)
}

func (m *mockProgress) NotifyRecovery(component string) {
	m.recoveries = append(m.recoveries, component)
}

func setupTelemetryDB(t *testing.T) *BotMemory {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	t.Cleanup(func() {
		bm.Close()
		db.Close()
	})
	return bm
}

func defaultTelemetryCfg() TelemetryConfig {
	return TelemetryConfig{
		DailyBudgetUSD:       2.00,
		WindowSize:           5,
		ToolFailThresholdPct: 30,
		LatencyThresholdMs:   30000,
	}
}

func TestRecordLLMCall_InsertsAndUpdatesSpent(t *testing.T) {
	bm := setupTelemetryDB(t)
	tel := NewTelemetry(defaultTelemetryCfg(), bm, nil)

	tel.RecordLLMCall(context.Background(), RecordLLMParams{
		SessionID: "s1",
		Model:     "main",
		Direction: "chat",
		Component: "main",
		TokensIn:  100,
		TokensOut: 50,
		CostUSD:   0.50,
		LatencyMs: 200,
		Status:    "ok",
	})

	remaining := tel.GetBudgetRemaining()
	if remaining < 1.49 || remaining > 1.51 {
		t.Errorf("remaining = %f, want ~1.50", remaining)
	}
}

func TestCheckBudget_FreshDay(t *testing.T) {
	bm := setupTelemetryDB(t)
	tel := NewTelemetry(defaultTelemetryCfg(), bm, nil)

	allowed, remaining := tel.CheckBudget()
	if !allowed {
		t.Error("expected allowed=true for fresh day")
	}
	if remaining < 1.99 || remaining > 2.01 {
		t.Errorf("remaining = %f, want ~2.00", remaining)
	}
}

func TestCheckBudget_Exceeded(t *testing.T) {
	bm := setupTelemetryDB(t)
	tel := NewTelemetry(defaultTelemetryCfg(), bm, nil)

	ctx := context.Background()
	tel.RecordLLMCall(ctx, RecordLLMParams{
		SessionID: "s1",
		Model:     "main",
		Direction: "chat",
		Component: "main",
		CostUSD:   2.01,
	})

	allowed, _ := tel.CheckBudget()
	if allowed {
		t.Error("expected allowed=false when budget exceeded")
	}
}

func TestCheckBudget_NewDayReset(t *testing.T) {
	bm := setupTelemetryDB(t)
	tel := NewTelemetry(defaultTelemetryCfg(), bm, nil)

	// Simulate old day with spent budget
	tel.mu.Lock()
	tel.spentTodayUSD = 2.50
	tel.budgetDate = "2020-01-01"
	tel.mu.Unlock()

	// Should reset on new date check
	allowed, remaining := tel.CheckBudget()
	if !allowed {
		t.Error("expected allowed=true after day reset")
	}
	if remaining < 1.99 {
		t.Errorf(
			"remaining = %f, want ~2.00 after reset",
			remaining,
		)
	}
}

func TestRecordToolCall_HealthDegraded(t *testing.T) {
	mp := &mockProgress{}
	tel := NewTelemetry(defaultTelemetryCfg(), nil, mp)

	now := time.Now()
	// 3 ok + 2 error = 40% fail >= 30% threshold
	for i := 0; i < 3; i++ {
		tel.RecordToolCall(CallResult{
			ToolName:  "compose",
			Status:    "ok",
			LatencyMs: 100,
			Timestamp: now,
		})
	}
	for i := 0; i < 2; i++ {
		tel.RecordToolCall(CallResult{
			ToolName:  "compose",
			Status:    "error",
			LatencyMs: 100,
			Timestamp: now,
		})
	}

	state := tel.GetSystemState()
	if state.Status != "degraded" {
		t.Errorf("status = %q, want degraded", state.Status)
	}
	if len(mp.degradations) != 1 {
		t.Errorf(
			"degradations = %d, want 1",
			len(mp.degradations),
		)
	}
}

func TestRecordToolCall_HealthOK(t *testing.T) {
	tel := NewTelemetry(defaultTelemetryCfg(), nil, nil)

	now := time.Now()
	// 5 ok calls = 0% fail < 30% threshold
	for i := 0; i < 5; i++ {
		tel.RecordToolCall(CallResult{
			ToolName:  "compose",
			Status:    "ok",
			LatencyMs: 100,
			Timestamp: now,
		})
	}

	state := tel.GetSystemState()
	if state.Status != "healthy" {
		t.Errorf("status = %q, want healthy", state.Status)
	}
}

func TestRecordToolCall_NotEnoughData(t *testing.T) {
	tel := NewTelemetry(defaultTelemetryCfg(), nil, nil)

	// Only 3 calls (window=5) — should not evaluate
	now := time.Now()
	for i := 0; i < 3; i++ {
		tel.RecordToolCall(CallResult{
			ToolName:  "compose",
			Status:    "error",
			LatencyMs: 100,
			Timestamp: now,
		})
	}

	state := tel.GetSystemState()
	if state.Status != "healthy" {
		t.Errorf(
			"status = %q, want healthy (not enough data)",
			state.Status,
		)
	}
}

func TestReportComponentFailure_NotifyOnChange(t *testing.T) {
	mp := &mockProgress{}
	tel := NewTelemetry(defaultTelemetryCfg(), nil, mp)

	tel.ReportComponentFailure("archivist", "degraded")

	if len(mp.degradations) != 1 {
		t.Fatalf(
			"degradations = %d, want 1",
			len(mp.degradations),
		)
	}
	if mp.degradations[0].component != "archivist" {
		t.Errorf(
			"component = %q, want archivist",
			mp.degradations[0].component,
		)
	}
}

func TestReportComponentFailure_NoNotifyOnSameState(
	t *testing.T,
) {
	mp := &mockProgress{}
	tel := NewTelemetry(defaultTelemetryCfg(), nil, mp)

	tel.ReportComponentFailure("archivist", "degraded")
	tel.ReportComponentFailure("archivist", "degraded")

	if len(mp.degradations) != 1 {
		t.Errorf(
			"degradations = %d, want 1 (no dup)",
			len(mp.degradations),
		)
	}
}

func TestReportComponentSuccess_NotifyOnRecovery(
	t *testing.T,
) {
	mp := &mockProgress{}
	tel := NewTelemetry(defaultTelemetryCfg(), nil, mp)

	tel.ReportComponentFailure("archivist", "degraded")
	tel.ReportComponentSuccess("archivist")

	if len(mp.recoveries) != 1 {
		t.Fatalf(
			"recoveries = %d, want 1",
			len(mp.recoveries),
		)
	}
	if mp.recoveries[0] != "archivist" {
		t.Errorf(
			"component = %q, want archivist",
			mp.recoveries[0],
		)
	}
}

func TestReportComponentSuccess_NoNotifyIfAlreadyOK(
	t *testing.T,
) {
	mp := &mockProgress{}
	tel := NewTelemetry(defaultTelemetryCfg(), nil, mp)

	// Never degraded — recovery should not fire
	tel.ReportComponentSuccess("archivist")

	if len(mp.recoveries) != 0 {
		t.Errorf(
			"recoveries = %d, want 0",
			len(mp.recoveries),
		)
	}
}

func TestGetSystemState_Healthy(t *testing.T) {
	tel := NewTelemetry(defaultTelemetryCfg(), nil, nil)

	state := tel.GetSystemState()
	if state.Status != "healthy" {
		t.Errorf("status = %q, want healthy", state.Status)
	}
	if len(state.DegradedComponents) != 0 {
		t.Errorf(
			"degraded = %v, want empty",
			state.DegradedComponents,
		)
	}
}

func TestGetSystemState_AfterRecovery(t *testing.T) {
	tel := NewTelemetry(defaultTelemetryCfg(), nil, nil)

	tel.ReportComponentFailure("archivist", "degraded")
	tel.ReportComponentSuccess("archivist")

	state := tel.GetSystemState()
	if state.Status != "healthy" {
		t.Errorf(
			"status = %q, want healthy after recovery",
			state.Status,
		)
	}
}

func TestGetSystemState_Offline(t *testing.T) {
	tel := NewTelemetry(defaultTelemetryCfg(), nil, nil)

	tel.ReportComponentFailure("llm", "offline")

	state := tel.GetSystemState()
	if state.Status != "offline" {
		t.Errorf("status = %q, want offline", state.Status)
	}
}
