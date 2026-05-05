// PIKA-V3: SessionLifecycle — in-memory FSM for session lifecycle.
// Manages session ensure/start/close/rotate + idle detection +
// rotation triggers. No sessions table — session_id is a grouping
// key in messages/events/request_log.
//
// FSM states:
//   active → idle (по idle_timeout_min)
//   idle → active (новое сообщение)
//   active → rotating (триггер ротации)
//   idle → rotating (idle timeout истёк)
//   rotating → active (новая сессия стартована)
//   active/idle → closed (явное закрытие)

package pika

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

// SessionState represents the FSM state of a session.
type SessionState string

const (
	StateActive   SessionState = "active"
	StateIdle     SessionState = "idle"
	StateRotating SessionState = "rotating"
	StateClosed   SessionState = "closed"
)

// RotateCallback is invoked when a session rotates or closes.
// Receives the old session ID that is being closed/rotated.
type RotateCallback func(sessionID string)

// SessionConfig holds configuration for SessionLifecycle.
type SessionConfig struct {
	IdleTimeoutMin     int // default 30
	RotateThresholdPct int // default 90
	ChainMaxCalls      int // default 8
}

// SessionLifecycle manages the lifecycle of a single chat
// session. Thread-safe via sync.Mutex on all public methods.
type SessionLifecycle struct {
	mu           sync.Mutex
	state        SessionState
	sessionID    string
	baseKey      string
	startedAt    time.Time
	lastActivity time.Time

	// Config
	idleTimeoutMin     int
	rotateThresholdPct int
	chainMaxCalls      int

	// Dependencies
	botmem   *BotMemory
	onRotate []RotateCallback

	// nowFunc returns current time. Defaults to time.Now.
	// Override in tests for deterministic behavior.
	nowFunc func() time.Time
}

// NewSessionLifecycle creates a SessionLifecycle. Applies
// defaults for zero-value config fields.
func NewSessionLifecycle(
	botmem *BotMemory, cfg SessionConfig,
) *SessionLifecycle {
	if cfg.IdleTimeoutMin <= 0 {
		cfg.IdleTimeoutMin = 30
	}
	if cfg.RotateThresholdPct <= 0 {
		cfg.RotateThresholdPct = 90
	}
	if cfg.ChainMaxCalls <= 0 {
		cfg.ChainMaxCalls = 8
	}
	return &SessionLifecycle{
		state:              StateClosed,
		botmem:             botmem,
		idleTimeoutMin:     cfg.IdleTimeoutMin,
		rotateThresholdPct: cfg.RotateThresholdPct,
		chainMaxCalls:      cfg.ChainMaxCalls,
	}
}

// now returns the current time, using nowFunc if set.
func (sl *SessionLifecycle) now() time.Time {
	if sl.nowFunc != nil {
		return sl.nowFunc()
	}
	return time.Now()
}

// OnRotate registers a callback invoked on session rotation
// or close. Callbacks are invoked in registration order.
func (sl *SessionLifecycle) OnRotate(cb RotateCallback) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.onRotate = append(sl.onRotate, cb)
}

// EnsureSession returns the current session ID, resuming from
// DB or creating a new one if needed.
//
// Algorithm:
//  1. baseKey = "tg:{chatID}"
//  2. If active/idle session for same baseKey → check idle
//     timeout. If expired → rotate. Otherwise return current.
//  3. Query DB: SELECT session_id FROM messages
//     WHERE session_id LIKE '<baseKey>:%' ORDER BY id DESC
//  4. If found and idle timeout not expired → resume.
//  5. Otherwise → StartSession(baseKey).
func (sl *SessionLifecycle) EnsureSession(
	baseKey string,
) string {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	// Step 2: reuse active/idle session for same baseKey
	if sl.sessionID != "" && sl.baseKey == baseKey &&
		(sl.state == StateActive ||
			sl.state == StateIdle) {
		if sl.isIdleLocked() {
			sl.rotateLocked()
			return sl.sessionID
		}
		sl.state = StateActive
		return sl.sessionID
	}

	// Step 3-4: DB recovery for baseKey
	if sl.botmem != nil {
		sid, lastTs := sl.findLastSessionLocked(baseKey)
		if sid != "" {
			elapsed := sl.now().Sub(lastTs)
			limit := time.Duration(
				sl.idleTimeoutMin,
			) * time.Minute
			if elapsed <= limit {
				sl.sessionID = sid
				sl.baseKey = baseKey
				sl.state = StateActive
				sl.startedAt = lastTs
				sl.lastActivity = sl.now()
				log.Printf(
					"pika/session: resumed %q "+
						"(idle %v)",
					sid,
					elapsed.Round(time.Second),
				)
				return sl.sessionID
			}
		}
	}

	// Step 5: new session
	return sl.startSessionLocked(baseKey)
}

// StartSession creates a new session. Format:
// "{baseKey}:{unixTimestamp}".
func (sl *SessionLifecycle) StartSession(
	baseKey string,
) string {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.startSessionLocked(baseKey)
}

func (sl *SessionLifecycle) startSessionLocked(
	baseKey string,
) string {
	now := sl.now()
	sl.sessionID = fmt.Sprintf(
		"%s:%d", baseKey, now.Unix(),
	)
	sl.baseKey = baseKey
	sl.state = StateActive
	sl.startedAt = now
	sl.lastActivity = now
	log.Printf(
		"pika/session: started %q", sl.sessionID,
	)
	return sl.sessionID
}

// CloseSession closes the current session with a reason.
func (sl *SessionLifecycle) CloseSession(reason string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.closeLocked(reason)
}

func (sl *SessionLifecycle) closeLocked(reason string) {
	if sl.state == StateClosed {
		return
	}
	oldID := sl.sessionID
	sl.state = StateClosed
	log.Printf(
		"pika/session: closed %q reason=%s",
		oldID, reason,
	)
	sl.fireCallbacksLocked(oldID)
}

// RotateSession closes the current session and starts a new
// one. Returns the new session ID.
func (sl *SessionLifecycle) RotateSession() string {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.rotateLocked()
	return sl.sessionID
}

func (sl *SessionLifecycle) rotateLocked() {
	oldID := sl.sessionID
	sl.state = StateRotating
	log.Printf(
		"pika/session: rotating %q", oldID,
	)
	sl.fireCallbacksLocked(oldID)
	sl.startSessionLocked(sl.baseKey)
}

// CheckRotationTriggers returns true if any rotation trigger
// is met: contextPct >= threshold OR chainCalls >= max.
// Idle timeout is checked lazily in EnsureSession.
func (sl *SessionLifecycle) CheckRotationTriggers(
	contextPct float64, chainCalls int,
) bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if contextPct >= float64(sl.rotateThresholdPct) {
		return true
	}
	if chainCalls >= sl.chainMaxCalls {
		return true
	}
	return false
}

// Touch updates lastActivity to now. Call on every incoming
// message to keep the session alive.
func (sl *SessionLifecycle) Touch() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.lastActivity = sl.now()
	if sl.state == StateIdle {
		sl.state = StateActive
	}
}

// State returns the current FSM state.
func (sl *SessionLifecycle) State() SessionState {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.state
}

// SessionID returns the current session ID.
func (sl *SessionLifecycle) SessionID() string {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.sessionID
}

// IsIdle returns true if lastActivity exceeds idle timeout.
func (sl *SessionLifecycle) IsIdle() bool {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.isIdleLocked()
}

func (sl *SessionLifecycle) isIdleLocked() bool {
	if sl.lastActivity.IsZero() {
		return false
	}
	return sl.now().Sub(sl.lastActivity) >
		time.Duration(sl.idleTimeoutMin)*time.Minute
}

// fireCallbacksLocked invokes all registered onRotate
// callbacks. Panics in callbacks are recovered and logged.
// Must be called with sl.mu held.
func (sl *SessionLifecycle) fireCallbacksLocked(
	oldSessionID string,
) {
	for _, cb := range sl.onRotate {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf(
						"pika/session: "+
							"callback panic: %v", r,
					)
				}
			}()
			cb(oldSessionID)
		}()
	}
}

// findLastSessionLocked queries BotMemory for the most recent
// session matching baseKey prefix. Returns session_id and last
// message timestamp.
func (sl *SessionLifecycle) findLastSessionLocked(
	baseKey string,
) (string, time.Time) {
	ctx, cancel := context.WithTimeout(
		context.Background(), 2*time.Second,
	)
	defer cancel()

	sid, ts, err := sl.botmem.GetLastSessionByPrefix(
		ctx, baseKey+":%",
	)
	if err != nil {
		log.Printf(
			"pika/session: find last session %q: %v",
			baseKey, err,
		)
		return "", time.Time{}
	}
	return sid, ts
}

// --- BotMemory extension ---

// GetLastSessionByPrefix returns the most recent session_id
// and its last message timestamp matching a LIKE pattern.
// Returns ("", zero, nil) if no rows match.
//
// Uses CAST(strftime('%s',ts) AS INTEGER) to get Unix epoch
// directly, avoiding driver-specific DATETIME type conversions
// that may break parseSQLiteTime (e.g. modernc.org/sqlite
// returns time.Time which database/sql formats as RFC3339Nano
// when scanned into *string).
func (bm *BotMemory) GetLastSessionByPrefix(
	ctx context.Context, pattern string,
) (string, time.Time, error) {
	var sid string
	var epoch int64
	err := bm.db.QueryRowContext(ctx,
		`SELECT session_id,
		        CAST(strftime('%s', ts) AS INTEGER)
		 FROM messages
		 WHERE session_id LIKE ?
		 ORDER BY id DESC LIMIT 1`,
		pattern,
	).Scan(&sid, &epoch)
	if err == sql.ErrNoRows {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf(
			"pika/botmemory: get last session: %w", err,
		)
	}
	return sid, time.Unix(epoch, 0), nil
}
