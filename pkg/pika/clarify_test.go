package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// mockSender implements ClarifySender for tests.
type mockSender struct {
	mu         sync.Mutex
	sentMsgs   []string
	reply      string
	replyErr   error
	replyDelay time.Duration
	sendErr    error
}

func (m *mockSender) SendMessage(
	chatID string, text string,
) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return "", m.sendErr
	}
	m.sentMsgs = append(m.sentMsgs, text)
	return "msg-1", nil
}

func (m *mockSender) WaitForReply(
	ctx context.Context,
	chatID string,
	timeout time.Duration,
) (string, error) {
	if m.replyDelay > 0 {
		select {
		case <-time.After(m.replyDelay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.replyErr != nil {
		return "", m.replyErr
	}
	return m.reply, nil
}

func newTestClarifyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Create minimal schema for FTS5 pre-check
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS knowledge_atoms (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			atom_id TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT 'general',
			summary TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0.8,
			created_at TEXT NOT NULL
				DEFAULT (strftime(
					'%%Y-%%m-%%dT%%H:%%M:%%SZ', 'now'
				)),
			source_message_id INTEGER
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS
			knowledge_fts USING fts5(
				summary,
				content=knowledge_atoms,
				content_rowid=id
			)`,
	}
	for _, s := range stmts {
		if _, execErr := db.Exec(s); execErr != nil {
			t.Fatalf("exec schema: %v", execErr)
		}
	}
	return db
}

func insertKnowledge(
	t *testing.T, db *sql.DB,
	summary, category string,
) {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO knowledge_atoms
			(atom_id, category, summary)
			VALUES (?, ?, ?)`,
		fmt.Sprintf("atom-%d", time.Now().UnixNano()),
		category, summary,
	)
	if err != nil {
		t.Fatalf("insert knowledge: %v", err)
	}
	id, _ := res.LastInsertId()
	// Sync FTS index
	_, ftsErr := db.Exec(
		`INSERT INTO knowledge_fts(rowid, summary)
			VALUES (?, ?)`,
		id, summary,
	)
	if ftsErr != nil {
		t.Fatalf("insert fts: %v", ftsErr)
	}
}

func newTestClarify(
	t *testing.T,
	sender *mockSender,
) (*ClarifyHandler, *sql.DB) {
	t.Helper()
	db := newTestClarifyDB(t)
	bm := &BotMemory{db: db}
	cfg := &ClarifyConfig{
		Enabled:              true,
		TimeoutMin:           1,
		MaxStreakBeforeBypass: 2,
		PrecheckTimeoutMs:    3000,
	}
	ch := NewClarifyHandler(cfg, bm, sender, "chat-1")
	return ch, db
}

func ctxWithSession(
	sessionID string,
) context.Context {
	return context.WithValue(
		context.Background(),
		SessionIDKey{},
		sessionID,
	)
}

// Test 1: Memory hit — knowledge exists.
func TestClarify_MemoryHit(t *testing.T) {
	sender := &mockSender{}
	ch, db := newTestClarify(t, sender)
	defer db.Close()

	insertKnowledge(
		t, db,
		"Деплой производится через Docker Compose",
		"devops",
	)

	ctx := ctxWithSession("sess-1")
	result := ch.Execute(ctx, map[string]any{
		"question": "деплой",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	var cr ClarifyResult
	if err := json.Unmarshal(
		[]byte(result.ForLLM), &cr,
	); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cr.Source != "memory" {
		t.Errorf(
			"source = %q, want memory", cr.Source,
		)
	}
	if len(sender.sentMsgs) > 0 {
		t.Error("should not escalate on memory hit")
	}
}

// Test 2: Escalate to user — knowledge empty.
func TestClarify_EscalateToUser(t *testing.T) {
	sender := &mockSender{reply: "Да, делай"}
	ch, db := newTestClarify(t, sender)
	defer db.Close()

	ctx := ctxWithSession("sess-2")
	result := ch.Execute(ctx, map[string]any{
		"question": "какой формат файла?",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	var cr ClarifyResult
	if err := json.Unmarshal(
		[]byte(result.ForLLM), &cr,
	); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cr.Source != "manager" {
		t.Errorf(
			"source = %q, want manager", cr.Source,
		)
	}
	if cr.Answer != "Да, делай" {
		t.Errorf(
			"answer = %q, want 'Да, делай'",
			cr.Answer,
		)
	}
	if len(sender.sentMsgs) != 1 {
		t.Errorf(
			"sent %d msgs, want 1",
			len(sender.sentMsgs),
		)
	}
}

// Test 3: Timeout — WaitForReply times out.
func TestClarify_Timeout(t *testing.T) {
	sender := &mockSender{
		replyErr: fmt.Errorf("timeout"),
	}
	ch, db := newTestClarify(t, sender)
	defer db.Close()

	ctx := ctxWithSession("sess-3")
	result := ch.Execute(ctx, map[string]any{
		"question": "нужна ли миграция?",
	})

	var cr ClarifyResult
	if err := json.Unmarshal(
		[]byte(result.ForLLM), &cr,
	); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cr.Source != "timeout" {
		t.Errorf(
			"source = %q, want timeout", cr.Source,
		)
	}
}

// Test 4: Streak bypass — streak=2 → escalate.
func TestClarify_StreakBypass(t *testing.T) {
	sender := &mockSender{reply: "ОК"}
	ch, db := newTestClarify(t, sender)
	defer db.Close()

	// Manually set streak=2
	state := ch.getOrCreateState("sess-4")
	state.streak = 2
	state.lastQuestions = []string{
		"вопрос 1", "вопрос 2",
	}

	ctx := ctxWithSession("sess-4")
	result := ch.Execute(ctx, map[string]any{
		"question": "вопрос 3",
	})

	var cr ClarifyResult
	if err := json.Unmarshal(
		[]byte(result.ForLLM), &cr,
	); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cr.Source != "manager" {
		t.Errorf(
			"source = %q, want manager", cr.Source,
		)
	}
	// Check history was included in message
	if len(sender.sentMsgs) != 1 {
		t.Fatalf(
			"sent %d msgs, want 1",
			len(sender.sentMsgs),
		)
	}
	msg := sender.sentMsgs[0]
	if !contains(msg, "вопрос 1") ||
		!contains(msg, "вопрос 2") {
		t.Error(
			"streak bypass should include history",
		)
	}
}

// Test 5: Decision question → escalate immediately.
func TestClarify_DecisionQuestion(t *testing.T) {
	sender := &mockSender{reply: "Подтверждаю"}
	ch, db := newTestClarify(t, sender)
	defer db.Close()

	// Insert knowledge to prove FTS5 is skipped
	insertKnowledge(
		t, db, "делать вещи", "general",
	)

	ctx := ctxWithSession("sess-5")
	result := ch.Execute(ctx, map[string]any{
		"question": "делать?",
	})

	var cr ClarifyResult
	if err := json.Unmarshal(
		[]byte(result.ForLLM), &cr,
	); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cr.Source != "manager" {
		t.Errorf(
			"source = %q, want manager "+
				"(decision question)",
			cr.Source,
		)
	}
}

// Test 6: ResetStreak.
func TestClarify_ResetStreak(t *testing.T) {
	sender := &mockSender{}
	ch, db := newTestClarify(t, sender)
	defer db.Close()

	state := ch.getOrCreateState("sess-6")
	state.streak = 2
	state.lastQuestions = []string{"q1", "q2"}

	ch.ResetStreak("sess-6")

	if state.streak != 0 {
		t.Errorf(
			"streak = %d, want 0", state.streak,
		)
	}
	if state.lastQuestions != nil {
		t.Error("lastQuestions should be nil")
	}
}

// Test 7: CleanupSession.
func TestClarify_CleanupSession(t *testing.T) {
	sender := &mockSender{}
	ch, db := newTestClarify(t, sender)
	defer db.Close()

	_ = ch.getOrCreateState("sess-7")
	ch.CleanupSession("sess-7")

	// After cleanup, Load should return false
	_, loaded := ch.sessions.Load("sess-7")
	if loaded {
		t.Error("session should be deleted")
	}
}

// Test 8: IsAwaiting.
func TestClarify_IsAwaiting(t *testing.T) {
	sender := &mockSender{
		replyDelay: 200 * time.Millisecond,
		reply:      "ответ",
	}
	ch, db := newTestClarify(t, sender)
	defer db.Close()

	if ch.IsAwaiting("sess-8") {
		t.Error("should not be awaiting initially")
	}

	state := ch.getOrCreateState("sess-8")
	state.awaiting = true

	if !ch.IsAwaiting("sess-8") {
		t.Error("should be awaiting")
	}

	state.awaiting = false
	if ch.IsAwaiting("sess-8") {
		t.Error(
			"should not be awaiting after reset",
		)
	}
}

// Test 9: FTS5 precheck timeout → escalation.
func TestClarify_PrecheckTimeout(t *testing.T) {
	sender := &mockSender{reply: "ответ"}
	db := newTestClarifyDB(t)
	defer db.Close()

	bm := &BotMemory{db: db}
	cfg := &ClarifyConfig{
		Enabled:              true,
		TimeoutMin:           1,
		MaxStreakBeforeBypass: 2,
		// Extremely short timeout to force FTS miss
		PrecheckTimeoutMs: 1,
	}
	ch := NewClarifyHandler(cfg, bm, sender, "chat-9")

	// Insert data so FTS would normally find it
	insertKnowledge(
		t, db,
		"важная информация о деплое",
		"devops",
	)

	// Create a context that is already canceled
	ctx, cancel := context.WithCancel(
		ctxWithSession("sess-9"),
	)
	cancel() // cancel immediately

	result := ch.Execute(ctx, map[string]any{
		"question": "деплой",
	})

	var cr ClarifyResult
	if err := json.Unmarshal(
		[]byte(result.ForLLM), &cr,
	); err != nil {
		// On canceled context, escalation will
		// also fail — check error result
		if !result.IsError {
			t.Fatalf(
				"unmarshal: %v, raw: %s",
				err, result.ForLLM,
			)
		}
		// Error is expected when context is canceled
		return
	}
	// If we got a valid result, it should be manager
	// or timeout (not memory)
	if cr.Source == "memory" {
		t.Error(
			"should not get memory hit on " +
				"canceled context",
		)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		containsHelper(s, substr)
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
