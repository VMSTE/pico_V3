package pika

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestSessionLifecycle(
	t *testing.T,
) (*BotMemory, *SessionLifecycle) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := Migrate(dbPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("new botmemory: %v", err)
	}
	t.Cleanup(func() { bm.Close() })

	sl := NewSessionLifecycle(bm, SessionConfig{})
	return bm, sl
}

func TestEnsureSession_NewBaseKey(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)

	sid := sl.EnsureSession("tg:12345")

	if sid == "" {
		t.Fatal("expected non-empty session ID")
	}
	if sl.State() != StateActive {
		t.Errorf(
			"state = %q, want active", sl.State(),
		)
	}
	// Format check: "tg:12345:{unix}"
	if len(sid) < len("tg:12345:") {
		t.Errorf("session ID too short: %q", sid)
	}
}

func TestEnsureSession_Repeat(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)

	sid1 := sl.EnsureSession("tg:12345")
	sid2 := sl.EnsureSession("tg:12345")

	if sid1 != sid2 {
		t.Errorf(
			"expected same session, got %q != %q",
			sid1, sid2,
		)
	}
}

func TestEnsureSession_IdleTimeout(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)
	sl.idleTimeoutMin = 1 // 1 minute for test speed

	baseTime := time.Now()
	sl.nowFunc = func() time.Time {
		return baseTime
	}

	sid1 := sl.EnsureSession("tg:12345")

	// Advance time past idle timeout (2 min > 1 min)
	sl.nowFunc = func() time.Time {
		return baseTime.Add(2 * time.Minute)
	}

	sid2 := sl.EnsureSession("tg:12345")

	if sid1 == sid2 {
		t.Error(
			"expected different session after idle",
		)
	}
	if sl.State() != StateActive {
		t.Errorf(
			"state = %q, want active", sl.State(),
		)
	}
}

func TestCheckRotationTriggers_ContextPct(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)
	sl.EnsureSession("tg:1")

	if !sl.CheckRotationTriggers(91.0, 0) {
		t.Error("expected true for contextPct=91")
	}
}

func TestCheckRotationTriggers_ChainCalls(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)
	sl.EnsureSession("tg:1")

	if !sl.CheckRotationTriggers(50.0, 8) {
		t.Error("expected true for chainCalls=8")
	}
}

func TestCheckRotationTriggers_Below(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)
	sl.EnsureSession("tg:1")

	if sl.CheckRotationTriggers(50.0, 3) {
		t.Error(
			"expected false for contextPct=50, chain=3",
		)
	}
}

func TestRotateSession_CallbacksFired(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)
	sl.EnsureSession("tg:42")

	var called []string
	var mu sync.Mutex
	sl.OnRotate(func(sid string) {
		mu.Lock()
		called = append(called, sid)
		mu.Unlock()
	})

	oldSID := sl.SessionID()
	sl.RotateSession()

	mu.Lock()
	defer mu.Unlock()
	if len(called) != 1 {
		t.Fatalf(
			"expected 1 callback, got %d",
			len(called),
		)
	}
	if called[0] != oldSID {
		t.Errorf(
			"callback got %q, want %q",
			called[0], oldSID,
		)
	}
}

func TestRotateSession_NewTimestamp(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)

	ts1 := time.Now()
	sl.nowFunc = func() time.Time { return ts1 }
	sid1 := sl.EnsureSession("tg:100")

	ts2 := ts1.Add(1 * time.Second)
	sl.nowFunc = func() time.Time { return ts2 }
	sid2 := sl.RotateSession()

	if sid1 == sid2 {
		t.Errorf(
			"expected different IDs: %q vs %q",
			sid1, sid2,
		)
	}
	if sl.State() != StateActive {
		t.Errorf(
			"state = %q, want active", sl.State(),
		)
	}
}

func TestCloseSession_StateClosed(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)
	sl.EnsureSession("tg:7")

	sl.CloseSession("user_request")

	if sl.State() != StateClosed {
		t.Errorf(
			"state = %q, want closed", sl.State(),
		)
	}
}

func TestEnsureSession_DBResume(t *testing.T) {
	bm, sl := setupTestSessionLifecycle(t)

	baseTime := time.Now()
	sl.nowFunc = func() time.Time {
		return baseTime
	}

	// Create a session and add a message
	sid := sl.EnsureSession("tg:999")
	_, err := bm.SaveMessage(
		context.Background(),
		MessageRow{
			SessionID: sid,
			TurnID:    1,
			Role:      "user",
			Content:   "hello",
			Tokens:    5,
		},
	)
	if err != nil {
		t.Fatalf("save message: %v", err)
	}

	// Simulate cold restart: new SessionLifecycle
	sl2 := NewSessionLifecycle(bm, SessionConfig{
		IdleTimeoutMin: 30,
	})
	sl2.nowFunc = func() time.Time {
		return baseTime.Add(5 * time.Minute)
	}

	sid2 := sl2.EnsureSession("tg:999")

	if sid2 != sid {
		t.Errorf(
			"expected resume %q, got %q", sid, sid2,
		)
	}
}

func TestSessionFormat(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)

	ts := time.Unix(1714900000, 0)
	sl.nowFunc = func() time.Time { return ts }
	sid := sl.EnsureSession("tg:12345")

	want := "tg:12345:1714900000"
	if sid != want {
		t.Errorf(
			"session ID = %q, want %q", sid, want,
		)
	}
}

func TestTouch_ReactivatesIdle(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)
	sl.EnsureSession("tg:1")

	// Manually set state to idle
	sl.mu.Lock()
	sl.state = StateIdle
	sl.mu.Unlock()

	sl.Touch()

	if sl.State() != StateActive {
		t.Errorf(
			"state = %q, want active after Touch",
			sl.State(),
		)
	}
}

func TestCloseSession_CallbacksFired(t *testing.T) {
	_, sl := setupTestSessionLifecycle(t)
	sl.EnsureSession("tg:50")

	var closedSID string
	sl.OnRotate(func(sid string) {
		closedSID = sid
	})

	oldSID := sl.SessionID()
	sl.CloseSession("idle")

	if closedSID != oldSID {
		t.Errorf(
			"callback got %q, want %q",
			closedSID, oldSID,
		)
	}
}

func TestNewSessionLifecycle_Defaults(t *testing.T) {
	sl := NewSessionLifecycle(nil, SessionConfig{})
	if sl.idleTimeoutMin != 30 {
		t.Errorf(
			"idleTimeoutMin = %d, want 30",
			sl.idleTimeoutMin,
		)
	}
	if sl.rotateThresholdPct != 90 {
		t.Errorf(
			"rotateThresholdPct = %d, want 90",
			sl.rotateThresholdPct,
		)
	}
	if sl.chainMaxCalls != 8 {
		t.Errorf(
			"chainMaxCalls = %d, want 8",
			sl.chainMaxCalls,
		)
	}
}
