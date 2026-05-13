// PIKA-V3: analytics.go — Go-only Analytics Pipeline.
// ТЗ-v2-7b. Trajectory analysis + efficiency metrics.
// 0 LLM. Pure Go + SQL over request_log, trace_spans, atom_usage, knowledge_atoms.
// Cron weekly + monthly. Report to Telegram (channels.analytics).
// History in registry (kind='snapshot').

package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// ---------------------------------------------------------------------------
// Constants (D-148: hardcoded Go, not config.json)
// ---------------------------------------------------------------------------

const (
	registryKindSnapshot  = "snapshot"
	registryKeyWeeklyPfx  = "analytics_weekly_"
	registryKeyMonthlyPfx = "analytics_monthly_"

	AnalyticsWeekly  = "weekly"
	AnalyticsMonthly = "monthly"
)

// ---------------------------------------------------------------------------
// Structs
// ---------------------------------------------------------------------------

type AnalyticsPeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type AnalyticsNameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type LLMMetrics struct {
	TotalRequests   int                `json:"total_requests"`
	TotalTokens     int64              `json:"total_tokens"`
	TotalCostUSD    float64            `json:"total_cost_usd"`
	CostByComponent map[string]float64 `json:"cost_by_component"`
	AvgResponseMs   int                `json:"avg_response_ms"`
	P95ResponseMs   int                `json:"p95_response_ms"`
	ErrorRatePct    float64            `json:"error_rate_pct"`
	ReasoningRatio  float64            `json:"reasoning_ratio"`
}

type ToolMetrics struct {
	TotalRequested int                  `json:"total_requested"`
	TotalSuccess   int                  `json:"total_success"`
	TotalFailed    int                  `json:"total_failed"`
	SuccessRatePct float64              `json:"success_rate_pct"`
	TopTools       []AnalyticsNameCount `json:"top_tools"`
}

type ChainMetrics struct {
	TotalChains    int     `json:"total_chains"`
	AvgChainLength float64 `json:"avg_chain_length"`
	AvgChainCost   float64 `json:"avg_chain_cost"`
}

type SubagentMetrics struct {
	Component     string `json:"component"`
	TotalSpans    int    `json:"total_spans"`
	ErrorCount    int    `json:"error_count"`
	TimeoutCount  int    `json:"timeout_count"`
	AvgDurationMs int    `json:"avg_duration_ms"`
	P95DurationMs int    `json:"p95_duration_ms"`
}

type KnowledgeMetrics struct {
	TotalAtoms  int            `json:"total_atoms"`
	NewInPeriod int            `json:"new_in_period"`
	ByCategory  map[string]int `json:"by_category"`
	ByPolarity  map[string]int `json:"by_polarity"`
	ConfHigh    int            `json:"conf_high"`
	ConfMedium  int            `json:"conf_medium"`
	ConfLow     int            `json:"conf_low"`
	ConfStale   int            `json:"conf_stale"`
}

type AtomUsageMetrics struct {
	TotalUsages      int                  `json:"total_usages"`
	UniqueAtomsUsed  int                  `json:"unique_atoms_used"`
	EffectivenessPct float64              `json:"effectiveness_pct"`
	TopAtoms         []AnalyticsNameCount `json:"top_atoms"`
	UnusedCount      int                  `json:"unused_count"`
	UnusedPct        float64              `json:"unused_pct"`
}

type AnalyticsTaskMetrics struct {
	TaskTag      string  `json:"task_tag"`
	RequestCount int     `json:"request_count"`
	AvgTokens    float64 `json:"avg_tokens"`
	AvgTools     float64 `json:"avg_tools"`
	TotalCost    float64 `json:"total_cost"`
	AvgCost      float64 `json:"avg_cost"`
}

type AnalyticsPeriodMetrics struct {
	Period    AnalyticsPeriod        `json:"period"`
	LLM       LLMMetrics             `json:"llm"`
	Tools     ToolMetrics            `json:"tools"`
	Chains    ChainMetrics           `json:"chains"`
	Subagents []SubagentMetrics      `json:"subagents"`
	Knowledge KnowledgeMetrics       `json:"knowledge"`
	AtomUsage AtomUsageMetrics       `json:"atom_usage"`
	Tasks     []AnalyticsTaskMetrics `json:"tasks"`
}

type AnalyticsDelta struct {
	Current   float64 `json:"current"`
	Previous  float64 `json:"previous"`
	DeltaPct  float64 `json:"delta_pct"`
	Direction string  `json:"direction"`
}

type AnalyticsAnomaly struct {
	Severity  string  `json:"severity"`
	Metric    string  `json:"metric"`
	Message   string  `json:"message"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
}

type AnalyticsReport struct {
	Mode        string                    `json:"mode"`
	Current     AnalyticsPeriodMetrics    `json:"current"`
	Previous    AnalyticsPeriodMetrics    `json:"previous"`
	Deltas      map[string]AnalyticsDelta `json:"deltas"`
	Anomalies   []AnalyticsAnomaly        `json:"anomalies"`
	GeneratedAt time.Time                 `json:"generated_at"`
}

// ---------------------------------------------------------------------------
// AnalyticsEngine
// ---------------------------------------------------------------------------

type AnalyticsEngine struct {
	cfg        config.AnalyticsConfig
	mem        *BotMemory
	sender     TelegramSender
	mgrSender  TelegramSender
	queriesDir string
}

func NewAnalyticsEngine(
	cfg config.AnalyticsConfig,
	mem *BotMemory,
	sender TelegramSender,
	mgrSender TelegramSender,
	queriesDir string,
) *AnalyticsEngine {
	cfg = applyAnalyticsDefaults(cfg)
	return &AnalyticsEngine{
		cfg:        cfg,
		mem:        mem,
		sender:     sender,
		mgrSender:  mgrSender,
		queriesDir: queriesDir,
	}
}

// applyAnalyticsDefaults fills zero-value config fields with hardcoded defaults. PIKA-V3.
func applyAnalyticsDefaults(cfg config.AnalyticsConfig) config.AnalyticsConfig {
	if cfg.ToolFailRatePct == 0 {
		cfg.ToolFailRatePct = 10.0
	}
	if cfg.ErrorRatePct == 0 {
		cfg.ErrorRatePct = 5.0
	}
	if cfg.LatencyP95Ms == 0 {
		cfg.LatencyP95Ms = 15000
	}
	if cfg.UnusedAtomsPct == 0 {
		cfg.UnusedAtomsPct = 20.0
	}
	if cfg.StaleAtomsPct == 0 {
		cfg.StaleAtomsPct = 10.0
	}
	if cfg.SubagentErrors == 0 {
		cfg.SubagentErrors = 5
	}
	if cfg.DeltaSignificantPct == 0 {
		cfg.DeltaSignificantPct = 50.0
	}
	if cfg.ReportMaxTelegramChars == 0 {
		cfg.ReportMaxTelegramChars = 4000
	}
	if cfg.TopToolsLimit == 0 {
		cfg.TopToolsLimit = 10
	}
	if cfg.TopAtomsLimit == 0 {
		cfg.TopAtomsLimit = 10
	}
	if cfg.TopTasksLimit == 0 {
		cfg.TopTasksLimit = 5
	}
	return cfg
}

// Run executes the analytics pipeline. mode = "weekly" or "monthly".
func (ae *AnalyticsEngine) Run(ctx context.Context, mode string) error {
	log.Printf("[analytics] starting mode=%s", mode)

	cur, prev := analyticsComputePeriods(mode, time.Now())

	current, err := ae.collectMetrics(ctx, cur)
	if err != nil {
		log.Printf("[analytics] WARN collectMetrics current: %v", err)
	}

	previous, err := ae.collectMetrics(ctx, prev)
	if err != nil {
		log.Printf("[analytics] WARN collectMetrics previous: %v", err)
	}

	deltas := analyticsComputeDeltas(current, previous)
	anomalies := analyticsDetectAnomalies(current, deltas, ae.cfg)

	report := &AnalyticsReport{
		Mode:        mode,
		Current:     *current,
		Previous:    *previous,
		Deltas:      deltas,
		Anomalies:   anomalies,
		GeneratedAt: time.Now(),
	}

	text := analyticsFormatReport(report)

	if !ae.cfg.DisableTelegramReports {
		if sendErr := ae.sendReport(ctx, text); sendErr != nil {
			log.Printf("[analytics] WARN sendReport: %v", sendErr)
		}
	}

	if analyticsHasCritical(anomalies) && ae.mgrSender != nil {
		alertText := analyticsFormatAlert(anomalies)
		if _, alertErr := ae.mgrSender.SendMessage(ctx, alertText); alertErr != nil {
			log.Printf("[analytics] WARN manager alert: %v", alertErr)
		}
	}

	if storeErr := ae.storeReport(ctx, report); storeErr != nil {
		log.Printf("[analytics] WARN storeReport: %v", storeErr)
	}

	log.Printf("[analytics] done mode=%s anomalies=%d", mode, len(anomalies))
	return nil
}

// ---------------------------------------------------------------------------
// Period computation
// ---------------------------------------------------------------------------

func analyticsComputePeriods(mode string, now time.Time) (cur, prev AnalyticsPeriod) {
	now = now.UTC()
	switch mode {
	case AnalyticsWeekly:
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7
		}
		monStart := time.Date(now.Year(), now.Month(), now.Day()-wd+1, 0, 0, 0, 0, time.UTC)
		cur = AnalyticsPeriod{Start: monStart, End: now}
		prevMon := monStart.AddDate(0, 0, -7)
		prev = AnalyticsPeriod{Start: prevMon, End: monStart}
	case AnalyticsMonthly:
		firstThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		firstPrevMonth := firstThisMonth.AddDate(0, -1, 0)
		cur = AnalyticsPeriod{Start: firstPrevMonth, End: firstThisMonth}
		firstPrevPrevMonth := firstPrevMonth.AddDate(0, -1, 0)
		prev = AnalyticsPeriod{Start: firstPrevPrevMonth, End: firstPrevMonth}
	default:
		cur = AnalyticsPeriod{Start: now.AddDate(0, 0, -7), End: now}
		prev = AnalyticsPeriod{Start: now.AddDate(0, 0, -14), End: now.AddDate(0, 0, -7)}
	}
	return cur, prev
}

// ---------------------------------------------------------------------------
// Metric collection
// ---------------------------------------------------------------------------

func (ae *AnalyticsEngine) collectMetrics(ctx context.Context, p AnalyticsPeriod) (*AnalyticsPeriodMetrics, error) {
	m := &AnalyticsPeriodMetrics{Period: p}
	start := p.Start.Format(sqliteTimeFmt)
	end := p.End.Format(sqliteTimeFmt)

	var firstErr error
	capture := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	capture(ae.queryLLMMetrics(ctx, start, end, &m.LLM))
	capture(ae.queryToolMetrics(ctx, start, end, &m.Tools))
	capture(ae.queryChainMetrics(ctx, start, end, &m.Chains))

	subs, subErr := ae.querySubagentMetrics(ctx, start, end)
	capture(subErr)
	m.Subagents = subs

	capture(ae.queryKnowledgeMetrics(ctx, start, end, &m.Knowledge))
	capture(ae.queryAtomUsageMetrics(ctx, start, end, &m.AtomUsage))

	tasks, taskErr := ae.queryTaskMetrics(ctx, start, end)
	capture(taskErr)
	m.Tasks = tasks

	return m, firstErr
}

func (ae *AnalyticsEngine) loadSQL(name string) ([]string, error) {
	path := filepath.Join(ae.queriesDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pika/analytics: loadSQL %s: %w", name, err)
	}
	return analyticsSplitSQL(string(data)), nil
}

func analyticsSplitSQL(raw string) []string {
	parts := strings.Split(raw, ";")
	var out []string
	for _, p := range parts {
		q := strings.TrimSpace(p)
		if q == "" {
			continue
		}
		lines := strings.Split(q, "\n")
		var clean []string
		for _, l := range lines {
			tl := strings.TrimSpace(l)
			if tl == "" || strings.HasPrefix(tl, "--") {
				continue
			}
			clean = append(clean, l)
		}
		if joined := strings.Join(clean, "\n"); strings.TrimSpace(joined) != "" {
			out = append(out, joined)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Query methods
// ---------------------------------------------------------------------------

func (ae *AnalyticsEngine) queryLLMMetrics(ctx context.Context, start, end string, out *LLMMetrics) error {
	queries, err := ae.loadSQL("analytics_llm.sql")
	if err != nil {
		log.Printf("[analytics] WARN %v", err)
		return err
	}
	out.CostByComponent = make(map[string]float64)

	if len(queries) > 0 {
		row := ae.mem.db.QueryRowContext(ctx, queries[0], start, end)
		var errRate, reasonRatio sql.NullFloat64
		if scanErr := row.Scan(
			&out.TotalRequests, &out.TotalTokens, &out.TotalCostUSD,
			&out.AvgResponseMs, &errRate, &reasonRatio,
		); scanErr != nil && scanErr != sql.ErrNoRows {
			log.Printf("[analytics] WARN llm main: %v", scanErr)
		}
		if errRate.Valid {
			out.ErrorRatePct = errRate.Float64
		}
		if reasonRatio.Valid {
			out.ReasoningRatio = reasonRatio.Float64
		}
	}

	if len(queries) > 1 {
		rows, qErr := ae.mem.db.QueryContext(ctx, queries[1], start, end)
		if qErr == nil {
			for rows.Next() {
				var comp string
				defer rows.Close()
				var cost float64
				var cnt int
				if rows.Scan(&comp, &cost, &cnt) == nil {
					out.CostByComponent[comp] = cost
				}
			}
			if rErr := rows.Err(); rErr != nil {
				log.Printf("[analytics] WARN rows.Err: %v", rErr)
			}
			rows.Close()
		}
	}

	if len(queries) > 2 {
		rows, qErr := ae.mem.db.QueryContext(ctx, queries[2], start, end)
		if qErr == nil {
			var latencies []int
			for rows.Next() {
				defer rows.Close()
				var ms int
				if rows.Scan(&ms) == nil {
					latencies = append(latencies, ms)
				}
			}
			if rErr := rows.Err(); rErr != nil {
				log.Printf("[analytics] WARN rows.Err: %v", rErr)
			}
			rows.Close()
			if len(latencies) > 0 {
				out.P95ResponseMs = analyticsPercentile(latencies, 95)
			}
		}
	}
	return nil
}

func (ae *AnalyticsEngine) queryToolMetrics(ctx context.Context, start, end string, out *ToolMetrics) error {
	queries, err := ae.loadSQL("analytics_tools.sql")
	if err != nil {
		log.Printf("[analytics] WARN %v", err)
		return err
	}

	if len(queries) > 0 {
		row := ae.mem.db.QueryRowContext(ctx, queries[0], start, end)
		var sr sql.NullFloat64
		if scanErr := row.Scan(&out.TotalRequested, &out.TotalSuccess, &out.TotalFailed, &sr); scanErr != nil &&
			scanErr != sql.ErrNoRows {
			log.Printf("[analytics] WARN tools main: %v", scanErr)
		}
		if sr.Valid {
			out.SuccessRatePct = sr.Float64
		}
	}

	if len(queries) > 1 {
		rows, qErr := ae.mem.db.QueryContext(ctx, queries[1], start, end)
		if qErr == nil {
			for rows.Next() {
				var nc AnalyticsNameCount
				defer rows.Close()
				if rows.Scan(&nc.Name, &nc.Count) == nil {
					out.TopTools = append(out.TopTools, nc)
				}
			}
			if rErr := rows.Err(); rErr != nil {
				log.Printf("[analytics] WARN rows.Err: %v", rErr)
			}
			rows.Close()
		}
	}
	return nil
}

func (ae *AnalyticsEngine) queryChainMetrics(ctx context.Context, start, end string, out *ChainMetrics) error {
	queries, err := ae.loadSQL("analytics_chains.sql")
	if err != nil {
		log.Printf("[analytics] WARN %v", err)
		return err
	}
	if len(queries) > 0 {
		row := ae.mem.db.QueryRowContext(ctx, queries[0], start, end)
		var avgLen, avgCost sql.NullFloat64
		if scanErr := row.Scan(&out.TotalChains, &avgLen, &avgCost); scanErr != nil && scanErr != sql.ErrNoRows {
			log.Printf("[analytics] WARN chains: %v", scanErr)
		}
		if avgLen.Valid {
			out.AvgChainLength = avgLen.Float64
		}
		if avgCost.Valid {
			out.AvgChainCost = avgCost.Float64
		}
	}
	return nil
}

func (ae *AnalyticsEngine) querySubagentMetrics(ctx context.Context, start, end string) ([]SubagentMetrics, error) {
	queries, err := ae.loadSQL("analytics_subagents.sql")
	if err != nil {
		log.Printf("[analytics] WARN %v", err)
		return nil, err
	}

	compMap := make(map[string]*SubagentMetrics)

	if len(queries) > 0 {
		rows, qErr := ae.mem.db.QueryContext(ctx, queries[0], start, end)
		if qErr == nil {
			for rows.Next() {
				var s SubagentMetrics
				defer rows.Close()
				if rows.Scan(&s.Component, &s.TotalSpans, &s.ErrorCount, &s.TimeoutCount, &s.AvgDurationMs) == nil {
					compMap[s.Component] = &s
				}
			}
			if rErr := rows.Err(); rErr != nil {
				log.Printf("[analytics] WARN rows.Err: %v", rErr)
			}
			rows.Close()
		}
	}

	if len(queries) > 1 {
		rows, qErr := ae.mem.db.QueryContext(ctx, queries[1], start, end)
		if qErr == nil {
			durMap := make(map[string][]int)
			for rows.Next() {
				defer rows.Close()
				var comp string
				var ms int
				if rows.Scan(&comp, &ms) == nil {
					durMap[comp] = append(durMap[comp], ms)
				}
			}
			if rErr := rows.Err(); rErr != nil {
				log.Printf("[analytics] WARN rows.Err: %v", rErr)
			}
			rows.Close()
			for comp, durs := range durMap {
				if s, ok := compMap[comp]; ok {
					s.P95DurationMs = analyticsPercentile(durs, 95)
				}
			}
		}
	}

	var out []SubagentMetrics
	for _, comp := range []string{"archivarius", "atomizer", "reflexor", "mcp_guard"} {
		if s, ok := compMap[comp]; ok {
			out = append(out, *s)
		}
	}
	return out, nil
}

func (ae *AnalyticsEngine) queryKnowledgeMetrics(ctx context.Context, start, end string, out *KnowledgeMetrics) error {
	queries, err := ae.loadSQL("analytics_knowledge.sql")
	if err != nil {
		log.Printf("[analytics] WARN %v", err)
		return err
	}
	out.ByCategory = make(map[string]int)
	out.ByPolarity = make(map[string]int)

	if len(queries) > 0 {
		row := ae.mem.db.QueryRowContext(ctx, queries[0], start)
		var catP, catC, catD, catT, catS, catR int
		var polPos, polNeg, polNeu int
		if scanErr := row.Scan(
			&out.TotalAtoms, &out.NewInPeriod,
			&catP, &catC, &catD, &catT, &catS, &catR,
			&polPos, &polNeg, &polNeu,
			&out.ConfHigh, &out.ConfMedium, &out.ConfLow, &out.ConfStale,
		); scanErr != nil && scanErr != sql.ErrNoRows {
			log.Printf("[analytics] WARN knowledge: %v", scanErr)
		}
		out.ByCategory["pattern"] = catP
		out.ByCategory["constraint"] = catC
		out.ByCategory["decision"] = catD
		out.ByCategory["tool_pref"] = catT
		out.ByCategory["summary"] = catS
		out.ByCategory["runbook_draft"] = catR
		out.ByPolarity["positive"] = polPos
		out.ByPolarity["negative"] = polNeg
		out.ByPolarity["neutral"] = polNeu
	}
	return nil
}

func (ae *AnalyticsEngine) queryAtomUsageMetrics(ctx context.Context, start, end string, out *AtomUsageMetrics) error {
	queries, err := ae.loadSQL("analytics_atom_usage.sql")
	if err != nil {
		log.Printf("[analytics] WARN %v", err)
		return err
	}

	if len(queries) > 0 {
		row := ae.mem.db.QueryRowContext(ctx, queries[0], start, end)
		var eff sql.NullFloat64
		if scanErr := row.Scan(&out.TotalUsages, &out.UniqueAtomsUsed, &eff); scanErr != nil &&
			scanErr != sql.ErrNoRows {
			log.Printf("[analytics] WARN atom_usage main: %v", scanErr)
		}
		if eff.Valid {
			out.EffectivenessPct = eff.Float64
		}
	}

	if len(queries) > 1 {
		rows, qErr := ae.mem.db.QueryContext(ctx, queries[1], start, end)
		if qErr == nil {
			for rows.Next() {
				var nc AnalyticsNameCount
				defer rows.Close()
				if rows.Scan(&nc.Name, &nc.Count) == nil {
					out.TopAtoms = append(out.TopAtoms, nc)
				}
			}
			if rErr := rows.Err(); rErr != nil {
				log.Printf("[analytics] WARN rows.Err: %v", rErr)
			}
			rows.Close()
		}
	}

	if len(queries) > 2 {
		row := ae.mem.db.QueryRowContext(ctx, queries[2], start)
		if scanErr := row.Scan(&out.UnusedCount); scanErr != nil && scanErr != sql.ErrNoRows {
			log.Printf("[analytics] WARN unused atoms: %v", scanErr)
		}
		total := out.UniqueAtomsUsed + out.UnusedCount
		if total > 0 {
			out.UnusedPct = float64(out.UnusedCount) / float64(total) * 100
		}
	}
	return nil
}

func (ae *AnalyticsEngine) queryTaskMetrics(ctx context.Context, start, end string) ([]AnalyticsTaskMetrics, error) {
	queries, err := ae.loadSQL("analytics_tasks.sql")
	if err != nil {
		log.Printf("[analytics] WARN %v", err)
		return nil, err
	}
	var out []AnalyticsTaskMetrics
	if len(queries) > 0 {
		rows, qErr := ae.mem.db.QueryContext(ctx, queries[0], start, end)
		if qErr != nil {
			return nil, qErr
		}
		defer rows.Close()
		for rows.Next() {
			var t AnalyticsTaskMetrics
			if rows.Scan(&t.TaskTag, &t.RequestCount, &t.AvgTokens, &t.AvgTools, &t.TotalCost, &t.AvgCost) == nil {
				out = append(out, t)
			}
		}
		if rErr := rows.Err(); rErr != nil {
			log.Printf("[analytics] WARN rows.Err: %v", rErr)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Deltas
// ---------------------------------------------------------------------------

func analyticsComputeDeltas(cur, prev *AnalyticsPeriodMetrics) map[string]AnalyticsDelta {
	d := make(map[string]AnalyticsDelta)
	add := func(name string, c, p float64) {
		d[name] = analyticsMakeDelta(c, p)
	}
	add("llm.total_requests", float64(cur.LLM.TotalRequests), float64(prev.LLM.TotalRequests))
	add("llm.total_tokens", float64(cur.LLM.TotalTokens), float64(prev.LLM.TotalTokens))
	add("llm.total_cost_usd", cur.LLM.TotalCostUSD, prev.LLM.TotalCostUSD)
	add("llm.error_rate_pct", cur.LLM.ErrorRatePct, prev.LLM.ErrorRatePct)
	add("llm.avg_response_ms", float64(cur.LLM.AvgResponseMs), float64(prev.LLM.AvgResponseMs))
	add("llm.p95_response_ms", float64(cur.LLM.P95ResponseMs), float64(prev.LLM.P95ResponseMs))
	add("llm.reasoning_ratio", cur.LLM.ReasoningRatio, prev.LLM.ReasoningRatio)
	add("tools.success_rate_pct", cur.Tools.SuccessRatePct, prev.Tools.SuccessRatePct)
	add("tools.total_requested", float64(cur.Tools.TotalRequested), float64(prev.Tools.TotalRequested))
	add("chains.total_chains", float64(cur.Chains.TotalChains), float64(prev.Chains.TotalChains))
	add("chains.avg_chain_length", cur.Chains.AvgChainLength, prev.Chains.AvgChainLength)
	add("knowledge.total_atoms", float64(cur.Knowledge.TotalAtoms), float64(prev.Knowledge.TotalAtoms))
	add("knowledge.new_in_period", float64(cur.Knowledge.NewInPeriod), float64(prev.Knowledge.NewInPeriod))
	add("atom_usage.effectiveness_pct", cur.AtomUsage.EffectivenessPct, prev.AtomUsage.EffectivenessPct)
	add("atom_usage.unused_pct", cur.AtomUsage.UnusedPct, prev.AtomUsage.UnusedPct)
	return d
}

func analyticsMakeDelta(cur, prev float64) AnalyticsDelta {
	d := AnalyticsDelta{Current: cur, Previous: prev}
	if prev == 0 {
		d.Direction = "→"
		return d
	}
	d.DeltaPct = (cur - prev) / math.Abs(prev) * 100
	if d.DeltaPct > 0.5 {
		d.Direction = "↑"
	} else if d.DeltaPct < -0.5 {
		d.Direction = "↓"
	} else {
		d.Direction = "→"
	}
	return d
}

// ---------------------------------------------------------------------------
// Anomaly detection
// ---------------------------------------------------------------------------

func analyticsDetectAnomalies(
	cur *AnalyticsPeriodMetrics,
	deltas map[string]AnalyticsDelta,
	cfg config.AnalyticsConfig,
) []AnalyticsAnomaly {
	var out []AnalyticsAnomaly

	failRate := 0.0
	if cur.Tools.TotalRequested > 0 {
		failRate = 100 - cur.Tools.SuccessRatePct
	}
	if failRate > cfg.ToolFailRatePct {
		out = append(out, AnalyticsAnomaly{
			Severity: "🔴", Metric: "tool_fail_rate",
			Message:   fmt.Sprintf("Tool fail rate %.1f%% превышает порог %.0f%%", failRate, cfg.ToolFailRatePct),
			Value:     failRate,
			Threshold: cfg.ToolFailRatePct,
		})
	}

	if cur.LLM.ErrorRatePct > cfg.ErrorRatePct {
		out = append(out, AnalyticsAnomaly{
			Severity: "🔴", Metric: "llm_error_rate",
			Message: fmt.Sprintf(
				"LLM error rate %.1f%% превышает порог %.0f%%",
				cur.LLM.ErrorRatePct,
				cfg.ErrorRatePct,
			),
			Value:     cur.LLM.ErrorRatePct,
			Threshold: cfg.ErrorRatePct,
		})
	}

	for _, s := range cur.Subagents {
		if s.ErrorCount > cfg.SubagentErrors {
			out = append(out, AnalyticsAnomaly{
				Severity: "🔴", Metric: "subagent_errors",
				Message:   fmt.Sprintf("%s: %d ошибок за период", s.Component, s.ErrorCount),
				Value:     float64(s.ErrorCount),
				Threshold: float64(cfg.SubagentErrors),
			})
		}
	}

	if cur.LLM.P95ResponseMs > cfg.LatencyP95Ms {
		out = append(out, AnalyticsAnomaly{
			Severity: "🟡", Metric: "latency_p95",
			Message: fmt.Sprintf(
				"P95 latency %dms превышает порог %dms",
				cur.LLM.P95ResponseMs,
				cfg.LatencyP95Ms,
			),
			Value:     float64(cur.LLM.P95ResponseMs),
			Threshold: float64(cfg.LatencyP95Ms),
		})
	}

	if cur.AtomUsage.UnusedPct > cfg.UnusedAtomsPct {
		out = append(out, AnalyticsAnomaly{
			Severity: "🟡", Metric: "unused_atoms",
			Message:   fmt.Sprintf("%.1f%% атомов не использованы", cur.AtomUsage.UnusedPct),
			Value:     cur.AtomUsage.UnusedPct,
			Threshold: cfg.UnusedAtomsPct,
		})
	}

	if cur.Knowledge.TotalAtoms > 0 {
		stalePct := float64(cur.Knowledge.ConfStale) / float64(cur.Knowledge.TotalAtoms) * 100
		if stalePct > cfg.StaleAtomsPct {
			out = append(out, AnalyticsAnomaly{
				Severity: "🟡", Metric: "stale_atoms",
				Message:   fmt.Sprintf("%.1f%% атомов stale (confidence < 0.2)", stalePct),
				Value:     stalePct,
				Threshold: cfg.StaleAtomsPct,
			})
		}
	}

	for name, d := range deltas {
		if math.Abs(d.DeltaPct) > cfg.DeltaSignificantPct {
			out = append(out, AnalyticsAnomaly{
				Severity: "🟡", Metric: name,
				Message:   fmt.Sprintf("%s изменилась на %s%.0f%%", name, d.Direction, math.Abs(d.DeltaPct)),
				Value:     math.Abs(d.DeltaPct),
				Threshold: cfg.DeltaSignificantPct,
			})
		}
	}

	return out
}

func analyticsHasCritical(anomalies []AnalyticsAnomaly) bool {
	for _, a := range anomalies {
		if a.Severity == "🔴" {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Report formatting
// ---------------------------------------------------------------------------

func analyticsFormatReport(r *AnalyticsReport) string {
	var b strings.Builder

	period := fmt.Sprintf("%s–%s",
		r.Current.Period.Start.Format("02.01"),
		r.Current.Period.End.Format("02.01"))

	b.WriteString(fmt.Sprintf("📊 Пика v3 — Аналитика %s (%s)\n\n", r.Mode, period))

	b.WriteString("🔹 LLM\n")
	b.WriteString(fmt.Sprintf("Запросов: %s %s | Токенов: %s %s | $%.2f %s\n",
		analyticsFormatCount(r.Current.LLM.TotalRequests), analyticsDeltaStr(r.Deltas["llm.total_requests"]),
		analyticsFormatCount(int(r.Current.LLM.TotalTokens)), analyticsDeltaStr(r.Deltas["llm.total_tokens"]),
		r.Current.LLM.TotalCostUSD, analyticsDeltaStr(r.Deltas["llm.total_cost_usd"])))

	if len(r.Current.LLM.CostByComponent) > 0 {
		b.WriteString("По компонентам: ")
		first := true
		for _, comp := range analyticsSortedKeys(r.Current.LLM.CostByComponent) {
			if !first {
				b.WriteString(" · ")
			}
			b.WriteString(fmt.Sprintf("%s $%.2f", comp, r.Current.LLM.CostByComponent[comp]))
			first = false
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("Latency avg: %dms %s | p95: %dms %s\n",
		r.Current.LLM.AvgResponseMs, analyticsDeltaStr(r.Deltas["llm.avg_response_ms"]),
		r.Current.LLM.P95ResponseMs, analyticsDeltaStr(r.Deltas["llm.p95_response_ms"])))
	b.WriteString(fmt.Sprintf("Ошибки: %.1f%% %s | Reasoning: %.0f%% %s\n\n",
		r.Current.LLM.ErrorRatePct, analyticsDeltaStr(r.Deltas["llm.error_rate_pct"]),
		r.Current.LLM.ReasoningRatio*100, analyticsDeltaStr(r.Deltas["llm.reasoning_ratio"])))

	b.WriteString("🔹 Tools\n")
	b.WriteString(fmt.Sprintf("Вызовов: %d %s | Успех: %.1f%% %s\n",
		r.Current.Tools.TotalRequested, analyticsDeltaStr(r.Deltas["tools.total_requested"]),
		r.Current.Tools.SuccessRatePct, analyticsDeltaStr(r.Deltas["tools.success_rate_pct"])))
	if len(r.Current.Tools.TopTools) > 0 {
		b.WriteString("Top: ")
		for i, t := range r.Current.Tools.TopTools {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(fmt.Sprintf("%s(%d)", t.Name, t.Count))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString("🔹 Цепочки\n")
	b.WriteString(fmt.Sprintf("Цепочек: %d | Средняя длина: %.1f | Средний расход: $%.2f\n\n",
		r.Current.Chains.TotalChains, r.Current.Chains.AvgChainLength, r.Current.Chains.AvgChainCost))

	b.WriteString("🔹 Субагенты\n")
	for _, s := range r.Current.Subagents {
		marker := ""
		if s.ErrorCount == 0 && s.TimeoutCount == 0 {
			marker = " 🟢"
		}
		b.WriteString(fmt.Sprintf("%s: err %d, timeout %d, avg %dms, p95 %dms%s\n",
			s.Component, s.ErrorCount, s.TimeoutCount, s.AvgDurationMs, s.P95DurationMs, marker))
	}
	b.WriteString("\n")

	k := r.Current.Knowledge
	b.WriteString("🔹 Knowledge\n")
	b.WriteString(fmt.Sprintf("Атомов: %d (+%d)\n", k.TotalAtoms, k.NewInPeriod))
	b.WriteString(fmt.Sprintf("pattern:%d constraint:%d decision:%d tool_pref:%d summary:%d runbook:%d\n",
		k.ByCategory["pattern"], k.ByCategory["constraint"], k.ByCategory["decision"],
		k.ByCategory["tool_pref"], k.ByCategory["summary"], k.ByCategory["runbook_draft"]))
	total := k.TotalAtoms
	if total == 0 {
		total = 1
	}
	b.WriteString(fmt.Sprintf("Confidence: 🟢high %d%% | 🟡mid %d%% | 🔴low %d%% | stale %d%%\n\n",
		k.ConfHigh*100/total, k.ConfMedium*100/total, k.ConfLow*100/total, k.ConfStale*100/total))

	au := r.Current.AtomUsage
	b.WriteString("🔹 Atom Usage\n")
	b.WriteString(fmt.Sprintf("Использований: %d | Эффективность: %.0f%%\n", au.TotalUsages, au.EffectivenessPct))
	if len(au.TopAtoms) > 0 {
		b.WriteString("Top: ")
		for i, a := range au.TopAtoms {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(fmt.Sprintf("%s(%d)", a.Name, a.Count))
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("Неиспользованные: %d (%.1f%%)\n\n", au.UnusedCount, au.UnusedPct))

	if len(r.Current.Tasks) > 0 {
		b.WriteString("🔹 Задачи (top-5 по расходу)\n")
		for _, t := range r.Current.Tasks {
			b.WriteString(fmt.Sprintf("%s: $%.2f avg, %.1f tools, %.0fK tok (%d задач)\n",
				t.TaskTag, t.AvgCost, t.AvgTools, t.AvgTokens/1000, t.RequestCount))
		}
		b.WriteString("\n")
	}

	if len(r.Anomalies) > 0 {
		b.WriteString("⚠️ Аномалии:\n")
		for _, a := range r.Anomalies {
			b.WriteString(fmt.Sprintf("%s %s\n", a.Severity, a.Message))
		}
	}

	return b.String()
}

func analyticsFormatAlert(anomalies []AnalyticsAnomaly) string {
	var b strings.Builder
	b.WriteString("⚠️ Analytics: обнаружены аномалии\n")
	for _, a := range anomalies {
		if a.Severity == "🔴" {
			b.WriteString(fmt.Sprintf("%s %s\n", a.Severity, a.Message))
		}
	}
	b.WriteString("Подробности → канал аналитики")
	return b.String()
}

func analyticsDeltaStr(d AnalyticsDelta) string {
	if d.Direction == "→" {
		return "(→)"
	}
	return fmt.Sprintf("(%s%.0f%%)", d.Direction, math.Abs(d.DeltaPct))
}

func analyticsFormatCount(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func analyticsPercentile(sorted []int, pct int) int {
	sort.Ints(sorted)
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(pct)/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func analyticsSortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// Send report
// ---------------------------------------------------------------------------

func (ae *AnalyticsEngine) sendReport(ctx context.Context, text string) error {
	if ae.sender == nil {
		return ae.fallbackWriteReport(text)
	}
	parts := analyticsSplitMessage(text, ae.cfg.ReportMaxTelegramChars)
	for _, part := range parts {
		_, err := ae.sender.SendMessage(ctx, part)
		if err != nil {
			log.Printf("[analytics] WARN send retry: %v", err)
			_, err = ae.sender.SendMessage(ctx, part)
			if err != nil {
				log.Printf("[analytics] ERROR send failed twice: %v", err)
				return ae.fallbackWriteReport(text)
			}
		}
	}
	return nil
}

func (ae *AnalyticsEngine) fallbackWriteReport(text string) error {
	dir := "/workspace/exports"
	_ = os.MkdirAll(dir, 0o755)
	fname := filepath.Join(dir, fmt.Sprintf("analytics_%s.md", time.Now().Format("2006-01-02_15-04")))
	if err := os.WriteFile(fname, []byte(text), 0o644); err != nil {
		return fmt.Errorf("pika/analytics: fallback write: %w", err)
	}
	log.Printf("[analytics] fallback: report saved to %s", fname)
	return nil
}

func analyticsSplitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	var parts []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			parts = append(parts, text)
			break
		}
		cut := strings.LastIndex(text[:maxLen], "\n\n")
		if cut <= 0 {
			cut = strings.LastIndex(text[:maxLen], "\n")
		}
		if cut <= 0 {
			cut = maxLen
		}
		parts = append(parts, text[:cut])
		text = text[cut:]
	}
	return parts
}

// ---------------------------------------------------------------------------
// Store report in registry
// ---------------------------------------------------------------------------

func (ae *AnalyticsEngine) storeReport(ctx context.Context, report *AnalyticsReport) error {
	data, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("pika/analytics: marshal report: %w", err)
	}

	var key string
	switch report.Mode {
	case AnalyticsWeekly:
		_, week := report.Current.Period.Start.ISOWeek()
		key = fmt.Sprintf("%s%d-%02d", registryKeyWeeklyPfx, report.Current.Period.Start.Year(), week)
	case AnalyticsMonthly:
		key = fmt.Sprintf("%s%s", registryKeyMonthlyPfx, report.Current.Period.Start.Format("2006-01"))
	default:
		key = fmt.Sprintf("analytics_%s", time.Now().Format("2006-01-02"))
	}

	tagsJSON, _ := json.Marshal([]string{"analytics", report.Mode})

	_, upsertErr := ae.mem.UpsertRegistry(ctx, RegistryRow{
		Kind:    registryKindSnapshot,
		Key:     key,
		Summary: fmt.Sprintf("Analytics %s report", report.Mode),
		Data:    data,
		Tags:    tagsJSON,
	})
	if upsertErr != nil {
		return fmt.Errorf("pika/analytics: store report: %w", upsertErr)
	}
	return nil
}
