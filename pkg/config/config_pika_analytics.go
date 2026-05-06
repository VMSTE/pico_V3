// PIKA-V3: Analytics pipeline configuration types.
// Separate file to minimize merge conflicts with config_pika.go.

package config

// AnalyticsConfig defines analytics pipeline settings.
type AnalyticsConfig struct {
	Enabled    bool              `json:"enabled"`
	QueriesDir string            `json:"queries_dir"`
	Schedule   AnalyticsSchedule `json:"schedule"`
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
	}
}
