package pika

import (
	"testing"
	"time"
)

func TestNewAnalyticsCron_Defaults(t *testing.T) {
	// nil engine is safe for construction (panics only on Run)
	ac := NewAnalyticsCron(nil, 0, 0)
	if ac.weekly != 7*24*time.Hour {
		t.Errorf("expected weekly=168h, got %v", ac.weekly)
	}
	if ac.monthly != 30*24*time.Hour {
		t.Errorf("expected monthly=720h, got %v", ac.monthly)
	}
}

func TestNewAnalyticsCron_CustomIntervals(t *testing.T) {
	ac := NewAnalyticsCron(nil, 2*time.Hour, 5*time.Hour)
	if ac.weekly != 2*time.Hour {
		t.Errorf("expected weekly=2h, got %v", ac.weekly)
	}
	if ac.monthly != 5*time.Hour {
		t.Errorf("expected monthly=5h, got %v", ac.monthly)
	}
}

func TestAnalyticsCron_StartStop(t *testing.T) {
	ac := NewAnalyticsCron(nil, time.Hour, time.Hour)
	ac.Start()
	// Double stop should not panic.
	ac.Stop()
	ac.Stop()
}
