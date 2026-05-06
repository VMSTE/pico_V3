package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type analyticsMockSender struct {
	messages []string
	fail     bool
	calls    int
}

func (m *analyticsMockSender) SendMessage(_ context.Context, text string) (string, error) {
	m.calls++
	if m.fail {
		return "", fmt.Errorf("mock send error")
	}
	m.messages = append(m.messages, text)
	return "msg-1", nil
}

func (m *analyticsMockSender) EditMessage(_ context.Context, _ string, _ string) error { return nil }
func (m *analyticsMockSender) DeleteMessage(_ context.Context, _ string) error         { return nil }
func (m *analyticsMockSender) SendConfirmation(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func setupAnalyticsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ddl := `
	CREATE TABLE IF NOT EXISTS request_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT NOT NULL DEFAULT (datetime('now')),
		session_id TEXT, msg_index INTEGER,
		direction TEXT NOT NULL DEFAULT 'outbound',
		component TEXT NOT NULL DEFAULT 'main',
		model TEXT NOT NULL DEFAULT '',
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		cached_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		estimated_tokens INTEGER,
		tool_calls_requested INTEGER NOT NULL DEFAULT 0,
		tool_calls_success INTEGER NOT NULL DEFAULT 0,
		tool_calls_failed INTEGER NOT NULL DEFAULT 0,
		tool_names TEXT,
		cost_usd REAL NOT NULL DEFAULT 0.0,
		error TEXT NOT NULL DEFAULT '',
		retry_count INTEGER NOT NULL DEFAULT 0,
		response_ms INTEGER NOT NULL DEFAULT 0,
		task_tag TEXT, chain_id TEXT, chain_position INTEGER,
		plan_detected INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS trace_spans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		span_id TEXT NOT NULL, parent_span_id TEXT,
		trace_id TEXT NOT NULL DEFAULT '',
		session_id TEXT, turn_id INTEGER,
		component TEXT NOT NULL, operation TEXT NOT NULL DEFAULT '',
		started_at TEXT NOT NULL DEFAULT (datetime('now')),
		duration_ms INTEGER,
		status TEXT NOT NULL DEFAULT 'ok', input_data TEXT, completed_at TEXT, error_type TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS knowledge_atoms (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		atom_id TEXT NOT NULL UNIQUE,
		session_id TEXT NOT NULL DEFAULT '', turn_id INTEGER NOT NULL DEFAULT 0,
		source_event_id INTEGER, source_message_id INTEGER,
		category TEXT NOT NULL DEFAULT 'pattern',
		summary TEXT NOT NULL DEFAULT '', detail TEXT,
		confidence REAL NOT NULL DEFAULT 0.5,
		polarity TEXT NOT NULL DEFAULT 'neutral',
		verified INTEGER NOT NULL DEFAULT 0,
		tags TEXT, source_turns TEXT, history TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE TABLE IF NOT EXISTS atom_usage (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		atom_id TEXT NOT NULL, session_id TEXT, turn_id INTEGER,
		invoked_tool_result TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);
	CREATE TABLE IF NOT EXISTS registry (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT NOT NULL DEFAULT (datetime('now')),
		kind TEXT NOT NULL, key TEXT NOT NULL,
		summary TEXT, data TEXT,
		verified INTEGER NOT NULL DEFAULT 0,
		last_used TEXT, tags TEXT,
		UNIQUE(kind, key)
	);`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return db
}

func setupAnalyticsTestBM(t *testing.T, db *sql.DB) *BotMemory {
	t.Helper()
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatal(err)
	}
	return bm
}

func setupAnalyticsTestQDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	srcDir := filepath.Join("..", "..", "workspace", "queries")
	for _, f := range []string{
		"analytics_llm.sql", "analytics_tools.sql", "analytics_chains.sql",
		"analytics_subagents.sql", "analytics_knowledge.sql",
		"analytics_atom_usage.sql", "analytics_tasks.sql",
	} {
		data, err := os.ReadFile(filepath.Join(srcDir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return dir
}

func insertAnalyticsTestData(t *testing.T, db *sql.DB, now time.Time) {
	t.Helper()
	ts := now.Add(-2 * time.Hour).Format(sqliteTimeFmt)
	for i := 0; i < 50; i++ {
		errStr := ""
		if i%20 == 0 {
			errStr = "timeout"
		}
		comp := "main"
		if i%5 == 0 {
			comp = "archivarius"
		}
		taskTag := "deploy"
		if i%3 == 0 {
			taskTag = "fix"
		}
		chainID := "chain-1"
		if i%10 == 0 {
			chainID = "chain-2"
		}
		_, err := db.Exec(`INSERT INTO request_log
			(ts,component,prompt_tokens,completion_tokens,reasoning_tokens,
			tool_calls_requested,tool_calls_success,tool_calls_failed,
			tool_names,cost_usd,error,response_ms,task_tag,chain_id,chain_position)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			ts, comp, 500+i*10, 200+i*5, 50+i,
			3, 2, 1,
			`["sandbox","compose"]`, 0.05+float64(i)*0.01, errStr, 1000+i*100,
			taskTag, chainID, i%5)
		if err != nil {
			t.Fatal(err)
		}
	}
	comps := []string{"archivarius", "atomizer", "reflexor", "mcp_guard"}
	for i := 0; i < 20; i++ {
		status := "ok"
		if i%7 == 0 {
			status = "error"
		}
		_, err := db.Exec(`INSERT INTO trace_spans
			(span_id,trace_id,component,operation,started_at,duration_ms,status)
			VALUES(?,?,?,?,?,?,?)`,
			fmt.Sprintf("span-%d", i), "trace-1", comps[i%4], "run", ts, 500+i*200, status)
		if err != nil {
			t.Fatal(err)
		}
	}
	cats := []string{"pattern", "constraint", "decision", "tool_pref", "summary", "runbook_draft"}
	pols := []string{"positive", "negative", "neutral"}
	for i := 0; i < 30; i++ {
		conf := 0.9 - float64(i)*0.03
		if conf < 0.1 {
			conf = 0.1
		}
		_, err := db.Exec(`INSERT INTO knowledge_atoms
			(atom_id,category,summary,confidence,polarity,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?)`,
			fmt.Sprintf("P-%d", i), cats[i%6], fmt.Sprintf("atom %d", i),
			conf, pols[i%3], ts, ts)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 40; i++ {
		result := "success"
		if i%5 == 0 {
			result = "failure"
		}
		_, err := db.Exec(`INSERT INTO atom_usage
			(atom_id,invoked_tool_result,created_at) VALUES(?,?,?)`,
			fmt.Sprintf("P-%d", i%20), result, ts)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAnalytics_CollectMetrics_Happy(t *testing.T) {
	db := setupAnalyticsTestDB(t)
	defer db.Close()
	bm := setupAnalyticsTestBM(t, db)
	defer bm.Close()
	qDir := setupAnalyticsTestQDir(t)
	now := time.Now().UTC()
	insertAnalyticsTestData(t, db, now)

	engine := NewAnalyticsEngine(bm, nil, nil, qDir)
	period := AnalyticsPeriod{Start: now.Add(-24 * time.Hour), End: now.Add(time.Hour)}

	m, err := engine.collectMetrics(context.Background(), period)
	if err != nil {
		t.Fatalf("collectMetrics: %v", err)
	}
	if m.LLM.TotalRequests != 50 {
		t.Errorf("LLM.TotalRequests = %d, want 50", m.LLM.TotalRequests)
	}
	if m.LLM.TotalCostUSD <= 0 {
		t.Error("LLM.TotalCostUSD should be > 0")
	}
	if m.Tools.TotalRequested != 150 {
		t.Errorf("Tools.TotalRequested = %d, want 150", m.Tools.TotalRequested)
	}
	if len(m.Tools.TopTools) == 0 {
		t.Error("TopTools should not be empty")
	}
	if m.Chains.TotalChains == 0 {
		t.Error("TotalChains should be > 0")
	}
	if len(m.Subagents) == 0 {
		t.Error("Subagents should not be empty")
	}
	if m.Knowledge.TotalAtoms != 30 {
		t.Errorf("Knowledge.TotalAtoms = %d, want 30", m.Knowledge.TotalAtoms)
	}
	if m.AtomUsage.TotalUsages != 40 {
		t.Errorf("AtomUsage.TotalUsages = %d, want 40", m.AtomUsage.TotalUsages)
	}
	if len(m.Tasks) == 0 {
		t.Error("Tasks should not be empty")
	}
}

func TestAnalytics_CollectMetrics_PartialFail(t *testing.T) {
	db := setupAnalyticsTestDB(t)
	defer db.Close()
	bm := setupAnalyticsTestBM(t, db)
	defer bm.Close()
	emptyDir := t.TempDir()
	engine := NewAnalyticsEngine(bm, nil, nil, emptyDir)
	now := time.Now().UTC()
	period := AnalyticsPeriod{Start: now.Add(-24 * time.Hour), End: now}

	m, err := engine.collectMetrics(context.Background(), period)
	if err == nil {
		t.Error("expected error for missing SQL files")
	}
	if m == nil {
		t.Fatal("metrics should not be nil even on error")
	}
	if m.LLM.TotalRequests != 0 {
		t.Errorf("expected 0 requests, got %d", m.LLM.TotalRequests)
	}
}

func TestAnalytics_CollectMetrics_EmptyDB(t *testing.T) {
	db := setupAnalyticsTestDB(t)
	defer db.Close()
	bm := setupAnalyticsTestBM(t, db)
	defer bm.Close()
	qDir := setupAnalyticsTestQDir(t)
	engine := NewAnalyticsEngine(bm, nil, nil, qDir)
	now := time.Now().UTC()
	period := AnalyticsPeriod{Start: now.Add(-24 * time.Hour), End: now}

	m, err := engine.collectMetrics(context.Background(), period)
	if err != nil {
		t.Fatalf("collectMetrics empty: %v", err)
	}
	if m.LLM.TotalRequests != 0 {
		t.Errorf("expected 0, got %d", m.LLM.TotalRequests)
	}
	if m.Knowledge.TotalAtoms != 0 {
		t.Errorf("expected 0 atoms, got %d", m.Knowledge.TotalAtoms)
	}
}

func TestAnalytics_Deltas_Increase(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	cur.LLM.TotalRequests = 100
	prev := &AnalyticsPeriodMetrics{}
	prev.LLM.TotalRequests = 80
	d := analyticsComputeDeltas(cur, prev)
	delta := d["llm.total_requests"]
	if delta.Direction != "↑" {
		t.Errorf("direction = %s, want ↑", delta.Direction)
	}
	if math.Abs(delta.DeltaPct-25.0) > 0.1 {
		t.Errorf("DeltaPct = %.1f, want 25.0", delta.DeltaPct)
	}
}

func TestAnalytics_Deltas_Decrease(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	cur.LLM.TotalRequests = 60
	prev := &AnalyticsPeriodMetrics{}
	prev.LLM.TotalRequests = 80
	d := analyticsComputeDeltas(cur, prev)
	delta := d["llm.total_requests"]
	if delta.Direction != "↓" {
		t.Errorf("direction = %s, want ↓", delta.Direction)
	}
	if math.Abs(delta.DeltaPct-(-25.0)) > 0.1 {
		t.Errorf("DeltaPct = %.1f, want -25.0", delta.DeltaPct)
	}
}

func TestAnalytics_Deltas_ZeroPrevious(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	cur.LLM.TotalRequests = 100
	prev := &AnalyticsPeriodMetrics{}
	d := analyticsComputeDeltas(cur, prev)
	delta := d["llm.total_requests"]
	if delta.Direction != "→" {
		t.Errorf("direction = %s, want →", delta.Direction)
	}
}

func TestAnalytics_Anomaly_ToolFailRate(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	cur.Tools.TotalRequested = 100
	cur.Tools.SuccessRatePct = 88.0
	anomalies := analyticsDetectAnomalies(cur, make(map[string]AnalyticsDelta))
	found := false
	for _, a := range anomalies {
		if a.Metric == "tool_fail_rate" && a.Severity == "🔴" {
			found = true
		}
	}
	if !found {
		t.Error("expected 🔴 tool_fail_rate anomaly")
	}
}

func TestAnalytics_Anomaly_ErrorRate(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	cur.LLM.ErrorRatePct = 6.0
	anomalies := analyticsDetectAnomalies(cur, make(map[string]AnalyticsDelta))
	found := false
	for _, a := range anomalies {
		if a.Metric == "llm_error_rate" && a.Severity == "🔴" {
			found = true
		}
	}
	if !found {
		t.Error("expected 🔴 llm_error_rate anomaly")
	}
}

func TestAnalytics_Anomaly_SubagentErrors(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	sub := SubagentMetrics{Component: "archivarius", ErrorCount: 7}
	cur.Subagents = append(cur.Subagents, sub)
	anomalies := analyticsDetectAnomalies(cur, make(map[string]AnalyticsDelta))
	found := false
	for _, a := range anomalies {
		if a.Metric == "subagent_errors" && a.Severity == "🔴" {
			found = true
		}
	}
	if !found {
		t.Error("expected 🔴 subagent_errors anomaly")
	}
}

func TestAnalytics_Anomaly_Latency(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	cur.LLM.P95ResponseMs = 16000
	anomalies := analyticsDetectAnomalies(cur, make(map[string]AnalyticsDelta))
	found := false
	for _, a := range anomalies {
		if a.Metric == "latency_p95" && a.Severity == "🟡" {
			found = true
		}
	}
	if !found {
		t.Error("expected 🟡 latency_p95 anomaly")
	}
}

func TestAnalytics_Anomaly_UnusedAtoms(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	cur.AtomUsage.UnusedPct = 25.0
	anomalies := analyticsDetectAnomalies(cur, make(map[string]AnalyticsDelta))
	found := false
	for _, a := range anomalies {
		if a.Metric == "unused_atoms" && a.Severity == "🟡" {
			found = true
		}
	}
	if !found {
		t.Error("expected 🟡 unused_atoms anomaly")
	}
}

func TestAnalytics_Anomaly_StaleAtoms(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	cur.Knowledge.TotalAtoms = 100
	cur.Knowledge.ConfStale = 12
	anomalies := analyticsDetectAnomalies(cur, make(map[string]AnalyticsDelta))
	found := false
	for _, a := range anomalies {
		if a.Metric == "stale_atoms" && a.Severity == "🟡" {
			found = true
		}
	}
	if !found {
		t.Error("expected 🟡 stale_atoms anomaly")
	}
}

func TestAnalytics_Anomaly_SignificantDelta(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	deltas := make(map[string]AnalyticsDelta)
	deltas["llm.total_requests"] = AnalyticsDelta{Current: 160, Previous: 100, DeltaPct: 60, Direction: "↑"}
	anomalies := analyticsDetectAnomalies(cur, deltas)
	found := false
	for _, a := range anomalies {
		if a.Metric == "llm.total_requests" && a.Severity == "🟡" {
			found = true
		}
	}
	if !found {
		t.Error("expected 🟡 significant delta anomaly")
	}
}

func TestAnalytics_Anomaly_Clean(t *testing.T) {
	cur := &AnalyticsPeriodMetrics{}
	cur.Tools.TotalRequested = 100
	cur.Tools.SuccessRatePct = 95.0
	cur.LLM.ErrorRatePct = 1.0
	cur.LLM.P95ResponseMs = 5000
	cur.AtomUsage.UnusedPct = 5.0
	cur.Knowledge.TotalAtoms = 100
	cur.Knowledge.ConfStale = 2
	deltas := make(map[string]AnalyticsDelta)
	deltas["llm.total_requests"] = AnalyticsDelta{DeltaPct: 10, Direction: "↑"}
	anomalies := analyticsDetectAnomalies(cur, deltas)
	if len(anomalies) != 0 {
		t.Errorf("expected 0 anomalies, got %d: %+v", len(anomalies), anomalies)
	}
}

func TestAnalytics_FormatReport(t *testing.T) {
	report := &AnalyticsReport{
		Mode:    "weekly",
		Deltas:  make(map[string]AnalyticsDelta),
	}
	report.Current.Period.Start = time.Now().AddDate(0, 0, -7)
	report.Current.Period.End = time.Now()
	report.Current.LLM = LLMMetrics{
		TotalRequests: 342, TotalTokens: 1200000, TotalCostUSD: 3.45,
		CostByComponent: map[string]float64{"main": 2.80, "archivarius": 0.40},
		AvgResponseMs: 3200, P95ResponseMs: 8100, ErrorRatePct: 2.1, ReasoningRatio: 0.45,
	}
	report.Current.Tools = ToolMetrics{TotalRequested: 890, SuccessRatePct: 94.2}
	tt := AnalyticsNameCount{Name: "sandbox", Count: 340}
	report.Current.Tools.TopTools = append(report.Current.Tools.TopTools, tt)
	report.Current.Chains = ChainMetrics{TotalChains: 45, AvgChainLength: 4.2, AvgChainCost: 0.12}
	sa := SubagentMetrics{Component: "archivarius", AvgDurationMs: 2100, P95DurationMs: 4800}
	report.Current.Subagents = append(report.Current.Subagents, sa)
	report.Current.Knowledge = KnowledgeMetrics{
		TotalAtoms: 245, NewInPeriod: 18,
		ByCategory: map[string]int{"pattern": 89, "constraint": 34, "decision": 45, "tool_pref": 23, "summary": 42, "runbook_draft": 12},
		ByPolarity: map[string]int{"positive": 164, "negative": 51, "neutral": 30},
		ConfHigh: 152, ConfMedium: 69, ConfLow: 19, ConfStale: 5,
	}
	report.Current.AtomUsage = AtomUsageMetrics{TotalUsages: 1230, EffectivenessPct: 78, UnusedCount: 23, UnusedPct: 9.4}

	anom := AnalyticsAnomaly{Severity: "🔴", Metric: "test", Message: "test anomaly"}
	report.Anomalies = append(report.Anomalies, anom)

	text := analyticsFormatReport(report)
	if !strings.Contains(text, "📊") {
		t.Error("report should contain header emoji")
	}
	if !strings.Contains(text, "⚠️") {
		t.Error("report with anomalies should contain ⚠️")
	}
}

func TestAnalytics_FormatReport_WithAnomalies(t *testing.T) {
	report := &AnalyticsReport{
		Mode:   "weekly",
		Deltas: make(map[string]AnalyticsDelta),
	}
	report.Current.Period.Start = time.Now()
	report.Current.Period.End = time.Now()
	report.Current.Knowledge.TotalAtoms = 1
	report.Current.Knowledge.ByCategory = map[string]int{}
	report.Current.Knowledge.ByPolarity = map[string]int{}

	a1 := AnalyticsAnomaly{Severity: "🔴", Metric: "test", Message: "critical issue"}
	a2 := AnalyticsAnomaly{Severity: "🟡", Metric: "test2", Message: "warning issue"}
	report.Anomalies = append(report.Anomalies, a1, a2)

	text := analyticsFormatReport(report)
	if !strings.Contains(text, "🔴 critical issue") {
		t.Error("should contain critical anomaly")
	}
	if !strings.Contains(text, "🟡 warning issue") {
		t.Error("should contain warning anomaly")
	}
}

func TestAnalytics_StoreReport(t *testing.T) {
	db := setupAnalyticsTestDB(t)
	defer db.Close()
	bm := setupAnalyticsTestBM(t, db)
	defer bm.Close()
	qDir := setupAnalyticsTestQDir(t)
	engine := NewAnalyticsEngine(bm, nil, nil, qDir)

	report := &AnalyticsReport{Mode: "weekly", GeneratedAt: time.Now()}
	report.Current.Period.Start = time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	report.Current.Period.End = time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)

	if err := engine.storeReport(context.Background(), report); err != nil {
		t.Fatalf("first store: %v", err)
	}
	report.GeneratedAt = time.Now()
	if err := engine.storeReport(context.Background(), report); err != nil {
		t.Fatalf("second store: %v", err)
	}

	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM registry WHERE kind='snapshot' AND key LIKE 'analytics_weekly_%'`).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 registry row, got %d", count)
	}
}

func TestAnalytics_P95(t *testing.T) {
	data := []int{100, 200, 300, 400, 500, 600, 700, 800, 900, 1000}
	p95 := analyticsPercentile(data, 95)
	if p95 != 1000 {
		t.Errorf("p95 = %d, want 1000", p95)
	}
	p50 := analyticsPercentile(data, 50)
	if p50 != 500 {
		t.Errorf("p50 = %d, want 500", p50)
	}
}

func TestAnalytics_SplitMessage(t *testing.T) {
	short := "hello world"
	parts := analyticsSplitMessage(short, 100)
	if len(parts) != 1 {
		t.Errorf("short message should be 1 part, got %d", len(parts))
	}
	long := strings.Repeat("line\n\n", 500)
	parts = analyticsSplitMessage(long, 100)
	if len(parts) < 2 {
		t.Errorf("long message should be split, got %d parts", len(parts))
	}
}

func TestAnalytics_Periods_Weekly(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	cur, prev := analyticsComputePeriods(AnalyticsWeekly, now)
	if cur.Start.Day() != 4 {
		t.Errorf("weekly current start day = %d, want 4", cur.Start.Day())
	}
	if prev.Start.Day() != 27 || prev.Start.Month() != 4 {
		t.Errorf("weekly prev start = %v, want April 27", prev.Start)
	}
}

func TestAnalytics_Periods_Monthly(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	cur, prev := analyticsComputePeriods(AnalyticsMonthly, now)
	if cur.Start.Month() != 4 || cur.Start.Day() != 1 {
		t.Errorf("monthly current start = %v, want April 1", cur.Start)
	}
	if cur.End.Month() != 5 || cur.End.Day() != 1 {
		t.Errorf("monthly current end = %v, want May 1", cur.End)
	}
	if prev.Start.Month() != 3 {
		t.Errorf("monthly prev start month = %v, want March", prev.Start.Month())
	}
}

func TestAnalytics_HasCritical(t *testing.T) {
	warn := AnalyticsAnomaly{Severity: "🟡", Metric: "warn", Message: "warning"}
	var warnList []AnalyticsAnomaly
	warnList = append(warnList, warn)
	if analyticsHasCritical(warnList) {
		t.Error("should not have critical")
	}

	crit := AnalyticsAnomaly{Severity: "🔴", Metric: "crit", Message: "critical"}
	var critList []AnalyticsAnomaly
	critList = append(critList, warn, crit)
	if !analyticsHasCritical(critList) {
		t.Error("should have critical")
	}
}

func TestAnalytics_FormatCount(t *testing.T) {
	if got := analyticsFormatCount(500); got != "500" {
		t.Errorf("formatCount(500) = %s, want 500", got)
	}
	if got := analyticsFormatCount(1500); got != "1.5K" {
		t.Errorf("formatCount(1500) = %s, want 1.5K", got)
	}
	if got := analyticsFormatCount(1200000); got != "1.2M" {
		t.Errorf("formatCount(1200000) = %s, want 1.2M", got)
	}
}

// unused import guard
var _ = json.Marshal
