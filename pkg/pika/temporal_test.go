package pika

import (
	"testing"
	"time"
)

func TestParseTemporalWindow(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		q             string
		after, before string
		asc, ok       bool
	}{
		{"что мы делали в самом начале, в мае", "2026-05-01", "2026-06-01", true, true},
		{"в начале июня", "2026-06-01", "2026-07-01", true, true},
		{"март 2026", "2026-03-01", "2026-04-01", false, true},
		{"что было в конце сентября", "2026-09-01", "2026-10-01", false, true},
		{"просто вопрос без дат", "", "", false, false},
	}
	for _, c := range cases {
		a, b, asc, ok := ParseTemporalWindow(c.q, now)
		if a != c.after || b != c.before || asc != c.asc || ok != c.ok {
			t.Errorf("%q: got (%s %s %v %v), want (%s %s %v %v)",
				c.q, a, b, asc, ok, c.after, c.before, c.asc, c.ok)
		}
	}
}
