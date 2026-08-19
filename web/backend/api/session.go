package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// D-AUDIT-109: chat history is served from bot_memory.db (SQLite).
// The v3 runtime stores all messages there via PikaSessionStore; the legacy
// filesystem session store (~/.picoclaw/workspace/sessions) is no longer
// written, so the old file-based listing always returned empty.

const (
	chatScopeRegPrefix   = "chat_scope:"
	chatTitleRegPrefix   = "chat_title:"
	chatHiddenRegPrefix  = "chat_hidden:"
	sessionTitleMaxRunes = 60
	sessionAPITimeout    = 10 * time.Second
)

// sessionListItem is a lightweight summary returned by GET /api/sessions.
type sessionListItem struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Preview      string `json:"preview"`
	MessageCount int    `json:"message_count"`
	Created      string `json:"created"`
	Updated      string `json:"updated"`
	Resumable    bool   `json:"resumable"`
}

// sessionChatMessage is one visible message in GET /api/sessions/{id}.
type sessionChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// registerSessionRoutes binds session list/detail/rename/hide endpoints.
func (h *Handler) registerSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions", h.handleListSessions)
	mux.HandleFunc("GET /api/sessions/{id}", h.handleGetSession)
	mux.HandleFunc("PATCH /api/sessions/{id}", h.handleRenameSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", h.handleDeleteSession)
}

// openPikaDBRW opens bot_memory.db for writes (rename/hide flags in the
// registry). WAL + busy_timeout tolerate the gateway writing concurrently.
func openPikaDBRW(path string) (*sql.DB, error) {
	return sql.Open(
		"sqlite",
		fmt.Sprintf("file:%s?mode=rw&_pragma=busy_timeout(5000)", path),
	)
}

// sqliteToRFC3339 converts "2006-01-02 15:04:05" (UTC) to RFC3339 for dayjs.
func sqliteToRFC3339(s string) string {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return s
	}
	return t.UTC().Format(time.RFC3339)
}

func truncateSessionRunes(s string, maxLen int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxLen {
		return string(runes)
	}
	return string(runes[:maxLen]) + "..."
}

// sessionFTSQuery builds a safe FTS5 MATCH: words OR'ed, each quoted.
func sessionFTSQuery(q string) string {
	words := strings.Fields(q)
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.ReplaceAll(word, `"`, "")
		if word != "" {
			quoted = append(quoted, `"`+word+`"`)
		}
	}
	return strings.Join(quoted, " OR ")
}

// resolveSessionChatID maps a frontend session id (pico UUID) or a raw
// canonical sk_v1_ key to the messages.chat_id value. "" if unknown.
func resolveSessionChatID(ctx context.Context, db *sql.DB, id string) string {
	if strings.HasPrefix(id, "sk_v1_") {
		return id
	}
	var key string
	err := db.QueryRowContext(ctx,
		`SELECT key FROM registry
		 WHERE kind='snapshot' AND key LIKE 'chat_scope:%' AND summary=?`,
		"pico:"+id).Scan(&key)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(key, chatScopeRegPrefix)
}

// sessionDBPath resolves the db path and reports existence.
func (h *Handler) sessionDBPath() (string, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return "", err
	}
	return resolvePikaDBPath(cfg), nil
}

// handleListSessions returns chat summaries from bot_memory.db.
//
//	GET /api/sessions?offset=0&limit=20&q=...
func (h *Handler) handleListSessions(w http.ResponseWriter, r *http.Request) {
	dbPath, err := h.sessionDBPath()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err),
			http.StatusInternalServerError)
		return
	}
	if _, stErr := os.Stat(dbPath); stErr != nil {
		writePikaJSON(w, []sessionListItem{})
		return
	}
	db, err := openPikaDBRO(dbPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open memory db: %v", err),
			http.StatusInternalServerError)
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(r.Context(), sessionAPITimeout)
	defer cancel()

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	var args []any
	var sb strings.Builder
	sb.WriteString(`SELECT m.chat_id, COUNT(*), MIN(m.ts), MAX(m.ts),
		(SELECT u.content FROM messages u
		 WHERE u.chat_id=m.chat_id AND u.role='user' ORDER BY u.id LIMIT 1),
		(SELECT t.summary FROM registry t
		 WHERE t.kind='snapshot' AND t.key='chat_title:'||m.chat_id),
		(SELECT s.summary FROM registry s
		 WHERE s.kind='snapshot' AND s.key='chat_scope:'||m.chat_id)
		FROM messages m
		WHERE NOT EXISTS (SELECT 1 FROM registry hd
		 WHERE hd.kind='snapshot' AND hd.key='chat_hidden:'||m.chat_id)`)
	if q != "" {
		sb.WriteString(` AND m.chat_id IN (
			SELECT mm.chat_id FROM messages_fts f
			JOIN messages mm ON mm.id=f.rowid
			WHERE messages_fts MATCH ?)`)
		args = append(args, sessionFTSQuery(q))
	}
	sb.WriteString(` GROUP BY m.chat_id ORDER BY MAX(m.ts) DESC LIMIT ? OFFSET ?`)
	args = append(args, limit, offset)

	// #nosec G202 -- static SQL with optional static fragment; values parameterized
	rows, err := db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list sessions: %v", err),
			http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	items := []sessionListItem{}
	for rows.Next() {
		var chatID, minTS, maxTS string
		var count int
		var firstUser, customTitle, scopeSummary sql.NullString
		if scanErr := rows.Scan(
			&chatID, &count, &minTS, &maxTS,
			&firstUser, &customTitle, &scopeSummary,
		); scanErr != nil {
			continue
		}
		id := chatID
		resumable := false
		if scopeSummary.Valid &&
			strings.HasPrefix(scopeSummary.String, "pico:") {
			id = strings.TrimPrefix(scopeSummary.String, "pico:")
			resumable = true
		}
		title := ""
		if customTitle.Valid {
			title = strings.TrimSpace(customTitle.String)
		}
		if title == "" {
			title = truncateSessionRunes(firstUser.String, sessionTitleMaxRunes)
		}
		if title == "" {
			title = "(empty)"
		}
		items = append(items, sessionListItem{
			ID:           id,
			Title:        title,
			Preview:      title,
			MessageCount: count,
			Created:      sqliteToRFC3339(minTS),
			Updated:      sqliteToRFC3339(maxTS),
			Resumable:    resumable,
		})
	}
	writePikaJSON(w, items)
}

// handleGetSession returns visible messages of one chat.
//
//	GET /api/sessions/{id}
func (h *Handler) handleGetSession(w http.ResponseWriter, r *http.Request) {
	dbPath, err := h.sessionDBPath()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err),
			http.StatusInternalServerError)
		return
	}
	if _, stErr := os.Stat(dbPath); stErr != nil {
		http.NotFound(w, r)
		return
	}
	db, err := openPikaDBRO(dbPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open memory db: %v", err),
			http.StatusInternalServerError)
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(r.Context(), sessionAPITimeout)
	defer cancel()

	id := r.PathValue("id")
	chatID := resolveSessionChatID(ctx, db, id)
	if chatID == "" {
		http.NotFound(w, r)
		return
	}

	rows, err := db.QueryContext(ctx,
		`SELECT role, content FROM messages
		 WHERE chat_id=? AND role IN ('user','assistant')
		   AND content IS NOT NULL AND content != ''
		 ORDER BY id ASC`, chatID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load session: %v", err),
			http.StatusInternalServerError)
		return
	}
	msgs := []sessionChatMessage{}
	for rows.Next() {
		var m sessionChatMessage
		if scanErr := rows.Scan(&m.Role, &m.Content); scanErr != nil {
			continue
		}
		msgs = append(msgs, m)
	}
	rows.Close()
	if len(msgs) == 0 {
		http.NotFound(w, r)
		return
	}

	var minTS, maxTS string
	_ = db.QueryRowContext(ctx,
		`SELECT MIN(ts), MAX(ts) FROM messages WHERE chat_id=?`,
		chatID).Scan(&minTS, &maxTS)

	writePikaJSON(w, map[string]any{
		"id":       id,
		"messages": msgs,
		"summary":  "",
		"created":  sqliteToRFC3339(minTS),
		"updated":  sqliteToRFC3339(maxTS),
	})
}

// upsertChatRegistry resolves {id} to a chat and upserts a registry flag.
func (h *Handler) upsertChatRegistry(
	w http.ResponseWriter, r *http.Request, keyPrefix, value string,
) {
	dbPath, err := h.sessionDBPath()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err),
			http.StatusInternalServerError)
		return
	}
	db, err := openPikaDBRW(dbPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open memory db: %v", err),
			http.StatusInternalServerError)
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(r.Context(), sessionAPITimeout)
	defer cancel()

	chatID := resolveSessionChatID(ctx, db, r.PathValue("id"))
	if chatID == "" {
		http.NotFound(w, r)
		return
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO registry (kind,key,summary) VALUES ('snapshot',?,?)
		 ON CONFLICT(kind,key) DO UPDATE
		 SET summary=excluded.summary, ts=CURRENT_TIMESTAMP`,
		keyPrefix+chatID, value)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to update session: %v", err),
			http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRenameSession sets a custom chat title.
//
//	PATCH /api/sessions/{id} {"title": "..."}
func (h *Handler) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	title := truncateSessionRunes(body.Title, sessionTitleMaxRunes)
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	h.upsertChatRegistry(w, r, chatTitleRegPrefix, title)
}

// handleDeleteSession HIDES the chat from the history list. The messages
// stay in bot_memory.db untouched — memory and atoms are never affected
// (founder's rule, D-AUDIT-109).
//
//	DELETE /api/sessions/{id}
func (h *Handler) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	h.upsertChatRegistry(w, r, chatHiddenRegPrefix, "1")
}
