// PIKA-V3: Analytics pipeline configuration types.
// Separate file to minimize merge conflicts with config_pika.go.

package config

// AnalyticsConfig defines analytics pipeline settings.
type AnalyticsConfig struct {
	Enabled    bool              `json:"enabled"`
	QueriesDir string            `json:"queries_dir"`
	Schedule   AnalyticsSchedule `json:"schedule"`

	// Alert thresholds. PIKA-V3: ТЗ-v2-8h.
	ToolFailRatePct     float64 `json:"tool_fail_rate_pct,omitempty"`
	ErrorRatePct        float64 `json:"error_rate_pct,omitempty"`
	LatencyP95Ms        int     `json:"latency_p95_ms,omitempty"`
	UnusedAtomsPct      float64 `json:"unused_atoms_pct,omitempty"`
	StaleAtomsPct       float64 `json:"stale_atoms_pct,omitempty"`
	SubagentErrors      int     `json:"subagent_errors,omitempty"`
	DeltaSignificantPct float64 `json:"delta_significant_pct,omitempty"`

	// Report limits.
	ReportMaxTelegramChars int `json:"report_max_telegram_chars,omitempty"`
	TopToolsLimit          int `json:"top_tools_limit,omitempty"`
	TopAtomsLimit          int `json:"top_atoms_limit,omitempty"`
	TopTasksLimit          int `json:"top_tasks_limit,omitempty"`

	// Delivery.
	DisableTelegramReports bool `json:"disable_telegram_reports"` // true = reports only in DB (web), not Telegram. Alerts always go to chat.
}

// AnalyticsSchedule defines analytics cron schedule.
type AnalyticsSchedule struct {
	Weekly  string `json:"weekly"`
	Monthly string `json:"monthly"`
}

// DefaultAnalyticsConfig returns production defaults for analytics.
func DefaultAnalyticsConfig() AnalyticsConfig {
	return AnalyticsConfig{
		Enabled:    true,
		QueriesDir: "/workspace/queries",
		Schedule: AnalyticsSchedule{
			Weekly:  "Sun 06:00",
			Monthly: "1st 07:00",
		},
		ToolFailRatePct:        10.0,
		ErrorRatePct:           5.0,
		LatencyP95Ms:           15000,
		UnusedAtomsPct:         20.0,
		StaleAtomsPct:          10.0,
		SubagentErrors:         5,
		DeltaSignificantPct:    50.0,
		ReportMaxTelegramChars: 4000,
		TopToolsLimit:          10,
		TopAtomsLimit:          10,
		TopTasksLimit:          5,
	}
}
