package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/pika"
)

// setupSessionDB creates a temp config + a migrated bot_memory.db and points
// the handler at it via PIKA_DB_PATH.
func setupSessionDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	configPath, cleanup := setupOAuthTestEnv(t)
	t.Cleanup(cleanup)
	dbPath := filepath.Join(t.TempDir(), "bot_memory.db")
	db, err := pika.Migrate(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	t.Setenv("PIKA_DB_PATH", dbPath)
	return configPath, db
}

func seedSessionMsg(t *testing.T, db *sql.DB, chatID, role, content string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO messages (chat_id,pika_session_id,role,content)
		 VALUES (?,?,?,?)`,
		chatID, "s1", role, content); err != nil {
		t.Fatalf("seed message: %v", err)
	}
}

func seedRegistry(t *testing.T, db *sql.DB, key, summary string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO registry (kind,key,summary) VALUES ('snapshot',?,?)`,
		key, summary); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
}

func newSessionMux(configPath string) *http.ServeMux {
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func listSessions(t *testing.T, mux *http.ServeMux, url string) []sessionListItem {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var items []sessionListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	return items
}

func TestHandleSessions_FromDB(t *testing.T) {
	configPath, db := setupSessionDB(t)

	seedSessionMsg(t, db, "sk_v1_aaa", "user", "привет из чата А")
	seedSessionMsg(t, db, "sk_v1_aaa", "assistant", "ответ А")
	seedSessionMsg(t, db, "sk_v1_bbb", "user", "привет из чата Б")
	seedSessionMsg(t, db, "sk_v1_hidden", "user", "скрытый чат")

	seedRegistry(t, db, "chat_scope:sk_v1_aaa", "pico:uuid-aaa")
	seedRegistry(t, db, "chat_title:sk_v1_aaa", "Мой чат")
	seedRegistry(t, db, "chat_hidden:sk_v1_hidden", "1")

	mux := newSessionMux(configPath)
	items := listSessions(t, mux, "/api/sessions")

	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 (hidden excluded)", len(items))
	}
	byID := map[string]sessionListItem{}
	for _, it := range items {
		byID[it.ID] = it
	}

	a, ok := byID["uuid-aaa"]
	if !ok {
		t.Fatalf("mapped chat missing: %+v", items)
	}
	if !a.Resumable {
		t.Error("uuid-aaa must be resumable")
	}
	if a.Title != "Мой чат" {
		t.Errorf("title = %q, want custom", a.Title)
	}
	if a.MessageCount != 2 {
		t.Errorf("message_count = %d, want 2", a.MessageCount)
	}

	b, ok := byID["sk_v1_bbb"]
	if !ok {
		t.Fatal("unmapped chat missing")
	}
	if b.Resumable {
		t.Error("sk_v1_bbb must not be resumable")
	}
	if b.Title != "привет из чата Б" {
		t.Errorf("auto title = %q", b.Title)
	}

	if _, ok := byID["sk_v1_hidden"]; ok {
		t.Error("hidden chat must not be listed")
	}
}

func TestHandleSessionDetail_FromDB(t *testing.T) {
	configPath, db := setupSessionDB(t)
	seedSessionMsg(t, db, "sk_v1_aaa", "user", "первое")
	seedSessionMsg(t, db, "sk_v1_aaa", "tool", "{}") // must be skipped
	seedSessionMsg(t, db, "sk_v1_aaa", "assistant", "второе")
	seedRegistry(t, db, "chat_scope:sk_v1_aaa", "pico:uuid-aaa")

	mux := newSessionMux(configPath)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(
		http.MethodGet, "/api/sessions/uuid-aaa", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var detail struct {
		ID       string               `json:"id"`
		Messages []sessionChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (tool skipped)", len(detail.Messages))
	}
	if detail.Messages[0].Role != "user" ||
		detail.Messages[0].Content != "первое" {
		t.Errorf("msg[0] = %+v", detail.Messages[0])
	}
	if detail.Messages[1].Role != "assistant" ||
		detail.Messages[1].Content != "второе" {
		t.Errorf("msg[1] = %+v", detail.Messages[1])
	}

	rec404 := httptest.NewRecorder()
	mux.ServeHTTP(rec404, httptest.NewRequest(
		http.MethodGet, "/api/sessions/nope", nil))
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404", rec404.Code)
	}
}

func TestHandleSessionRenameHide_FromDB(t *testing.T) {
	configPath, db := setupSessionDB(t)
	seedSessionMsg(t, db, "sk_v1_aaa", "user", "чат для правок")
	seedRegistry(t, db, "chat_scope:sk_v1_aaa", "pico:uuid-aaa")

	mux := newSessionMux(configPath)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch,
		"/api/sessions/uuid-aaa",
		strings.NewReader(`{"title":"Переименованный"}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("rename status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var title string
	if err := db.QueryRow(
		`SELECT summary FROM registry
		 WHERE kind='snapshot' AND key='chat_title:sk_v1_aaa'`).
		Scan(&title); err != nil || title != "Переименованный" {
		t.Fatalf("title row = %q, err=%v", title, err)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(
		http.MethodDelete, "/api/sessions/uuid-aaa", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("hide status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if items := listSessions(t, mux, "/api/sessions"); len(items) != 0 {
		t.Fatalf("after hide len = %d, want 0", len(items))
	}

	// Founder's rule: hiding never touches messages.
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE chat_id='sk_v1_aaa'`).
		Scan(&n); err != nil || n != 1 {
		t.Fatalf("messages must stay intact: n=%d err=%v", n, err)
	}
}

func TestHandleSessionSearch_FromDB(t *testing.T) {
	configPath, db := setupSessionDB(t)
	seedSessionMsg(t, db, "sk_v1_aaa", "user", "мой любимый цвет синий")
	seedSessionMsg(t, db, "sk_v1_bbb", "user", "разговор про погоду")

	mux := newSessionMux(configPath)

	items := listSessions(t, mux, "/api/sessions?q=синий")
	if len(items) != 1 || items[0].ID != "sk_v1_aaa" {
		t.Fatalf("q=синий: %+v", items)
	}
	// FTS5 matches whole tokens, no RU stemming: "погода" != "погоду".
	items = listSessions(t, mux, "/api/sessions?q=погоду")
	if len(items) != 1 || items[0].ID != "sk_v1_bbb" {
		t.Fatalf("q=погоду: %+v", items)
	}
}
