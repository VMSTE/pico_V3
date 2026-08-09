package pika

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *BotMemory {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Migrate(dbPath)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("NewBotMemory: %v", err)
	}
	t.Cleanup(func() { bm.Close() })
	return bm
}

func TestSaveAndGetMessages(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	id, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "hello", Tokens: 5,
	})
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
	msgs, err := bm.GetMessages(ctx, "s1")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Errorf("content = %q, want hello", msgs[0].Content)
	}
	if msgs[0].Role != "user" {
		t.Errorf("role = %q, want user", msgs[0].Role)
	}
}

func TestSumTokensAndCount(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "a", Tokens: 10,
	})
	bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "assistant",
		Content: "b", Tokens: 20,
	})
	sum, err := bm.SumTokensBySession(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if sum != 30 {
		t.Errorf("sum = %d, want 30", sum)
	}
	c, err := bm.CountMessagesBySession(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if c != 2 {
		t.Errorf("count = %d, want 2", c)
	}
}

func TestGetMaxPikaSessionID(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	m, _ := bm.GetMaxPikaSessionID(ctx, "s1")
	if m != "" {
		t.Errorf("empty max = %s, want empty string", m)
	}
	bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "3", Role: "user",
		Content: "c", Tokens: 1,
	})
	m, _ = bm.GetMaxPikaSessionID(ctx, "s1")
	if m != "3" {
		t.Errorf("max = %s, want 3", m)
	}
}

func TestGetOldestPikaSessionIDs(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "a", Tokens: 10,
	})
	bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "2", Role: "user",
		Content: "b", Tokens: 20,
	})
	bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "3", Role: "user",
		Content: "c", Tokens: 30,
	})
	ids, err := bm.GetOldestPikaSessionIDs(ctx, "s1", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "1" {
		t.Errorf("oldest = %v, want [1]", ids)
	}
	ids2, _ := bm.GetOldestPikaSessionIDs(ctx, "s1", 35)
	if len(ids2) != 2 {
		t.Errorf("oldest = %v, want [1,2]", ids2)
	}
}

func TestSaveAndGetEvents(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	id, err := bm.SaveEvent(ctx, EventRow{
		Type: "tool_call", Summary: "called api",
		Outcome: "success", ChatID: "s1", PikaSessionID: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
	evts, err := bm.GetEventsByTurns(ctx, "s1", []string{"1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != "tool_call" {
		t.Errorf("type = %q", evts[0].Type)
	}
}

func TestUpsertRegistry(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	created, err := bm.UpsertRegistry(ctx, RegistryRow{
		Kind: "runbook", Key: "web_search", Summary: "search the web",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("expected created=true")
	}
	created2, err := bm.UpsertRegistry(ctx, RegistryRow{
		Kind: "runbook", Key: "web_search", Summary: "updated",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Error("expected created=false on update")
	}
	r, err := bm.GetRegistry(ctx, "runbook", "web_search")
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil")
	}
	if r.Summary != "updated" {
		t.Errorf("summary = %q, want updated", r.Summary)
	}
}

func TestSearchRegistry(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.UpsertRegistry(ctx, RegistryRow{
		Kind: "runbook", Key: "web_search", Summary: "ws",
	})
	bm.UpsertRegistry(ctx, RegistryRow{
		Kind: "runbook", Key: "web_browse", Summary: "wb",
	})
	bm.UpsertRegistry(ctx, RegistryRow{
		Kind: "script", Key: "gpt4", Summary: "g4",
	})
	res, err := bm.SearchRegistry(ctx, "runbook", "web_%")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Errorf("expected 2, got %d", len(res))
	}
}

// PIKA-V3: Bug 1 fix — Migrate returns (*sql.DB, error), not just error
func TestInsertSpanAndRecover(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Migrate(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Insert a stale span BEFORE NewBotMemory
	_, err = db.Exec(
		`INSERT INTO trace_spans
		(span_id,trace_id,component,operation,started_at,status)
		VALUES('stale-1','t1','comp','op','2025-01-01 00:00:00','ok')`)
	if err != nil {
		t.Fatal(err)
	}
	bm, err := NewBotMemory(db)
	if err != nil {
		t.Fatal(err)
	}
	defer bm.Close()
	// Verify stale span was recovered
	var status, errType, errMsg string
	err = db.QueryRow(
		`SELECT status, error_type, error_message
		FROM trace_spans WHERE span_id='stale-1'`).Scan(
		&status, &errType, &errMsg)
	if err != nil {
		t.Fatal(err)
	}
	if status != "error" {
		t.Errorf("status = %q, want error", status)
	}
	if errType != "crash_recovery" {
		t.Errorf(
			"error_type = %q, want crash_recovery", errType)
	}
}

func TestInsertAndCompleteSpan(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	importTime := parseSQLiteTime("2025-06-01 12:00:00")
	err := bm.InsertSpan(ctx, TraceSpanRow{
		SpanID: "sp1", TraceID: "t1", Component: "llm",
		Operation: "generate", StartedAt: importTime,
		Status: "ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = bm.CompleteSpan(ctx, "sp1", "ok", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var status string
	bm.db.QueryRow(
		`SELECT status FROM trace_spans WHERE span_id='sp1'`,
	).Scan(&status)
	if status != "ok" {
		t.Errorf("status = %q, want ok", status)
	}
}

func TestArchiveAndDeleteTurns(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "hello", Tokens: 5,
	})
	bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "assistant",
		Content: "hi", Tokens: 3,
	})
	bm.SaveEvent(ctx, EventRow{
		Type: "msg", Summary: "test",
		ChatID: "s1", PikaSessionID: "1",
	})
	err := bm.ArchiveAndDeleteTurns(ctx, "s1", []string{"1"})
	if err != nil {
		t.Fatalf("ArchiveAndDeleteTurns: %v", err)
	}
	// Hot data should be gone
	msgs, _ := bm.GetMessages(ctx, "s1")
	if len(msgs) != 0 {
		t.Errorf("hot messages = %d, want 0", len(msgs))
	}
	// Archive should have data
	content, meta, err := bm.ReadArchivedMessage(ctx, 1)
	if err != nil {
		t.Fatalf("ReadArchivedMessage: %v", err)
	}
	if content != "hello" {
		t.Errorf(
			"archived content = %q, want hello", content)
	}
	_ = meta
}

func TestArchiveTransactionRollback(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "keep me", Tokens: 5,
	})
	bm.SaveEvent(ctx, EventRow{
		Type: "msg", Summary: "evt",
		ChatID: "s1", PikaSessionID: "1",
	})
	// Pre-insert conflicting row in events_archive
	evts, _ := bm.GetEventsByTurns(ctx, "s1", []string{"1"})
	if len(evts) == 0 {
		t.Fatal("no events")
	}
	bm.db.ExecContext(ctx,
		`INSERT INTO events_archive
		(id,chat_id,pika_session_id,ts,type,outcome,
		summary,tags,blob)
		VALUES(?,?,?,datetime('now'),
		'x','','',NULL,NULL)`,
		evts[0].ID, "s1", "1")
	// Archive should fail due to PK conflict
	err := bm.ArchiveAndDeleteTurns(
		ctx, "s1", []string{"1"})
	if err == nil {
		t.Fatal("expected error from PK conflict")
	}
	// Hot data should still be intact (TX rolled back)
	msgs, _ := bm.GetMessages(ctx, "s1")
	if len(msgs) != 1 {
		t.Errorf(
			"hot messages = %d, want 1 (rollback failed)",
			len(msgs))
	}
	if msgs[0].Content != "keep me" {
		t.Errorf("content = %q", msgs[0].Content)
	}
}

// PIKA-V3: Tests updated to match DDL-correct signatures
func TestPromptVersionsAndSnapshots(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	promptID, err := bm.UpsertPromptVersion(
		ctx, "CORE", 1,
		"abc123", "You are a helpful assistant.", "initial")
	if err != nil {
		t.Fatal(err)
	}
	if promptID != "CORE/v1" {
		t.Errorf(
			"promptID = %q, want CORE/v1", promptID)
	}
	// Idempotent
	_, err = bm.UpsertPromptVersion(
		ctx, "CORE", 1,
		"abc123", "You are a helpful assistant.", "initial")
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]int{
		"core": 10, "context": 20, "brief": 5,
		"trail": 3, "plan": 2,
	}
	err = bm.InsertPromptSnapshot(ctx,
		"snap-1", "trace-1", "s1", "1",
		promptID, "", "", tokens,
		"fullhash", "preview text", 42)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAtomUsage(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	// Need atoms for FK constraint
	bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "P-1", ChatID: "s1", PikaSessionID: "1",
		Category: "pattern", Summary: "test",
		Confidence: 0.8, Polarity: "positive",
	})
	bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "P-2", ChatID: "s1", PikaSessionID: "1",
		Category: "pattern", Summary: "test2",
		Confidence: 0.7, Polarity: "neutral",
	})
	pos := 0
	tok := 100
	err := bm.InsertAtomUsage(ctx,
		"P-1", "trace-1", "1", "BRIEF",
		&pos, &tok, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = bm.InsertAtomUsage(ctx,
		"P-2", "trace-1", "1", "CONTEXT",
		nil, nil, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var count int
	bm.db.QueryRow(
		`SELECT COUNT(*) FROM atom_usage`).Scan(&count)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestGetMaxAtomN(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	n, err := bm.GetMaxAtomN(ctx, "pattern")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("empty max = %d, want 0", n)
	}
	bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "P-1", ChatID: "s1", PikaSessionID: "1",
		Category: "pattern", Summary: "test",
		Confidence: 0.8, Polarity: "positive",
	})
	bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "P-5", ChatID: "s1", PikaSessionID: "1",
		Category: "pattern", Summary: "test2",
		Confidence: 0.9, Polarity: "positive",
	})
	n, err = bm.GetMaxAtomN(ctx, "pattern")
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("max = %d, want 5", n)
	}
	_, err = bm.GetMaxAtomN(ctx, "unknown_cat")
	if err == nil {
		t.Error("expected error for unknown category")
	}
}

func TestUpdateAtomConfidence(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "D-1", ChatID: "s1", PikaSessionID: "1",
		Category: "decision", Summary: "use redis",
		Confidence: 0.7, Polarity: "positive",
	})
	hist := json.RawMessage(
		`{"turn":2,"delta":0.1,"reason":"confirmed"}`)
	err := bm.UpdateAtomConfidence(ctx, "D-1", 0.8, hist)
	if err != nil {
		t.Fatal(err)
	}
	var conf float64
	var history string
	bm.db.QueryRow(
		`SELECT confidence, history
		FROM knowledge_atoms
		WHERE atom_id='D-1'`).Scan(&conf, &history)
	if conf != 0.8 {
		t.Errorf("confidence = %f, want 0.8", conf)
	}
	if history == "" || history == "[]" {
		t.Error("history should contain entry")
	}
}

// PIKA-V3: ТЗ-v2-1a-fix7 — FTS search test
func TestInsertAndQueryKnowledgeFTS(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	err := bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID:        "P-1",
		ChatID:        "s1",
		PikaSessionID: "1",
		Category:      "pattern",
		Summary:       "docker OOM restart observed",
		Confidence:    0.8,
		Polarity:      "negative",
	})
	if err != nil {
		t.Fatal(err)
	}
	// FTS should find by keyword
	results, err := bm.QueryKnowledgeFTS(
		ctx, "OOM", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf(
			"expected 1 result, got %d", len(results))
	}
	if results[0].AtomID != "P-1" {
		t.Errorf(
			"atom_id = %q, want P-1",
			results[0].AtomID)
	}
	if results[0].Summary !=
		"docker OOM restart observed" {
		t.Errorf(
			"summary = %q, want "+
				"'docker OOM restart observed'",
			results[0].Summary,
		)
	}
}

// PIKA-V3: ТЗ-v2-1a-fix7 — RequestLog insert test
func TestInsertRequestLog(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	mi := 0
	cp := 1
	id, err := bm.InsertRequestLog(ctx, RequestLogRow{
		ChatID:             "s1",
		MsgIndex:           &mi,
		Direction:          "chat",
		Component:          "main",
		Model:              "step-3.5-flash",
		PromptTokens:       100,
		CompletionTokens:   50,
		CachedTokens:       10,
		ReasoningTokens:    20,
		ToolCallsRequested: 1,
		ToolCallsSuccess:   1,
		CostUSD:            0.002,
		ResponseMs:         350,
		TaskTag:            "general",
		ChainID:            "chain-1",
		ChainPosition:      &cp,
		PlanDetected:       1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Error("expected non-zero id")
	}
	// Verify row exists
	var model string
	var cost float64
	err = bm.db.QueryRow(
		`SELECT model, cost_usd
		FROM request_log WHERE id=?`,
		id,
	).Scan(&model, &cost)
	if err != nil {
		t.Fatal(err)
	}
	if model != "step-3.5-flash" {
		t.Errorf("model = %q", model)
	}
	if cost != 0.002 {
		t.Errorf("cost = %f, want 0.002", cost)
	}
}

func TestQueryCorrelatedTools(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()

	// Insert a knowledge atom
	err := bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID:        "CT-1",
		ChatID:        "s1",
		PikaSessionID: "1",
		Category:      "pattern",
		Summary:       "deploy pipeline needs docker compose",
		Confidence:    0.9,
		Polarity:      "positive",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Insert atom_usage records linking atom to tools
	for i := 0; i < 3; i++ {
		err = bm.InsertAtomUsage(ctx, "CT-1", "tr-1", "sess-1",
			"BRIEF", nil, nil, "exec", "", "")
		if err != nil {
			t.Fatal(err)
		}
	}
	err = bm.InsertAtomUsage(ctx, "CT-1", "tr-2", "sess-1",
		"BRIEF", nil, nil, "compose", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Query correlated tools for "deploy"
	results, err := bm.QueryCorrelatedTools(ctx, "deploy", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 correlated tool, got 0")
	}
	// exec should be first (3 hits vs 1)
	if results[0].ToolName != "exec" {
		t.Errorf("top tool = %q, want exec", results[0].ToolName)
	}
	if results[0].Count != 3 {
		t.Errorf("top count = %d, want 3", results[0].Count)
	}
	if len(results) >= 2 && results[1].ToolName != "compose" {
		t.Errorf("second tool = %q, want compose", results[1].ToolName)
	}
}

// D-AUDIT-63: регрессия — прод-писатели используют только разрешённые статусы.
func TestSpanStatusRegression(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	if err := bm.InsertSpan(ctx, TraceSpanRow{
		SpanID: "sp-err", TraceID: "t1", Component: "reflexor",
		Operation: "run", StartedAt: parseSQLiteTime("2025-06-01 12:00:00"),
		Status: "ok",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := bm.CompleteSpan(ctx, "sp-err", "error", nil, "timeout", "boom"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	var status, errType string
	var duration int64
	if err := bm.db.QueryRow(
		`SELECT status, COALESCE(error_type,''), COALESCE(duration_ms,-1)
		FROM trace_spans WHERE span_id='sp-err'`,
	).Scan(&status, &errType, &duration); err != nil {
		t.Fatal(err)
	}
	if status != "error" {
		t.Errorf("status = %q, want error", status)
	}
	if errType != "timeout" {
		t.Errorf("error_type = %q, want timeout", errType)
	}
	if duration < 0 {
		t.Error("duration_ms should be computed after completion")
	}
}

// D-AUDIT-61: конвейер дописывает invoked_tool_after/result.
func TestMarkAtomUsageToolAfter(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "P-1", ChatID: "s1", PikaSessionID: "1",
		Category: "pattern", Summary: "test",
		Confidence: 0.8, Polarity: "positive",
	})
	// Атом попал в бриф хода 1 (как пишет Архивариус — tool-поля пустые)
	if err := bm.InsertAtomUsage(ctx,
		"P-1", "trace-1", "1", "BRIEF", nil, nil, "", "", ""); err != nil {
		t.Fatal(err)
	}
	// Конвейер: после брифа вызвали exec успешно
	if err := bm.MarkAtomUsageToolAfter(ctx, "1", "exec", "success"); err != nil {
		t.Fatal(err)
	}
	var tool, result string
	if err := bm.db.QueryRow(
		`SELECT invoked_tool_after, invoked_tool_result
		FROM atom_usage WHERE atom_id='P-1'`,
	).Scan(&tool, &result); err != nil {
		t.Fatal(err)
	}
	if tool != "exec" || result != "success" {
		t.Errorf("got %q/%q, want exec/success", tool, result)
	}
	// Первая запись побеждает: второй инструмент не перезаписывает
	if err := bm.MarkAtomUsageToolAfter(ctx, "1", "compose", "failure"); err != nil {
		t.Fatal(err)
	}
	if err := bm.db.QueryRow(
		`SELECT invoked_tool_after FROM atom_usage WHERE atom_id='P-1'`,
	).Scan(&tool); err != nil {
		t.Fatal(err)
	}
	if tool != "exec" {
		t.Errorf("first-write-wins violated: got %q", tool)
	}
	// Другой ход не затрагивается
	bm.InsertAtom(ctx, KnowledgeAtomRow{
		AtomID: "P-2", ChatID: "s1", PikaSessionID: "2",
		Category: "pattern", Summary: "test2",
		Confidence: 0.7, Polarity: "neutral",
	})
	if err := bm.InsertAtomUsage(ctx,
		"P-2", "trace-2", "2", "BRIEF", nil, nil, "", "", ""); err != nil {
		t.Fatal(err)
	}
	var cnt int
	bm.db.QueryRow(
		`SELECT COUNT(*) FROM atom_usage
		WHERE pika_session_id='2' AND invoked_tool_after IS NULL`,
	).Scan(&cnt)
	if cnt != 1 {
		t.Errorf("turn 2 row should stay unmarked, got %d", cnt)
	}
}

// D-AUDIT-67: сигнал фидбека пишется в metadata последнего user-сообщения.
func TestMarkFeedbackSignal(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user", Content: "привет",
	}); err != nil {
		t.Fatal(err)
	}
	if err := bm.MarkFeedbackSignal(ctx, "s1", "wrong"); err != nil {
		t.Fatal(err)
	}
	var meta string
	if err := bm.db.QueryRow(
		`SELECT COALESCE(metadata,'') FROM messages WHERE chat_id='s1'`,
	).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meta, `"feedback_signal":"wrong"`) {
		t.Errorf("metadata = %q, want feedback_signal=wrong", meta)
	}
	// Другая сессия не затронута
	if _, err := bm.SaveMessage(ctx, MessageRow{
		ChatID: "s2", PikaSessionID: "1", Role: "user", Content: "ок",
	}); err != nil {
		t.Fatal(err)
	}
	if err := bm.MarkFeedbackSignal(ctx, "s2", "rephrase"); err != nil {
		t.Fatal(err)
	}
	var meta1 string
	bm.db.QueryRow(
		`SELECT COALESCE(metadata,'') FROM messages WHERE chat_id='s1'`,
	).Scan(&meta1)
	if !strings.Contains(meta1, `"feedback_signal":"wrong"`) {
		t.Error("s1 metadata changed by s2 mark")
	}
}

// D-AUDIT-67: fail-события выбираются за окно времени.
func TestGetRecentFailEvents(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	if _, err := bm.SaveEvent(ctx, EventRow{
		ChatID: "s1", PikaSessionID: "1", Type: "tool_exec",
		Summary: "exec failed", Outcome: "fail",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bm.SaveEvent(ctx, EventRow{
		ChatID: "s1", PikaSessionID: "1", Type: "tool_exec",
		Summary: "exec ok", Outcome: "success",
	}); err != nil {
		t.Fatal(err)
	}
	events, err := bm.GetRecentFailEvents(ctx, "s1", 24, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Summary != "exec failed" {
		t.Errorf("got %+v, want 1 fail event", events)
	}
	// Все сессии
	all, err := bm.GetRecentFailEvents(ctx, "", 24, 10)
	if err != nil || len(all) != 1 {
		t.Errorf("all-sessions: got %d, want 1", len(all))
	}
}
