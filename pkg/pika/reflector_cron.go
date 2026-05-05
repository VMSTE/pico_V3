// PIKA-V3: reflector_cron.go — Cron wiring for Reflector.
// Registers 3 scheduled jobs in upstream CronService.
// Decision: D-134.

package pika

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
)

const reflectorJobPrefix = "reflector:"

// RegisterReflectorJobs reads schedule config and registers
// 3 cron jobs in upstream CronService.
// Empty/absent schedule → skip, no error.
func RegisterReflectorJobs(
	cronSvc *cron.CronService,
	pipeline *ReflectorPipeline,
	sched ReflectorSchedule,
) error {
	type spec struct {
		name string
		raw  string
		mode string
	}
	specs := []spec{
		{"reflector-daily", sched.Daily, ReflectorDaily},
		{"reflector-weekly", sched.Weekly, ReflectorWeekly},
		{"reflector-monthly", sched.Monthly, ReflectorMonthly},
	}

	var registered int
	for _, s := range specs {
		if s.raw == "" {
			continue
		}
		expr, err := schedToCronExpr(s.raw, s.mode)
		if err != nil {
			return fmt.Errorf(
				"pika/reflector_cron: invalid %s schedule "+
					"%q: %w",
				s.mode, s.raw, err,
			)
		}
		_, addErr := cronSvc.AddJob(
			s.name,
			cron.CronSchedule{
				Kind: "cron",
				Expr: expr,
			},
			reflectorJobPrefix+s.mode,
			"", "",
		)
		if addErr != nil {
			return fmt.Errorf(
				"pika/reflector_cron: add %s: %w",
				s.name, addErr,
			)
		}
		registered++
	}

	if registered > 0 {
		log.Printf(
			"[reflector] registered %d cron jobs",
			registered,
		)
	}
	return nil
}

// HandleReflectorJob checks if a cron job is a reflector
// job and executes pipeline.Run. Returns (handled, error).
func HandleReflectorJob(
	pipeline *ReflectorPipeline,
	job *cron.CronJob,
) (bool, error) {
	if !strings.HasPrefix(
		job.Payload.Message, reflectorJobPrefix,
	) {
		return false, nil
	}
	mode := strings.TrimPrefix(
		job.Payload.Message, reflectorJobPrefix,
	)

	timeoutMs := pipeline.cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 120000
	}
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(timeoutMs)*time.Millisecond,
	)
	defer cancel()

	log.Printf(
		"[reflector] executing cron job: mode=%s", mode,
	)
	return true, pipeline.Run(ctx, mode)
}

// schedToCronExpr converts schedule string to cron expr.
// Formats: "HH:MM" (daily), "Sun HH:MM" (weekly),
// "1st HH:MM" (monthly).
func schedToCronExpr(
	raw, mode string,
) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty schedule")
	}

	switch mode {
	case ReflectorDaily:
		// "03:00" → "0 3 * * *"
		h, m, err := parseTime(raw)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d %d * * *", m, h), nil

	case ReflectorWeekly:
		// "Sun 04:00" → "0 4 * * 0"
		parts := strings.Fields(raw)
		if len(parts) != 2 {
			return "", fmt.Errorf(
				"expected 'Day HH:MM', got %q", raw,
			)
		}
		dow, err := parseDayOfWeek(parts[0])
		if err != nil {
			return "", err
		}
		h, m, err := parseTime(parts[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(
			"%d %d * * %d", m, h, dow,
		), nil

	case ReflectorMonthly:
		// "1st 05:00" → "0 5 1 * *"
		parts := strings.Fields(raw)
		if len(parts) != 2 {
			return "", fmt.Errorf(
				"expected 'Nth HH:MM', got %q", raw,
			)
		}
		dom, err := parseDayOfMonth(parts[0])
		if err != nil {
			return "", err
		}
		h, m, err := parseTime(parts[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(
			"%d %d %d * *", m, h, dom,
		), nil

	default:
		return "", fmt.Errorf("unknown mode %q", mode)
	}
}

func parseTime(s string) (int, int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf(
			"expected HH:MM, got %q", s,
		)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf(
			"invalid hour in %q", s,
		)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf(
			"invalid minute in %q", s,
		)
	}
	return h, m, nil
}

var dayOfWeekMap = map[string]int{
	"sun": 0, "sunday": 0,
	"mon": 1, "monday": 1,
	"tue": 2, "tuesday": 2,
	"wed": 3, "wednesday": 3,
	"thu": 4, "thursday": 4,
	"fri": 5, "friday": 5,
	"sat": 6, "saturday": 6,
}

func parseDayOfWeek(s string) (int, error) {
	d, ok := dayOfWeekMap[strings.ToLower(s)]
	if !ok {
		return 0, fmt.Errorf(
			"unknown day of week %q", s,
		)
	}
	return d, nil
}

func parseDayOfMonth(s string) (int, error) {
	s = strings.TrimRight(
		strings.ToLower(s), "stndrh",
	)
	d, err := strconv.Atoi(s)
	if err != nil || d < 1 || d > 31 {
		return 0, fmt.Errorf(
			"invalid day of month %q", s,
		)
	}
	return d, nil
}
