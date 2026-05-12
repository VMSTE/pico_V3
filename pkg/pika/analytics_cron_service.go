// PIKA-V3: analytics_cron_service.go — CronService wiring for Analytics.
// Replaces the custom ticker in analytics_cron.go with upstream CronService.
// Pattern mirrors reflector_cron.go. Decision: ТЗ-v2-8h.

package pika

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
)

const analyticsJobPrefix = "analytics:"

// RegisterAnalyticsJobs registers weekly and monthly analytics jobs
// in the upstream CronService. Empty schedule fields are skipped.
func RegisterAnalyticsJobs(
	cronSvc *cron.CronService,
	engine *AnalyticsEngine,
	weekly, monthly string,
) error {
	type spec struct {
		name string
		raw  string
		mode string
	}
	specs := []spec{
		{"analytics-weekly", weekly, AnalyticsWeekly},
		{"analytics-monthly", monthly, AnalyticsMonthly},
	}

	var registered int
	for _, s := range specs {
		if s.raw == "" {
			continue
		}
		// Reuse schedToCronExpr: AnalyticsWeekly=="weekly"==ReflectorWeekly.
		expr, err := schedToCronExpr(s.raw, s.mode)
		if err != nil {
			return fmt.Errorf(
				"pika/analytics_cron: invalid %s schedule %q: %w",
				s.mode, s.raw, err,
			)
		}
		_, addErr := cronSvc.AddJob(
			s.name,
			cron.CronSchedule{
				Kind: "cron",
				Expr: expr,
			},
			analyticsJobPrefix+s.mode,
			"", "",
		)
		if addErr != nil {
			return fmt.Errorf(
				"pika/analytics_cron: add %s: %w",
				s.name, addErr,
			)
		}
		registered++
	}

	if registered > 0 {
		log.Printf("[analytics] registered %d cron jobs", registered)
	}
	return nil
}

// HandleAnalyticsJob checks if a cron job is an analytics job
// and executes engine.Run. Returns (handled, error).
func HandleAnalyticsJob(
	engine *AnalyticsEngine,
	job *cron.CronJob,
) (bool, error) {
	if !strings.HasPrefix(job.Payload.Message, analyticsJobPrefix) {
		return false, nil
	}
	mode := strings.TrimPrefix(job.Payload.Message, analyticsJobPrefix)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Minute,
	)
	defer cancel()

	log.Printf("[analytics] executing cron job: mode=%s", mode)
	return true, engine.Run(ctx, mode)
}
