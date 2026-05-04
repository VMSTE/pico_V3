package pika

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Trail tests ---

func TestTrailAdd(t *testing.T) {
	tr := NewTrail()
	for i := 0; i < 3; i++ {
		tr.Add(TrailEntry{
			ToolName:   "compose",
			Operation:  "status",
			Result:     "ok",
			OK:         true,
			DurationMs: (i + 1) * 100,
			Timestamp:  time.Now(),
		})
	}

	entries := tr.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// Check oldest→newest order by DurationMs
	if entries[0].DurationMs != 100 {
		t.Errorf("entries[0].DurationMs = %d, want 100", entries[0].DurationMs)
	}
	if entries[2].DurationMs != 300 {
		t.Errorf("entries[2].DurationMs = %d, want 300", entries[2].DurationMs)
	}
}

func TestTrailRingOverflow(t *testing.T) {
	tr := NewTrail()
	for i := 0; i < 7; i++ {
		tr.Add(TrailEntry{
			ToolName:   "tool",
			Operation:  "op",
			DurationMs: i,
			OK:         true,
			Timestamp:  time.Now(),
		})
	}

	entries := tr.Entries()
	if len(entries) != TrailSize {
		t.Fatalf("expected %d entries, got %d", TrailSize, len(entries))
	}
	// Oldest 2 (DurationMs 0, 1) should be gone.
	// Remaining: 2, 3, 4, 5, 6 (oldest→newest).
	if entries[0].DurationMs != 2 {
		t.Errorf("entries[0].DurationMs = %d, want 2", entries[0].DurationMs)
	}
	if entries[4].DurationMs != 6 {
		t.Errorf("entries[4].DurationMs = %d, want 6", entries[4].DurationMs)
	}
}

func TestTrailLoopDetection(t *testing.T) {
	tr := NewTrail()

	// 3 identical calls → loop detected with threshold=3
	for i := 0; i < 3; i++ {
		tr.Add(TrailEntry{
			ToolName:  "compose",
			Operation: "restart",
			OK:        false,
		})
	}
	if !tr.HasLoopDetection(3) {
		t.Error("expected loop detection with 3 identical calls")
	}

	// Add a different call → no loop
	tr.Add(TrailEntry{
		ToolName:  "files",
		Operation: "read",
		OK:        true,
	})
	if tr.HasLoopDetection(3) {
		t.Error("did not expect loop detection after different call")
	}

	// 2 identical + 1 different → no loop for threshold=3
	tr2 := NewTrail()
	tr2.Add(TrailEntry{ToolName: "a", Operation: "x"})
	tr2.Add(TrailEntry{ToolName: "b", Operation: "y"})
	tr2.Add(TrailEntry{ToolName: "b", Operation: "y"})
	if tr2.HasLoopDetection(3) {
		t.Error("expected no loop with only 2 identical")
	}
}

func TestTrailLoopDetectionEdgeCases(t *testing.T) {
	tr := NewTrail()

	// Empty trail → no loop
	if tr.HasLoopDetection(3) {
		t.Error("empty trail should not detect loop")
	}

	// threshold=0 → no loop
	tr.Add(TrailEntry{ToolName: "a", Operation: "b"})
	if tr.HasLoopDetection(0) {
		t.Error("threshold 0 should not detect loop")
	}

	// threshold=1 → always true if there's at least 1 entry
	if !tr.HasLoopDetection(1) {
		t.Error("threshold 1 with 1 entry should detect loop")
	}
}

func TestTrailSerialize(t *testing.T) {
	tr := NewTrail()
	tr.Add(TrailEntry{
		ToolName: "compose", Operation: "restart",
		Result: "ok", OK: true, DurationMs: 230,
	})
	tr.Add(TrailEntry{
		ToolName: "files", Operation: "read",
		Result: "not found", OK: false, DurationMs: 12,
	})

	s := tr.Serialize()
	if !strings.Contains(s, "TRAIL (last 2 tool calls):") {
		t.Errorf("unexpected header: %s", s)
	}
	if !strings.Contains(s, "1. compose.restart → ✅ ok (230ms)") {
		t.Errorf("missing first entry in: %s", s)
	}
	if !strings.Contains(s, "2. files.read → ❌ fail (12ms)") {
		t.Errorf("missing second entry in: %s", s)
	}
}

func TestTrailSerializeEmpty(t *testing.T) {
	tr := NewTrail()
	s := tr.Serialize()
	if !strings.Contains(s, "empty") {
		t.Errorf("expected 'empty' in serialize of empty trail: %s", s)
	}
}

func TestTrailReset(t *testing.T) {
	tr := NewTrail()
	for i := 0; i < 3; i++ {
		tr.Add(TrailEntry{ToolName: "t", Operation: "o"})
	}
	tr.Reset()

	entries := tr.Entries()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after reset, got %d", len(entries))
	}
}

// --- Meta tests ---

func TestMetaIncrement(t *testing.T) {
	m := NewMeta()
	for i := 0; i < 5; i++ {
		m.IncrementMsgCount()
	}
	if m.GetMsgCount() != 5 {
		t.Errorf("MsgCount = %d, want 5", m.GetMsgCount())
	}
}

func TestMetaContextPct(t *testing.T) {
	m := NewMeta()
	m.UpdateContextPct(128000, 256000)
	got := m.GetContextPct()
	if got != 50.0 {
		t.Errorf("ContextPct = %f, want 50.0", got)
	}

	// Zero context window → 0%
	m.UpdateContextPct(100, 0)
	if m.GetContextPct() != 0 {
		t.Error("expected 0% for zero context window")
	}
}

func TestMetaSerialize(t *testing.T) {
	m := NewMeta()
	m.IncrementMsgCount()
	m.UpdateContextPct(45000, 256000)

	s := m.Serialize()
	if !strings.Contains(s, "MSG_COUNT: 1") {
		t.Errorf("missing MSG_COUNT in: %s", s)
	}
	if !strings.Contains(s, "CONTEXT_PCT: 17.6%") {
		t.Errorf("missing CONTEXT_PCT in: %s", s)
	}
	if !strings.Contains(s, "HEALTH: healthy") {
		t.Errorf("missing HEALTH in: %s", s)
	}
	if !strings.Contains(s, "LAST_FAIL: —") {
		t.Errorf("missing LAST_FAIL in: %s", s)
	}
}

func TestMetaSerializeDegraded(t *testing.T) {
	m := NewMeta()
	m.SetHealth(StateDegraded)
	m.RecordFail()

	s := m.Serialize()
	if !strings.Contains(s, "⚠️ degraded") {
		t.Errorf("missing degraded icon in: %s", s)
	}
	if !strings.Contains(s, "ago") {
		t.Errorf("expected 'ago' for recent fail in: %s", s)
	}
}

func TestMetaSerializeOffline(t *testing.T) {
	m := NewMeta()
	m.SetHealth(StateOffline)

	s := m.Serialize()
	if !strings.Contains(s, "🔴 offline") {
		t.Errorf("missing offline icon in: %s", s)
	}
}

func TestMetaReset(t *testing.T) {
	m := NewMeta()
	for i := 0; i < 5; i++ {
		m.IncrementMsgCount()
	}
	m.UpdateContextPct(100000, 256000)
	m.SetHealth(StateDegraded)
	m.RecordFail()

	m.Reset()

	// MsgCount and ContextPct are reset
	if m.GetMsgCount() != 0 {
		t.Errorf("MsgCount = %d after reset, want 0", m.GetMsgCount())
	}
	if m.GetContextPct() != 0 {
		t.Errorf("ContextPct = %f after reset, want 0", m.GetContextPct())
	}

	// Health and LastFail are NOT reset
	if m.GetHealth().Status != StateDegraded.Status {
		t.Errorf(
			"Health = %s after reset, want degraded",
			m.GetHealth().Status,
		)
	}
	m.mu.RLock()
	if m.LastFail == nil {
		t.Error("LastFail should NOT be reset")
	}
	m.mu.RUnlock()
}

// --- Concurrency tests ---

func TestConcurrencyTrail(t *testing.T) {
	tr := NewTrail()
	var wg sync.WaitGroup

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				tr.Add(TrailEntry{
					ToolName:   "tool",
					Operation:  "op",
					DurationMs: id*100 + i,
					OK:         true,
				})
				_ = tr.Entries()
				_ = tr.Serialize()
				_ = tr.HasLoopDetection(3)
			}
		}(g)
	}
	wg.Wait()

	// Just verify no panic and entries are valid
	entries := tr.Entries()
	if len(entries) != TrailSize {
		t.Errorf(
			"expected %d entries after concurrent writes, got %d",
			TrailSize, len(entries),
		)
	}
}

func TestConcurrencyMeta(t *testing.T) {
	m := NewMeta()
	var wg sync.WaitGroup

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				m.IncrementMsgCount()
				m.UpdateContextPct(i*1000, 256000)
				m.SetHealth(StateHealthy)
				m.RecordFail()
				_ = m.Serialize()
				_ = m.GetContextPct()
				_ = m.GetHealth()
				_ = m.GetMsgCount()
			}
		}()
	}
	wg.Wait()

	// 10 goroutines * 50 increments = 500
	if m.GetMsgCount() != 500 {
		t.Errorf("MsgCount = %d, want 500", m.GetMsgCount())
	}
}
