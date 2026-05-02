package pika

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) (*BotMemory) {
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
	id, err := bm.SaveMessage(ctx, MessageRow{SessionID: "s1", TurnID: 1, Role: "user", Content: "hello", Tokens: 5})
	if err != nil { t.Fatalf("SaveMessage: %v", err) }
	if id == 0 { t.Fatal("expected non-zero id") }
	msgs, err := bm.GetMessages(ctx, "s1")
	if err != nil { t.Fatalf("GetMessages: %v", err) }
	if len(msgs) != 1 { t.Fatalf("expected 1 message, got %d", len(msgs)) }
	if msgs[0].Content != "hello" { t.Errorf("content = %q, want hello", msgs[0].Content) }
	if msgs[0].Role != "user" { t.Errorf("role = %q, want user", msgs[0].Role) }
}

func TestSumTokensAndCount(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.SaveMessage(ctx, MessageRow{SessionID: "s1", TurnID: 1, Role: "user", Content: "a", Tokens: 10})
	bm.SaveMessage(ctx, MessageRow{SessionID: "s1", TurnID: 1, Role: "assistant", Content: "b", Tokens: 20})
	sum, err := bm.SumTokensBySession(ctx, "s1")
	if err != nil { t.Fatal(err) }
	if sum != 30 { t.Errorf("sum = %d, want 30", sum) }
	c, err := bm.CountMessagesBySession(ctx, "s1")
	if err != nil { t.Fatal(err) }
	if c != 2 { t.Errorf("count = %d, want 2", c) }
}

func TestGetMaxTurnID(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	m, _ := bm.GetMaxTurnID(ctx, "s1")
	if m != 0 { t.Errorf("empty max = %d, want 0", m) }
	bm.SaveMessage(ctx, MessageRow{SessionID: "s1", TurnID: 3, Role: "user", Content: "c", Tokens: 1})
	m, _ = bm.GetMaxTurnID(ctx, "s1")
	if m != 3 { t.Errorf("max = %d, want 3", m) }
}

func TestGetOldestTurnIDs(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.SaveMessage(ctx, MessageRow{SessionID: "s1", TurnID: 1, Role: "user", Content: "a", Tokens: 10})
	bm.SaveMessage(ctx, MessageRow{SessionID: "s1", TurnID: 2, Role: "user", Content: "b", Tokens: 20})
	bm.SaveMessage(ctx, MessageRow{SessionID: "s1", TurnID: 3, Role: "user", Content: "c", Tokens: 30})
	ids, err := bm.GetOldestTurnIDs(ctx, "s1", 25)
	if err != nil { t.Fatal(err) }
	if len(ids) != 1 || ids[0] != 1 { t.Errorf("oldest = %v, want [1]", ids) }
	ids2, _ := bm.GetOldestTurnIDs(ctx, "s1", 35)
	if len(ids2) != 2 { t.Errorf("oldest = %v, want [1,2]", ids2) }
}

func TestSaveAndGetEvents(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	id, err := bm.SaveEvent(ctx, EventRow{Type: "tool_call", Summary: "called api", Outcome: "success", SessionID: "s1", TurnID: 1})
	if err != nil { t.Fatal(err) }
	if id == 0 { t.Fatal("expected non-zero id") }
	evts, err := bm.GetEventsByTurns(ctx, "s1", []int{1})
	if err != nil { t.Fatal(err) }
	if len(evts) != 1 { t.Fatalf("expected 1 event, got %d", len(evts)) }
	if evts[0].Type != "tool_call" { t.Errorf("type = %q", evts[0].Type) }
}

func TestUpsertRegistry(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	created, err := bm.UpsertRegistry(ctx, RegistryRow{Kind: "tool", Key: "web_search", Summary: "search the web"})
	if err != nil { t.Fatal(err) }
	if !created { t.Error("expected created=true") }
	created2, err := bm.UpsertRegistry(ctx, RegistryRow{Kind: "tool", Key: "web_search", Summary: "updated"})
	if err != nil { t.Fatal(err) }
	if created2 { t.Error("expected created=false on update") }
	r, err := bm.GetRegistry(ctx, "tool", "web_search")
	if err != nil { t.Fatal(err) }
	if r == nil { t.Fatal("expected non-nil") }
	if r.Summary != "updated" { t.Errorf("summary = %q, want updated", r.Summary) }
}

func TestSearchRegistry(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.UpsertRegistry(ctx, RegistryRow{Kind: "tool", Key: "web_search", Summary: "ws"})
	bm.UpsertRegistry(ctx, RegistryRow{Kind: "tool", Key: "web_browse", Summary: "wb"})
	bm.UpsertRegistry(ctx, RegistryRow{Kind: "model", Key: "gpt4", Summary: "g4"})
	res, err := bm.SearchRegistry(ctx, "tool", "web_%")
	if err != nil { t.Fatal(err) }
	if len(res) != 2 { t.Errorf("expected 2, got %d", len(res)) }
}

// PIKA-V3: Bug 1 fix — Migrate returns (*sql.DB, error), not just error
func TestInsertSpanAndRecover(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Migrate(dbPath)
	if err != nil { t.Fatal(err) }
	defer db.Close()
	// Insert a stale span BEFORE NewBotMemory
	_, err = db.Exec(`INSERT INTO trace_spans (span_id,trace_id,component,operation,started_at,status) VALUES('stale-1','t1','comp','op','2025-01-01 00:00:00','ok')`)
	if err != nil { t.Fatal(err) }
	bm, err := NewBotMemory(db)
	if err != nil { t.Fatal(err) }
	defer bm.Close()
	// Verify stale span was recovered
	var status, errType, errMsg string
	err = db.QueryRow(`SELECT status, error_type, error_message FROM trace_spans WHERE span_id='stale-1'`).Scan(&status, &errType, &errMsg)
	if err != nil { t.Fatal(err) }
	if status != "error" { t.Errorf("status = %q, want error", status) }
	if errType != "crash_recovery" { t.Errorf("error_type = %q, want crash_recovery", errType) }
}

func TestInsertAndCompleteSpan(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	import_time := parseSQLiteTime("2025-06-01 12:00:00")
	err := bm.InsertSpan(ctx, TraceSpanRow{SpanID: "sp1", TraceID: "t1", Component: "llm", Operation: "generate", StartedAt: import_time, Status: "ok"})
	if err != nil { t.Fatal(err) }
	err = bm.CompleteSpan(ctx, "sp1", "ok", nil, "", "")
	if err != nil { t.Fatal(err) }
	var status string
	bm.db.QueryRow(`SELECT status FROM trace_spans WHERE span_id='sp1'`).Scan(&status)
	if status != "ok" { t.Errorf("status = %q, want ok", status) }
}

func TestArchiveAndDeleteTurns(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.SaveMessage(ctx, MessageRow{SessionID: "s1", TurnID: 1, Role: "user", Content: "hello", Tokens: 5})
	bm.SaveMessage(ctx, MessageRow{SessionID: "s1", TurnID: 1, Role: "assistant", Content: "hi", Tokens: 3})
	bm.SaveEvent(ctx, EventRow{Type: "msg", Summary: "test", SessionID: "s1", TurnID: 1})
	err := bm.ArchiveAndDeleteTurns(ctx, "s1", []int{1})
	if err != nil { t.Fatalf("ArchiveAndDeleteTurns: %v", err) }
	// Hot data should be gone
	msgs, _ := bm.GetMessages(ctx, "s1")
	if len(msgs) != 0 { t.Errorf("hot messages = %d, want 0", len(msgs)) }
	// Archive should have data
	content, meta, err := bm.ReadArchivedMessage(ctx, 1)
	if err != nil { t.Fatalf("ReadArchivedMessage: %v", err) }
	if content != "hello" { t.Errorf("archived content = %q, want hello", content) }
	_ = meta
}

func TestArchiveTransactionRollback(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.SaveMessage(ctx, MessageRow{SessionID: "s1", TurnID: 1, Role: "user", Content: "keep me", Tokens: 5})
	bm.SaveEvent(ctx, EventRow{Type: "msg", Summary: "evt", SessionID: "s1", TurnID: 1})
	// Pre-insert conflicting row in events_archive to force PK violation
	evts, _ := bm.GetEventsByTurns(ctx, "s1", []int{1})
	if len(evts) == 0 { t.Fatal("no events") }
	bm.db.ExecContext(ctx, `INSERT INTO events_archive (id,session_id,turn_id,ts,type,outcome,summary,tags,blob) VALUES(?,?,?,datetime('now'),'x','','',NULL,NULL)`,
		evts[0].ID, "s1", 1)
	// Archive should fail due to PK conflict
	err := bm.ArchiveAndDeleteTurns(ctx, "s1", []int{1})
	if err == nil { t.Fatal("expected error from PK conflict") }
	// Hot data should still be intact (TX rolled back)
	msgs, _ := bm.GetMessages(ctx, "s1")
	if len(msgs) != 1 { t.Errorf("hot messages = %d, want 1 (rollback failed)", len(msgs)) }
	if msgs[0].Content != "keep me" { t.Errorf("content = %q", msgs[0].Content) }
}

// PIKA-V3: Tests updated to match DDL-correct signatures (bugs 2-4)
func TestPromptVersionsAndSnapshots(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	promptID, err := bm.UpsertPromptVersion(ctx, "CORE", 1, "abc123", "You are a helpful assistant.", "initial")
	if err != nil { t.Fatal(err) }
	if promptID != "CORE/v1" { t.Errorf("promptID = %q, want CORE/v1", promptID) }
	// Idempotent
	_, err = bm.UpsertPromptVersion(ctx, "CORE", 1, "abc123", "You are a helpful assistant.", "initial")
	if err != nil { t.Fatal(err) }
	tokens := map[string]int{"core": 10, "context": 20, "brief": 5, "trail": 3, "plan": 2}
	err = bm.InsertPromptSnapshot(ctx, "snap-1", "trace-1", "s1", 1, promptID, "", "", tokens, "fullhash", "preview text", 42)
	if err != nil { t.Fatal(err) }
}

func TestAtomUsage(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	// Need atoms in knowledge_atoms for FK constraint
	bm.InsertAtom(ctx, KnowledgeAtomRow{AtomID: "P-1", SessionID: "s1", TurnID: 1, Category: "pattern", Summary: "test", Confidence: 0.8, Polarity: "positive"})
	bm.InsertAtom(ctx, KnowledgeAtomRow{AtomID: "P-2", SessionID: "s1", TurnID: 1, Category: "pattern", Summary: "test2", Confidence: 0.7, Polarity: "neutral"})
	pos := 0
	tok := 100
	err := bm.InsertAtomUsage(ctx, "P-1", "trace-1", 1, "BRIEF", &pos, &tok, "", "", "")
	if err != nil { t.Fatal(err) }
	err = bm.InsertAtomUsage(ctx, "P-2", "trace-1", 1, "CONTEXT", nil, nil, "", "", "")
	if err != nil { t.Fatal(err) }
	var count int
	bm.db.QueryRow(`SELECT COUNT(*) FROM atom_usage`).Scan(&count)
	if count != 2 { t.Errorf("count = %d, want 2", count) }
}

func TestGetMaxAtomN(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	n, err := bm.GetMaxAtomN(ctx, "pattern")
	if err != nil { t.Fatal(err) }
	if n != 0 { t.Errorf("empty max = %d, want 0", n) }
	bm.InsertAtom(ctx, KnowledgeAtomRow{AtomID: "P-1", SessionID: "s1", TurnID: 1, Category: "pattern", Summary: "test", Confidence: 0.8, Polarity: "positive"})
	bm.InsertAtom(ctx, KnowledgeAtomRow{AtomID: "P-5", SessionID: "s1", TurnID: 1, Category: "pattern", Summary: "test2", Confidence: 0.9, Polarity: "positive"})
	n, err = bm.GetMaxAtomN(ctx, "pattern")
	if err != nil { t.Fatal(err) }
	if n != 5 { t.Errorf("max = %d, want 5", n) }
	_, err = bm.GetMaxAtomN(ctx, "unknown_cat")
	if err == nil { t.Error("expected error for unknown category") }
}

func TestUpdateAtomConfidence(t *testing.T) {
	bm := setupTestDB(t)
	ctx := context.Background()
	bm.InsertAtom(ctx, KnowledgeAtomRow{AtomID: "D-1", SessionID: "s1", TurnID: 1, Category: "decision", Summary: "use redis", Confidence: 0.7, Polarity: "positive"})
	hist := json.RawMessage(`{"turn":2,"delta":0.1,"reason":"confirmed"}`)
	err := bm.UpdateAtomConfidence(ctx, "D-1", 0.8, hist)
	if err != nil { t.Fatal(err) }
	var conf float64; var history string
	bm.db.QueryRow(`SELECT confidence, history FROM knowledge_atoms WHERE atom_id='D-1'`).Scan(&conf, &history)
	if conf != 0.8 { t.Errorf("confidence = %f, want 0.8", conf) }
	if history == "" || history == "[]" { t.Error("history should contain entry") }
}
