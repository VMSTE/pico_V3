// PIKA-V3: analytics_cron.go — Cron wiring for Analytics.
// Registers 2 scheduled jobs (weekly + monthly) in upstream CronService.
// Pattern mirrors reflector_cron.go.

package pika

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/cron"
)

const analyticsJobPrefix = "analytics:"

// RegisterAnalyticsJobs reads analytics schedule config and registers
// cron jobs in upstream CronService.
func RegisterAnalyticsJobs(
	cronSvc *cron.CronService,
	engine *AnalyticsEngine,
	sched config.AnalyticsSchedule,
) error {
	type spec struct {
		name string
		raw  string
		mode string
	}
	specs := []spec{
		{"analytics-weekly", sched.Weekly, AnalyticsWeekly},
		{"analytics-monthly", sched.Monthly, AnalyticsMonthly},
	}

	var registered int
	for _, s := range specs {
		if s.raw == "" {
			continue
		}
		expr, err := schedToCronExpr(s.raw, mapAnalyticsMode(s.mode))
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

// HandleAnalyticsJob checks if a cron job is an analytics
// job and executes engine.Run. Returns (handled, error).
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
		5*time.Minute,
	)
	defer cancel()

	log.Printf("[analytics] executing cron job: mode=%s", mode)
	return true, engine.Run(ctx, mode)
}

// mapAnalyticsMode maps analytics mode to reflector mode for schedToCronExpr reuse.
func mapAnalyticsMode(mode string) string {
	switch mode {
	case AnalyticsWeekly:
		return ReflectorWeekly
	case AnalyticsMonthly:
		return ReflectorMonthly
	default:
		return mode
	}
}
