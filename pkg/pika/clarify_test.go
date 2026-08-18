package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sipeed/picoclaw/pkg/bus"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func newTestClarifyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS knowledge_atoms (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			atom_id TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT 'general',
			summary TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0.8,
			created_at TEXT NOT NULL
				DEFAULT (strftime(
					'%Y-%m-%dT%H:%M:%SZ', 'now'
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
	_, ftsErr := db.Exec(
		`INSERT INTO knowledge_fts(rowid, summary)
			VALUES (?, ?)`,
		id, summary,
	)
	if ftsErr != nil {
		t.Fatalf("insert fts: %v", ftsErr)
	}
}

// newTestClarify creates a ClarifyHandler backed by MessageBus.
func newTestClarify(
	t *testing.T,
) (*ClarifyHandler, *bus.MessageBus, *sql.DB) {
	t.Helper()
	return newTestClarifyWithCfg(t, nil)
}

// newTestClarifyWithCfg allows custom config overrides.
func newTestClarifyWithCfg(
	t *testing.T,
	cfgFn func(*ClarifyConfig),
) (*ClarifyHandler, *bus.MessageBus, *sql.DB) {
	t.Helper()
	db := newTestClarifyDB(t)
	bm := &BotMemory{db: db}
	cfg := &ClarifyConfig{
		Enabled:               true,
		TimeoutMin:            1,
		MaxStreakBeforeBypass: 2,
		PrecheckTimeoutMs:     3000,
	}
	if cfgFn != nil {
		cfgFn(cfg)
	}
	mb := bus.NewMessageBus()
	ch := NewClarifyHandler(cfg, bm, mb)
	return ch, mb, db
}

// ctxWithChat creates a context with session, chat, and channel info.
func ctxWithChat(
	sessionID, chatID, channel string,
) context.Context {
	ctx := toolshared.WithToolSessionContext(
		context.Background(), "main", sessionID, nil,
	)
	ctx = toolshared.WithToolInboundContext(ctx, channel, chatID, "", "")

	return ctx
}

// sendReplyAfter publishes an inbound reply via MessageBus
// after a short delay (simulates user responding).
func sendReplyAfter(
	mb *bus.MessageBus,
	chatID, content string,
	delay time.Duration,
) {
	go func() {
		time.Sleep(delay)
		_ = mb.PublishInbound(
			context.Background(),
			bus.InboundMessage{
				ChatID:  chatID,
				Channel: "test",
				Content: content,
				Context: bus.InboundContext{
					Channel:  "test",
					ChatID:   chatID,
					SenderID: "user-test",
				},
			},
		)
	}()
}

// Test 1: Memory hit — knowledge exists, no escalation.
func TestClarify_MemoryHit(t *testing.T) {
	ch, _, db := newTestClarify(t)
	defer db.Close()

	insertKnowledge(
		t, db,
		"Деплой производится через Docker Compose",
		"devops",
	)

	ctx := ctxWithChat("sess-1", "chat-1", "test")
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
}

// Test 2: Escalate to user — knowledge empty, reply via bus.
func TestClarify_EscalateToUser(t *testing.T) {
	ch, mb, db := newTestClarify(t)
	defer db.Close()

	// Simulate user reply after 50ms
	sendReplyAfter(
		mb, "chat-2", "Да, делай",
		50*time.Millisecond,
	)

	ctx := ctxWithChat("sess-2", "chat-2", "test")
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
}

// Test 3: Timeout — no reply, waitForReply times out.
func TestClarify_Timeout(t *testing.T) {
	ch, _, db := newTestClarifyWithCfg(
		t,
		func(cfg *ClarifyConfig) {
			// 0 minutes → context.WithTimeout(ctx, 0)
			// → immediately expired → instant timeout
			cfg.TimeoutMin = 0
		},
	)
	defer db.Close()

	ctx := ctxWithChat("sess-3", "chat-3", "test")
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

// Test 4: Streak bypass — streak=2 → escalate with history.
func TestClarify_StreakBypass(t *testing.T) {
	ch, mb, db := newTestClarify(t)
	defer db.Close()

	// Manually set streak=2
	state := ch.getOrCreateState("sess-4")
	state.streak = 2
	state.lastQuestions = []string{
		"вопрос 1", "вопрос 2",
	}

	sendReplyAfter(
		mb, "chat-4", "ОК",
		50*time.Millisecond,
	)

	ctx := ctxWithChat("sess-4", "chat-4", "test")
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

	// Verify outbound message includes history.
	// PublishOutbound writes to buffered channel;
	// drain it after Execute returns.
	sent := drainOutbound(mb)
	if len(sent) != 1 {
		t.Fatalf(
			"sent %d msgs, want 1", len(sent),
		)
	}
	msg := sent[0].Content
	if !strings.Contains(msg, "вопрос 1") ||
		!strings.Contains(msg, "вопрос 2") {
		t.Error(
			"streak bypass should include history",
		)
	}
}

// drainOutbound reads all buffered outbound messages
// from MessageBus (non-blocking).
func drainOutbound(
	mb *bus.MessageBus,
) []bus.OutboundMessage {
	var msgs []bus.OutboundMessage
	for {
		select {
		case msg := <-mb.OutboundChan():
			msgs = append(msgs, msg)
		default:
			return msgs
		}
	}
}

// Test 5: Decision question → escalate immediately.
func TestClarify_DecisionQuestion(t *testing.T) {
	ch, mb, db := newTestClarify(t)
	defer db.Close()

	// Insert knowledge to prove FTS5 is skipped
	insertKnowledge(
		t, db, "делать вещи", "general",
	)

	sendReplyAfter(
		mb, "chat-5", "Подтверждаю",
		50*time.Millisecond,
	)

	ctx := ctxWithChat("sess-5", "chat-5", "test")
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
	ch, _, db := newTestClarify(t)
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
	ch, _, db := newTestClarify(t)
	defer db.Close()

	_ = ch.getOrCreateState("sess-7")
	ch.CleanupSession("sess-7")

	_, loaded := ch.sessions.Load("sess-7")
	if loaded {
		t.Error("session should be deleted")
	}
}

// Test 8: IsAwaiting.
func TestClarify_IsAwaiting(t *testing.T) {
	ch, _, db := newTestClarify(t)
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
	db := newTestClarifyDB(t)
	defer db.Close()

	bm := &BotMemory{db: db}
	cfg := &ClarifyConfig{
		Enabled:               true,
		TimeoutMin:            0, // immediate waitForReply timeout
		MaxStreakBeforeBypass: 2,
		PrecheckTimeoutMs:     1, // very short FTS5 timeout
	}
	mb := bus.NewMessageBus()
	ch := NewClarifyHandler(cfg, bm, mb)

	insertKnowledge(
		t, db,
		"важная информация о деплое",
		"devops",
	)

	// Create a context that is already canceled
	ctx, cancel := context.WithCancel(
		ctxWithChat("sess-9", "chat-9", "test"),
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
	// If we got a valid result, it should not be memory
	if cr.Source == "memory" {
		t.Error(
			"should not get memory hit on " +
				"canceled context",
		)
	}
}
