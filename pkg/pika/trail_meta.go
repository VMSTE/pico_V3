package pika

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// PIKA-V3: TRAIL ring buffer + META system metrics.
// In-memory only. No DB, no I/O. Serialized into system prompt text.

// TrailSize — maximum number of entries in the TRAIL ring buffer.
const TrailSize = 5

// TrailEntry — one record in the TRAIL ring buffer.
type TrailEntry struct {
	ToolName   string
	Operation  string // e.g. "restart", "status"
	Result     string // short result (≤100 chars)
	OK         bool   // success flag
	DurationMs int    // execution time in ms
	Timestamp  time.Time
}

// Trail — ring buffer of the last TrailSize tool calls.
// Thread-safe via sync.RWMutex.
type Trail struct {
	entries [TrailSize]TrailEntry
	count   int // total adds (used for position calc)
	mu      sync.RWMutex
}

// NewTrail creates an empty Trail ring buffer.
func NewTrail() *Trail {
	return &Trail{}
}

// Add appends an entry to the ring buffer.
// If the buffer is full, the oldest entry is overwritten.
func (t *Trail) Add(entry TrailEntry) {
	t.mu.Lock()
	pos := t.count % TrailSize
	t.entries[pos] = entry
	t.count++
	t.mu.Unlock()
}

// Entries returns entries from oldest to newest.
// Empty slots are excluded.
func (t *Trail) Entries() []TrailEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.count == 0 {
		return nil
	}

	n := t.count
	if n > TrailSize {
		n = TrailSize
	}

	result := make([]TrailEntry, n)
	if t.count <= TrailSize {
		// Buffer not yet full — entries are [0..count-1]
		for i := 0; i < n; i++ {
			result[i] = t.entries[i]
		}
	} else {
		// Buffer overflowed — oldest is at (count % TrailSize)
		start := t.count % TrailSize
		for i := 0; i < n; i++ {
			result[i] = t.entries[(start+i)%TrailSize]
		}
	}

	return result
}

// HasLoopDetection checks the last `threshold` entries:
// if all have the same ToolName+Operation → returns true.
func (t *Trail) HasLoopDetection(threshold int) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if threshold <= 0 || t.count < threshold {
		return false
	}

	n := t.count
	if n > TrailSize {
		n = TrailSize
	}
	if threshold > n {
		return false
	}

	// Get the newest entry
	newestIdx := (t.count - 1) % TrailSize
	refName := t.entries[newestIdx].ToolName
	refOp := t.entries[newestIdx].Operation

	// Check last `threshold` entries from newest backwards
	for i := 0; i < threshold; i++ {
		idx := (t.count - 1 - i) % TrailSize
		if idx < 0 {
			idx += TrailSize
		}
		e := t.entries[idx]
		if e.ToolName != refName || e.Operation != refOp {
			return false
		}
	}

	return true
}

// Serialize returns a text representation for the system prompt.
// Format:
//
//	TRAIL (last N tool calls):
//	1. compose.restart → ✅ ok (230ms)
//	2. compose.status → ❌ fail (1200ms)
func (t *Trail) Serialize() string {
	entries := t.Entries()
	if len(entries) == 0 {
		return "TRAIL (last 0 tool calls): empty"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "TRAIL (last %d tool calls):\n", len(entries))

	for i, e := range entries {
		statusIcon := "✅"
		statusText := "ok"
		if !e.OK {
			statusIcon := "❌"
			statusText = "fail"
			_ = statusIcon
		}
		fmt.Fprintf(
			&sb, "%d. %s.%s → %s %s (%dms)\n",
			i+1, e.ToolName, e.Operation,
			statusIcon, statusText, e.DurationMs,
		)
	}

	return strings.TrimRight(sb.String(), "\n")
}

// Reset clears the ring buffer. Called on session rotation.
func (t *Trail) Reset() {
	t.mu.Lock()
	t.entries = [TrailSize]TrailEntry{}
	t.count = 0
	t.mu.Unlock()
}

// --- META: system metrics ---
// SystemState type and constants (StateHealthy, StateDegraded, StateOffline)
// are defined in interfaces.go.

// Meta — system metrics, updated after each API response.
// Thread-safe via sync.RWMutex.
type Meta struct {
	MsgCount   int         // message counter in session
	ContextPct float64     // context window usage %
	Health     SystemState // healthy / degraded / offline
	LastFail   *time.Time  // last fail time (nil if none)
	mu         sync.RWMutex
}

// NewMeta creates Meta with healthy defaults.
func NewMeta() *Meta {
	return &Meta{
		Health: StateHealthy,
	}
}

// IncrementMsgCount increases the message counter.
func (m *Meta) IncrementMsgCount() {
	m.mu.Lock()
	m.MsgCount++
	m.mu.Unlock()
}

// UpdateContextPct recalculates context usage percentage.
// Called after each LLM response with usage data.
func (m *Meta) UpdateContextPct(usedTokens, contextWindow int) {
	m.mu.Lock()
	if contextWindow > 0 {
		m.ContextPct = float64(usedTokens) /
			float64(contextWindow) * 100.0
	} else {
		m.ContextPct = 0
	}
	m.mu.Unlock()
}

// SetHealth sets the system health state.
func (m *Meta) SetHealth(state SystemState) {
	m.mu.Lock()
	m.Health = state
	m.mu.Unlock()
}

// RecordFail records the time of the last failure.
func (m *Meta) RecordFail() {
	m.mu.Lock()
	now := time.Now()
	m.LastFail = &now
	m.mu.Unlock()
}

// GetContextPct returns the current context usage percentage.
func (m *Meta) GetContextPct() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ContextPct
}

// GetHealth returns the current system health state.
func (m *Meta) GetHealth() SystemState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Health
}

// GetMsgCount returns the current message count.
func (m *Meta) GetMsgCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.MsgCount
}

// Serialize returns a text representation for the system prompt.
// Format:
//
//	META:
//	MSG_COUNT: 12
//	CONTEXT_PCT: 34.2%
//	HEALTH: healthy
//	LAST_FAIL: —
func (m *Meta) Serialize() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	healthStr := m.Health.Status
	if m.Health == StateDegraded {
		healthStr = "⚠️ degraded"
	} else if m.Health == StateOffline {
		healthStr = "🔴 offline"
	}

	lastFailStr := "—"
	if m.LastFail != nil {
		ago := time.Since(*m.LastFail)
		switch {
		case ago < time.Minute:
			lastFailStr = fmt.Sprintf("%ds ago", int(ago.Seconds()))
		case ago < time.Hour:
			lastFailStr = fmt.Sprintf("%dm ago", int(ago.Minutes()))
		default:
			lastFailStr = fmt.Sprintf("%dh ago", int(ago.Hours()))
		}
	}

	return fmt.Sprintf(
		"META:\nMSG_COUNT: %d\nCONTEXT_PCT: %.1f%%\nHEALTH: %s\nLAST_FAIL: %s",
		m.MsgCount, m.ContextPct, healthStr, lastFailStr,
	)
}

// Reset resets session-scoped metrics.
// Health and LastFail are NOT reset — they are system-wide.
func (m *Meta) Reset() {
	m.mu.Lock()
	m.MsgCount = 0
	m.ContextPct = 0
	m.mu.Unlock()
}
