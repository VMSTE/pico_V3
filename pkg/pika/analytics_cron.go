package pika

import (
	"context"
	"log"
	"sync"
	"time"
)

// AnalyticsCron runs analytics engine on a periodic schedule.
// PIKA-V3: D-136a checkpoint F17, TZ-v2-8i.
type AnalyticsCron struct {
	engine  *AnalyticsEngine
	weekly  time.Duration
	monthly time.Duration
	stopCh  chan struct{}
	once    sync.Once
}

// NewAnalyticsCron creates a cron scheduler for analytics.
func NewAnalyticsCron(engine *AnalyticsEngine, weekly, monthly time.Duration) *AnalyticsCron {
	if weekly <= 0 {
		weekly = 7 * 24 * time.Hour
	}
	if monthly <= 0 {
		monthly = 30 * 24 * time.Hour
	}
	return &AnalyticsCron{
		engine:  engine,
		weekly:  weekly,
		monthly: monthly,
		stopCh:  make(chan struct{}),
	}
}

// Start launches weekly and monthly goroutines.
func (ac *AnalyticsCron) Start() {
	go ac.loop("weekly", ac.weekly)
	go ac.loop("monthly", ac.monthly)
	log.Println("[analytics] cron started")
}

// Stop halts all analytics goroutines.
func (ac *AnalyticsCron) Stop() {
	ac.once.Do(func() { close(ac.stopCh) })
}

func (ac *AnalyticsCron) loop(mode string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if err := ac.engine.Run(ctx, mode); err != nil {
				log.Printf("[analytics] %s run failed: %v", mode, err)
			}
			cancel()
		case <-ac.stopCh:
			return
		}
	}
}
