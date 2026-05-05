// PIKA-V3: Telemetry — token/cost accounting, budget control,
// sliding window health, component degradation tracking.
// Direct calls from pipeline (D-136a, core infra).

package pika

import (
	"context"
	"sync"
	"time"
)

// ProgressNotifier sends degradation/recovery notifications
// to the user (e.g. via Telegram). Implemented by progress.go
// (ТЗ-4e). May be nil — Telemetry checks before calling.
type ProgressNotifier interface {
	NotifyDegradation(component, status string)
	NotifyRecovery(component string)
}

// CallResult represents the outcome of a single tool call
// for sliding window health evaluation.
type CallResult struct {
	ToolName  string
	LatencyMs int64
	Status    string // "ok", "error", "timeout"
	Timestamp time.Time
}

// RecordLLMParams holds parameters for recording an LLM call.
type RecordLLMParams struct {
	SessionID string
	Model     string
	Direction string
	Component string
	TokensIn  int
	TokensOut int
	CostUSD   float64
	LatencyMs int64
	Status    string
	Error     string
}

// TelemetryConfig holds configuration for the Telemetry subsystem.
// Populated from config.Health + ResolvedAgentConfig.Budget.
type TelemetryConfig struct {
	DailyBudgetUSD       float64
	WindowSize           int
	ToolFailThresholdPct int
	LatencyThresholdMs   int64
}

// Telemetry is the unified telemetry layer: token/cost accounting,
// budget control, sliding window health, component degradation.
// Thread-safe: all public methods use sync.Mutex.
// Implements SystemStateProvider for buildPrompt integration.
type Telemetry struct {
	mu       sync.Mutex
	botmem   *BotMemory
	progress ProgressNotifier

	// Budget
	dailyBudgetUSD float64
	spentTodayUSD  float64
	budgetDate     string // "2006-01-02"

	// Health — sliding window
	windowSize           int
	toolFailThresholdPct int
	latencyThresholdMs   int64
	recentCalls          []CallResult

	// Component state: component name → "ok"|"degraded"|"offline"
	componentState map[string]string
}

// NewTelemetry creates a Telemetry instance.
// progress may be nil (notifications will be skipped).
func NewTelemetry(
	cfg TelemetryConfig,
	botmem *BotMemory,
	progress ProgressNotifier,
) *Telemetry {
	ws := cfg.WindowSize
	if ws <= 0 {
		ws = 5
	}
	tfp := cfg.ToolFailThresholdPct
	if tfp <= 0 {
		tfp = 30
	}
	ltm := cfg.LatencyThresholdMs
	if ltm <= 0 {
		ltm = 30000
	}
	dbu := cfg.DailyBudgetUSD
	if dbu <= 0 {
		dbu = 2.00
	}
	return &Telemetry{
		botmem:               botmem,
		progress:             progress,
		dailyBudgetUSD:       dbu,
		windowSize:           ws,
		toolFailThresholdPct: tfp,
		latencyThresholdMs:   ltm,
		recentCalls:          make([]CallResult, 0, ws),
		componentState:       make(map[string]string),
	}
}

// CheckBudget returns whether the daily budget allows another
// LLM call and the remaining amount.
// Called from pipeline_llm.go BEFORE each LLM invocation.
func (t *Telemetry) CheckBudget() (
	allowed bool, remaining float64,
) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureBudgetDate()
	remaining = t.dailyBudgetUSD - t.spentTodayUSD
	return remaining > 0, remaining
}

// RecordLLMCall persists an LLM call to request_log and updates
// the in-memory daily spend counter.
// Called from pipeline_llm.go AFTER each LLM invocation.
func (t *Telemetry) RecordLLMCall(
	ctx context.Context, p RecordLLMParams,
) {
	t.mu.Lock()
	t.ensureBudgetDate()
	t.spentTodayUSD += p.CostUSD
	t.mu.Unlock()

	if t.botmem == nil {
		return
	}
	row := RequestLogRow{
		SessionID:        p.SessionID,
		Direction:        p.Direction,
		Component:        p.Component,
		Model:            p.Model,
		PromptTokens:     p.TokensIn,
		CompletionTokens: p.TokensOut,
		CostUSD:          p.CostUSD,
		ResponseMs:       int(p.LatencyMs),
		Error:            p.Error,
	}
	// Fire-and-forget: telemetry should not block pipeline.
	_, _ = t.botmem.InsertRequestLog(ctx, row)
}

// RecordToolCall records a tool call result into the sliding
// window and evaluates system health.
// Called from pipeline_tool.go AFTER each tool invocation.
func (t *Telemetry) RecordToolCall(result CallResult) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Ring buffer
	if len(t.recentCalls) >= t.windowSize {
		t.recentCalls = t.recentCalls[1:]
	}
	t.recentCalls = append(t.recentCalls, result)

	t.evaluateHealth()
}

// ReportComponentFailure records a component entering a degraded
// or offline state. Sends notification if state changed.
// Called directly from turn_coord.go or other pipeline code.
func (t *Telemetry) ReportComponentFailure(
	component, status string,
) {
	t.mu.Lock()
	prev := t.componentState[component]
	t.componentState[component] = status
	t.mu.Unlock()

	if prev != status && t.progress != nil {
		t.progress.NotifyDegradation(component, status)
	}
}

// ReportComponentSuccess records a component recovering to
// healthy. Sends a recovery notification only if previously
// not ok.
func (t *Telemetry) ReportComponentSuccess(
	component string,
) {
	t.mu.Lock()
	prev := t.componentState[component]
	t.componentState[component] = "ok"
	t.mu.Unlock()

	if prev != "" && prev != "ok" && t.progress != nil {
		t.progress.NotifyRecovery(component)
	}
}

// GetSystemState returns the current system state for the META
// block in buildPrompt. Implements SystemStateProvider.
func (t *Telemetry) GetSystemState() SystemState {
	t.mu.Lock()
	defer t.mu.Unlock()

	var degraded []string
	hasOffline := false
	for comp, st := range t.componentState {
		switch st {
		case "offline":
			hasOffline = true
			degraded = append(degraded, comp)
		case "degraded":
			degraded = append(degraded, comp)
		}
	}

	if hasOffline {
		return SystemState{
			Status:             "offline",
			DegradedComponents: degraded,
		}
	}
	if len(degraded) > 0 {
		return SystemState{
			Status:             "degraded",
			DegradedComponents: degraded,
		}
	}
	return StateHealthy
}

// GetBudgetRemaining returns the remaining daily budget in USD.
func (t *Telemetry) GetBudgetRemaining() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureBudgetDate()
	return t.dailyBudgetUSD - t.spentTodayUSD
}

// ensureBudgetDate resets spentTodayUSD if it is a new day.
// Must be called with t.mu held.
func (t *Telemetry) ensureBudgetDate() {
	today := time.Now().Format("2006-01-02")
	if t.budgetDate != today {
		t.budgetDate = today
		t.spentTodayUSD = t.loadTodaySpent()
	}
}

// loadTodaySpent queries the DB for today's total spend.
func (t *Telemetry) loadTodaySpent() float64 {
	if t.botmem == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(
		context.Background(), 5*time.Second,
	)
	defer cancel()
	total, err := t.botmem.QueryTodayCostUSD(ctx)
	if err != nil {
		return 0
	}
	return total
}

// evaluateHealth checks the sliding window for tool failures.
// Triggers degradation/recovery for "tool_executor" component.
// Must be called with t.mu held.
func (t *Telemetry) evaluateHealth() {
	if len(t.recentCalls) < t.windowSize {
		return
	}

	failCount := 0
	for _, c := range t.recentCalls {
		if c.Status == "error" || c.Status == "timeout" {
			failCount++
		}
	}

	failPct := float64(failCount) /
		float64(len(t.recentCalls)) * 100

	comp := "tool_executor"
	if failPct >= float64(t.toolFailThresholdPct) {
		prev := t.componentState[comp]
		t.componentState[comp] = "degraded"
		if prev != "degraded" && t.progress != nil {
			t.progress.NotifyDegradation(comp, "degraded")
		}
	} else if t.componentState[comp] == "degraded" {
		t.componentState[comp] = "ok"
		if t.progress != nil {
			t.progress.NotifyRecovery(comp)
		}
	}
}

// Compile-time check: Telemetry implements SystemStateProvider.
var _ SystemStateProvider = (*Telemetry)(nil)
