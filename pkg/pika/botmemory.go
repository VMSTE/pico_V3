package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const sqliteTimeFmt = "2006-01-02 15:04:05"

var categoryPrefix = map[string]string{
	"pattern":       "P-",
	"constraint":    "C-",
	"decision":      "D-",
	"tool_pref":     "T-",
	"summary":       "S-",
	"runbook_draft": "R-",
}

type MessageRow struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	TurnID    int             `json:"turn_id"`
	Ts        time.Time       `json:"ts"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Tokens    int             `json:"tokens"`
	MsgIndex  *int            `json:"msg_index,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

type EventRow struct {
	ID        int64           `json:"id"`
	Ts        time.Time       `json:"ts"`
	Type      string          `json:"type"`
	Summary   string          `json:"summary"`
	Outcome   string          `json:"outcome"`
	Tags      json.RawMessage `json:"tags,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	SessionID string          `json:"session_id"`
	TurnID    int             `json:"turn_id"`
}

type KnowledgeAtomRow struct {
	ID              int64           `json:"id"`
	AtomID          string          `json:"atom_id"`
	SessionID       string          `json:"session_id"`
	TurnID          int             `json:"turn_id"`
	SourceEventID   *int64          `json:"source_event_id,omitempty"`
	SourceMessageID *int64          `json:"source_message_id,omitempty"`
	Category        string          `json:"category"`
	Summary         string          `json:"summary"`
	Detail          string          `json:"detail,omitempty"`
	Confidence      float64         `json:"confidence"`
	Polarity        string          `json:"polarity"`
	Verified        int             `json:"verified"`
	Tags            json.RawMessage `json:"tags,omitempty"`
	SourceTurns     json.RawMessage `json:"source_turns,omitempty"`
	History         json.RawMessage `json:"history,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type RegistryRow struct {
	ID       int64           `json:"id"`
	Ts       time.Time       `json:"ts"`
	Kind     string          `json:"kind"`
	Key      string          `json:"key"`
	Summary  string          `json:"summary,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Verified int             `json:"verified"`
	LastUsed *time.Time      `json:"last_used,omitempty"`
	Tags     json.RawMessage `json:"tags,omitempty"`
}

type RequestLogRow struct {
	SessionID          string          `json:"session_id,omitempty"`
	MsgIndex           *int            `json:"msg_index,omitempty"`
	Direction          string          `json:"direction"`
	Component          string          `json:"component"`
	Model              string          `json:"model"`
	PromptTokens       int             `json:"prompt_tokens"`
	CompletionTokens   int             `json:"completion_tokens"`
	CachedTokens       int             `json:"cached_tokens"`
	ReasoningTokens    int             `json:"reasoning_tokens"`
	EstimatedTokens    *int            `json:"estimated_tokens,omitempty"`
	ToolCallsRequested int             `json:"tool_calls_requested"`
	ToolCallsSuccess   int             `json:"tool_calls_success"`
	ToolCallsFailed    int             `json:"tool_calls_failed"`
	ToolNames          json.RawMessage `json:"tool_names,omitempty"`
	CostUSD            float64         `json:"cost_usd"`
	Error              string          `json:"error,omitempty"`
	RetryCount         int             `json:"retry_count"`
	ResponseMs         int             `json:"response_ms"`
	TaskTag            string          `json:"task_tag,omitempty"`
	ChainID            string          `json:"chain_id,omitempty"`
	ChainPosition      *int            `json:"chain_position,omitempty"`
	PlanDetected       int             `json:"plan_detected"`
}

type ReasoningLogRow struct {
	SessionID         string          `json:"session_id,omitempty"`
	MsgIndex          *int            `json:"msg_index,omitempty"`
	Task              string          `json:"task,omitempty"`
	Mode              string          `json:"mode,omitempty"`
	ReasoningText     string          `json:"reasoning_text,omitempty"`
	ReasoningTokens   int             `json:"reasoning_tokens"`
	PromptComponents  json.RawMessage `json:"prompt_components,omitempty"`
	ToolCalls         json.RawMessage `json:"tool_calls,omitempty"`
	ContextPct        float64         `json:"context_pct"`
	ReasoningKeywords json.RawMessage `json:"reasoning_keywords,omitempty"`
	TurnID            int             `json:"turn_id"`
}

type TraceSpanRow struct {
	SpanID       string          `json:"span_id"`
	ParentSpanID string          `json:"parent_span_id,omitempty"`
	TraceID      string          `json:"trace_id"`
	SessionID    string          `json:"session_id,omitempty"`
	TurnID       *int            `json:"turn_id,omitempty"`
	Component    string          `json:"component"`
	Operation    string          `json:"operation"`
	StartedAt    time.Time       `json:"started_at"`
	Status       string          `json:"status"`
	InputData    json.RawMessage `json:"input_data,omitempty"`
}

type EventArchiveRow struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	TurnID    int             `json:"turn_id"`
	Ts        time.Time       `json:"ts"`
	Type      string          `json:"type"`
	Outcome   string          `json:"outcome"`
	Summary   string          `json:"summary"`
	Tags      json.RawMessage `json:"tags"`
}

type msgArchivePayload struct {
	Content  string          `json:"content"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type reasoningArchivePayload struct {
	ReasoningText    string          `json:"reasoning_text,omitempty"`
	ToolCalls        json.RawMessage `json:"tool_calls,omitempty"`
	PromptComponents json.RawMessage `json:"prompt_components,omitempty"`
}

// BotMemory is the sole SQL access layer for bot_memory.db.
type BotMemory struct {
	db      *sql.DB
	encoder *zstd.Encoder
	decoder *zstd.Decoder
}

// NewBotMemory creates a new BotMemory instance with zstd compression and recovers stale spans.
func NewBotMemory(db *sql.DB) (*BotMemory, error) {
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: zstd encoder: %w", err)
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: zstd decoder: %w", err)
	}
	bm := &BotMemory{db: db, encoder: enc, decoder: dec}
	if err := bm.recoverStaleSpans(context.Background()); err != nil {
		return nil, fmt.Errorf("pika/botmemory: recover spans: %w", err)
	}
	return bm, nil
}

// Close releases zstd encoder and decoder resources.
func (bm *BotMemory) Close() {
	bm.encoder.Close()
	bm.decoder.Close()
}

func (bm *BotMemory) compress(data []byte) []byte {
	return bm.encoder.EncodeAll(data, nil)
}

func (bm *BotMemory) decompress(data []byte) ([]byte, error) {
	return bm.decoder.DecodeAll(data, nil)
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func inArgs(sid string, ids []int) []any {
	a := make([]any, 0, 1+len(ids))
	a = append(a, sid)
	for _, id := range ids {
		a = append(a, id)
	}
	return a
}

func parseSQLiteTime(s string) time.Time {
	t, _ := time.Parse(sqliteTimeFmt, s)
	return t
}

func jsonArg(j json.RawMessage) any {
	if j == nil {
		return nil
	}
	return string(j)
}

func strOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SaveMessage persists a message row into the messages table.
func (bm *BotMemory) SaveMessage(ctx context.Context, m MessageRow) (int64, error) {
	res, err := bm.db.ExecContext(ctx,
		`INSERT INTO messages (session_id,turn_id,role,content,tokens,msg_index,metadata)
		VALUES(?,?,?,?,?,?,?)`,
		m.SessionID, m.TurnID, m.Role, m.Content, m.Tokens,
		m.MsgIndex, jsonArg(m.Metadata))
	if err != nil {
		return 0, fmt.Errorf("pika/botmemory: save message: %w", err)
	}
	return res.LastInsertId()
}

// GetMessages returns all messages for a session ordered by id.
func (bm *BotMemory) GetMessages(ctx context.Context, sid string) ([]MessageRow, error) {
	rows, err := bm.db.QueryContext(ctx,
		`SELECT id,session_id,turn_id,ts,role,content,tokens,msg_index,metadata
		FROM messages WHERE session_id=? ORDER BY id ASC`, sid)
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: get messages: %w", err)
	}
	defer rows.Close()
	var out []MessageRow
	for rows.Next() {
		var m MessageRow
		var ts string
		var content, meta sql.NullString
		var mi sql.NullInt64
		if err := rows.Scan(&m.ID, &m.SessionID, &m.TurnID, &ts, &m.Role, &content, &m.Tokens, &mi, &meta); err != nil {
			return nil, fmt.Errorf("pika/botmemory: scan message: %w", err)
		}
		m.Ts = parseSQLiteTime(ts)
		m.Content = content.String
		if mi.Valid {
			v := int(mi.Int64)
			m.MsgIndex = &v
		}
		if meta.Valid && meta.String != "" {
			m.Metadata = json.RawMessage(meta.String)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetDistinctSessionIDs returns all distinct session IDs from the messages table.
func (bm *BotMemory) GetDistinctSessionIDs(ctx context.Context) ([]string, error) {
	rows, err := bm.db.QueryContext(ctx, `SELECT DISTINCT session_id FROM messages`)
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: get session ids: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SumTokensBySession returns the total token count for a session.
func (bm *BotMemory) SumTokensBySession(ctx context.Context, sid string) (int64, error) {
	var s int64
	err := bm.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(tokens),0) FROM messages WHERE session_id=?`, sid).Scan(&s)
	if err != nil {
		return 0, fmt.Errorf("pika/botmemory: sum tokens: %w", err)
	}
	return s, nil
}

// GetOldestTurnIDs returns turn IDs from oldest to newest up to a token budget.
func (bm *BotMemory) GetOldestTurnIDs(ctx context.Context, sid string, budget int) ([]int, error) {
	rows, err := bm.db.QueryContext(ctx,
		`SELECT turn_id, SUM(tokens) as t FROM messages
		WHERE session_id=? GROUP BY turn_id ORDER BY MIN(id) ASC`, sid)
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: oldest turns: %w", err)
	}
	defer rows.Close()
	var ids []int
	var acc int
	for rows.Next() {
		var tid, tok int
		if err := rows.Scan(&tid, &tok); err != nil {
			return nil, err
		}
		if acc+tok > budget {
			break
		}
		acc += tok
		ids = append(ids, tid)
	}
	return ids, rows.Err()
}

// CountMessagesBySession returns the number of messages for a session.
func (bm *BotMemory) CountMessagesBySession(ctx context.Context, sid string) (int, error) {
	var c int
	err := bm.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE session_id=?`, sid).Scan(&c)
	if err != nil {
		return 0, fmt.Errorf("pika/botmemory: count messages: %w", err)
	}
	return c, nil
}

// GetMaxTurnID returns the highest turn_id for a session.
func (bm *BotMemory) GetMaxTurnID(ctx context.Context, sid string) (int, error) {
	var m int
	err := bm.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(turn_id),0) FROM messages WHERE session_id=?`, sid).Scan(&m)
	if err != nil {
		return 0, fmt.Errorf("pika/botmemory: max turn id: %w", err)
	}
	return m, nil
}

// DeleteAllMessages removes all messages for a session.
func (bm *BotMemory) DeleteAllMessages(ctx context.Context, sid string) error {
	_, err := bm.db.ExecContext(ctx, `DELETE FROM messages WHERE session_id=?`, sid)
	if err != nil {
		return fmt.Errorf("pika/botmemory: delete messages: %w", err)
	}
	return nil
}

// SaveEvent persists an event row into the events table.
func (bm *BotMemory) SaveEvent(ctx context.Context, e EventRow) (int64, error) {
	res, err := bm.db.ExecContext(ctx,
		`INSERT INTO events (type,summary,outcome,tags,data,session_id,turn_id)
		VALUES(?,?,?,?,?,?,?)`,
		e.Type, e.Summary, strOrNil(e.Outcome),
		jsonArg(e.Tags), jsonArg(e.Data), e.SessionID, e.TurnID)
	if err != nil {
		return 0, fmt.Errorf("pika/botmemory: save event: %w", err)
	}
	return res.LastInsertId()
}

// GetEventsByTurns returns events for specific turn IDs in a session.
func (bm *BotMemory) GetEventsByTurns(ctx context.Context, sid string, tids []int) ([]EventRow, error) {
	if len(tids) == 0 {
		return nil, nil
	}
	args := inArgs(sid, tids)
	rows, err := bm.db.QueryContext(ctx,
		`SELECT id,ts,type,summary,outcome,tags,data,session_id,turn_id
		FROM events WHERE session_id=? AND turn_id IN (`+placeholders(len(tids))+`)
		ORDER BY id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: get events: %w", err)
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var ts string
		var outcome, tags, data sql.NullString
		err := rows.Scan(
			&e.ID, &ts, &e.Type, &e.Summary, &outcome,
			&tags, &data, &e.SessionID, &e.TurnID,
		)
		if err != nil {
			return nil, fmt.Errorf("pika/botmemory: scan event: %w", err)
		}
		e.Ts = parseSQLiteTime(ts)
		e.Outcome = outcome.String
		if tags.Valid {
			e.Tags = json.RawMessage(tags.String)
		}
		if data.Valid {
			e.Data = json.RawMessage(data.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertAtom persists a knowledge atom row.
func (bm *BotMemory) InsertAtom(ctx context.Context, a KnowledgeAtomRow) error {
	_, err := bm.db.ExecContext(ctx,
		`INSERT INTO knowledge_atoms
		(atom_id,session_id,turn_id,source_event_id,source_message_id,
		category,summary,detail,confidence,polarity,verified,tags,source_turns,history)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.AtomID, a.SessionID, a.TurnID, a.SourceEventID, a.SourceMessageID,
		a.Category, a.Summary, strOrNil(a.Detail), a.Confidence, a.Polarity,
		a.Verified, jsonArg(a.Tags), jsonArg(a.SourceTurns), jsonArg(a.History))
	if err != nil {
		return fmt.Errorf("pika/botmemory: insert atom: %w", err)
	}
	return nil
}

// QueryKnowledgeFTS searches knowledge atoms via FTS5 index.
func (bm *BotMemory) QueryKnowledgeFTS(ctx context.Context, q string, limit int) ([]KnowledgeAtomRow, error) {
	rows, err := bm.db.QueryContext(ctx,
		`SELECT ka.id,ka.atom_id,ka.session_id,ka.turn_id,
		ka.source_event_id,ka.source_message_id,ka.category,ka.summary,
		ka.detail,ka.confidence,ka.polarity,ka.verified,
		ka.tags,ka.source_turns,ka.history,ka.created_at,ka.updated_at
		FROM knowledge_atoms ka
		JOIN knowledge_fts kf ON ka.id=kf.rowid
		WHERE knowledge_fts MATCH ? ORDER BY kf.rank LIMIT ?`, q, limit)
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: fts: %w", err)
	}
	defer rows.Close()
	var out []KnowledgeAtomRow
	for rows.Next() {
		var a KnowledgeAtomRow
		var se, sm sql.NullInt64
		var det, tg, st, hi sql.NullString
		var ca, ua string
		scanErr := rows.Scan(
			&a.ID, &a.AtomID, &a.SessionID, &a.TurnID,
			&se, &sm, &a.Category, &a.Summary, &det,
			&a.Confidence, &a.Polarity, &a.Verified,
			&tg, &st, &hi, &ca, &ua,
		)
		if scanErr != nil {
			return nil, fmt.Errorf("pika/botmemory: scan atom: %w", scanErr)
		}
		if se.Valid {
			a.SourceEventID = &se.Int64
		}
		if sm.Valid {
			a.SourceMessageID = &sm.Int64
		}
		a.Detail = det.String
		if tg.Valid {
			a.Tags = json.RawMessage(tg.String)
		}
		if st.Valid {
			a.SourceTurns = json.RawMessage(st.String)
		}
		if hi.Valid {
			a.History = json.RawMessage(hi.String)
		}
		a.CreatedAt = parseSQLiteTime(ca)
		a.UpdatedAt = parseSQLiteTime(ua)
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateAtomConfidence updates confidence and appends a history entry for an atom.
func (bm *BotMemory) UpdateAtomConfidence(
	ctx context.Context, atomID string, conf float64, hist json.RawMessage,
) error {
	_, err := bm.db.ExecContext(ctx,
		`UPDATE knowledge_atoms
		SET confidence=?, history=json_insert(COALESCE(history,'[]'),'$[#]',json(?)),
		updated_at=CURRENT_TIMESTAMP WHERE atom_id=?`,
		conf, string(hist), atomID)
	if err != nil {
		return fmt.Errorf("pika/botmemory: update confidence: %w", err)
	}
	return nil
}

// GetMaxAtomN returns the highest numeric suffix for atoms of a given category.
func (bm *BotMemory) GetMaxAtomN(ctx context.Context, cat string) (int, error) {
	p, ok := categoryPrefix[cat]
	if !ok {
		return 0, fmt.Errorf("pika/botmemory: unknown category %q", cat)
	}
	var n int
	err := bm.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(CAST(SUBSTR(atom_id,3) AS INTEGER)),0)
		FROM knowledge_atoms WHERE category=? AND atom_id LIKE ?`,
		cat, p+"%").Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("pika/botmemory: max atom n: %w", err)
	}
	return n, nil
}

// UpsertRegistry inserts or updates a registry row. Returns true if inserted, false if updated.
func (bm *BotMemory) UpsertRegistry(ctx context.Context, r RegistryRow) (bool, error) {
	res, err := bm.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO registry (kind,key,summary,data,verified,tags)
		VALUES(?,?,?,?,?,?)`,
		r.Kind, r.Key, r.Summary, jsonArg(r.Data), r.Verified, jsonArg(r.Tags))
	if err != nil {
		return false, fmt.Errorf("pika/botmemory: upsert insert: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		return true, nil
	}
	_, err = bm.db.ExecContext(ctx,
		`UPDATE registry SET summary=?, data=?, tags=?, ts=CURRENT_TIMESTAMP
		WHERE kind=? AND key=?`,
		r.Summary, jsonArg(r.Data), jsonArg(r.Tags), r.Kind, r.Key)
	if err != nil {
		return false, fmt.Errorf("pika/botmemory: upsert update: %w", err)
	}
	return false, nil
}

// GetRegistry returns a single registry row by kind and key.
func (bm *BotMemory) GetRegistry(ctx context.Context, kind, key string) (*RegistryRow, error) {
	var r RegistryRow
	var ts string
	var sum, data, tags, lu sql.NullString
	err := bm.db.QueryRowContext(ctx,
		`SELECT id,ts,kind,key,summary,data,verified,last_used,tags
		FROM registry WHERE kind=? AND key=?`,
		kind, key).Scan(&r.ID, &ts, &r.Kind, &r.Key, &sum, &data, &r.Verified, &lu, &tags)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: get registry: %w", err)
	}
	r.Ts = parseSQLiteTime(ts)
	r.Summary = sum.String
	if data.Valid {
		r.Data = json.RawMessage(data.String)
	}
	if lu.Valid && lu.String != "" {
		t := parseSQLiteTime(lu.String)
		r.LastUsed = &t
	}
	if tags.Valid {
		r.Tags = json.RawMessage(tags.String)
	}
	return &r, nil
}

// SearchRegistry returns registry rows matching a kind and key LIKE pattern.
func (bm *BotMemory) SearchRegistry(ctx context.Context, kind, pat string) ([]RegistryRow, error) {
	rows, err := bm.db.QueryContext(ctx,
		`SELECT id,ts,kind,key,summary,data,verified,last_used,tags
		FROM registry WHERE kind=? AND key LIKE ?`,
		kind, pat)
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: search registry: %w", err)
	}
	defer rows.Close()
	var out []RegistryRow
	for rows.Next() {
		var r RegistryRow
		var ts string
		var sum, data, tags, lu sql.NullString
		if err := rows.Scan(&r.ID, &ts, &r.Kind, &r.Key, &sum, &data, &r.Verified, &lu, &tags); err != nil {
			return nil, fmt.Errorf("pika/botmemory: scan registry: %w", err)
		}
		r.Ts = parseSQLiteTime(ts)
		r.Summary = sum.String
		if data.Valid {
			r.Data = json.RawMessage(data.String)
		}
		if lu.Valid && lu.String != "" {
			t := parseSQLiteTime(lu.String)
			r.LastUsed = &t
		}
		if tags.Valid {
			r.Tags = json.RawMessage(tags.String)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateRegistryLastUsed updates the last_used timestamp for a registry row.
func (bm *BotMemory) UpdateRegistryLastUsed(ctx context.Context, kind, key string) error {
	_, err := bm.db.ExecContext(ctx,
		`UPDATE registry SET last_used=CURRENT_TIMESTAMP WHERE kind=? AND key=?`,
		kind, key)
	if err != nil {
		return fmt.Errorf("pika/botmemory: update last_used: %w", err)
	}
	return nil
}

// InsertRequestLog persists a request log row.
func (bm *BotMemory) InsertRequestLog(ctx context.Context, r RequestLogRow) (int64, error) {
	res, err := bm.db.ExecContext(ctx,
		`INSERT INTO request_log
		(session_id,msg_index,direction,component,model,
		prompt_tokens,completion_tokens,cached_tokens,reasoning_tokens,estimated_tokens,
		tool_calls_requested,tool_calls_success,tool_calls_failed,tool_names,
		cost_usd,error,retry_count,response_ms,task_tag,chain_id,chain_position,plan_detected)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		strOrNil(r.SessionID), r.MsgIndex, r.Direction, r.Component, r.Model,
		r.PromptTokens, r.CompletionTokens, r.CachedTokens, r.ReasoningTokens, r.EstimatedTokens,
		r.ToolCallsRequested, r.ToolCallsSuccess, r.ToolCallsFailed, jsonArg(r.ToolNames),
		r.CostUSD, strOrNil(r.Error), r.RetryCount, r.ResponseMs,
		strOrNil(r.TaskTag), strOrNil(r.ChainID), r.ChainPosition, r.PlanDetected)
	if err != nil {
		return 0, fmt.Errorf("pika/botmemory: insert request log: %w", err)
	}
	return res.LastInsertId()
}

// InsertReasoningLog persists a reasoning log row.
func (bm *BotMemory) InsertReasoningLog(ctx context.Context, r ReasoningLogRow) (int64, error) {
	res, err := bm.db.ExecContext(ctx,
		`INSERT INTO reasoning_log
		(session_id,msg_index,task,mode,reasoning_text,reasoning_tokens,
		prompt_components,tool_calls,context_pct,reasoning_keywords,turn_id)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		strOrNil(r.SessionID), r.MsgIndex, strOrNil(r.Task), strOrNil(r.Mode),
		strOrNil(r.ReasoningText), r.ReasoningTokens,
		jsonArg(r.PromptComponents), jsonArg(r.ToolCalls),
		r.ContextPct, jsonArg(r.ReasoningKeywords), r.TurnID)
	if err != nil {
		return 0, fmt.Errorf("pika/botmemory: insert reasoning: %w", err)
	}
	return res.LastInsertId()
}

// GetReasoningByTurns returns reasoning log rows for specific turn IDs in a session.
func (bm *BotMemory) GetReasoningByTurns(ctx context.Context, sid string, tids []int) ([]ReasoningLogRow, error) {
	if len(tids) == 0 {
		return nil, nil
	}
	args := inArgs(sid, tids)
	rows, err := bm.db.QueryContext(ctx,
		`SELECT session_id,msg_index,task,mode,reasoning_text,reasoning_tokens,
		prompt_components,tool_calls,context_pct,reasoning_keywords,turn_id
		FROM reasoning_log WHERE session_id=? AND turn_id IN (`+placeholders(len(tids))+`)
		ORDER BY id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: get reasoning: %w", err)
	}
	defer rows.Close()
	var out []ReasoningLogRow
	for rows.Next() {
		var r ReasoningLogRow
		var s, tk, md, rt sql.NullString
		var mi sql.NullInt64
		var pc, tc, rk sql.NullString
		var cp sql.NullFloat64
		if err := rows.Scan(&s, &mi, &tk, &md, &rt, &r.ReasoningTokens, &pc, &tc, &cp, &rk, &r.TurnID); err != nil {
			return nil, fmt.Errorf("pika/botmemory: scan reasoning: %w", err)
		}
		r.SessionID = s.String
		if mi.Valid {
			v := int(mi.Int64)
			r.MsgIndex = &v
		}
		r.Task = tk.String
		r.Mode = md.String
		r.ReasoningText = rt.String
		if pc.Valid {
			r.PromptComponents = json.RawMessage(pc.String)
		}
		if tc.Valid {
			r.ToolCalls = json.RawMessage(tc.String)
		}
		if cp.Valid {
			r.ContextPct = cp.Float64
		}
		if rk.Valid {
			r.ReasoningKeywords = json.RawMessage(rk.String)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertSpan persists a trace span row.
func (bm *BotMemory) InsertSpan(ctx context.Context, s TraceSpanRow) error {
	_, err := bm.db.ExecContext(ctx,
		`INSERT INTO trace_spans
		(span_id,parent_span_id,trace_id,session_id,turn_id,
		component,operation,started_at,status,input_data)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		s.SpanID, strOrNil(s.ParentSpanID), s.TraceID,
		strOrNil(s.SessionID), s.TurnID, s.Component, s.Operation,
		s.StartedAt.UTC().Format(sqliteTimeFmt), s.Status, jsonArg(s.InputData))
	if err != nil {
		return fmt.Errorf("pika/botmemory: insert span: %w", err)
	}
	return nil
}

// CompleteSpan marks a trace span as completed with status and optional output/error.
func (bm *BotMemory) CompleteSpan(
	ctx context.Context, spanID, status string,
	outputData json.RawMessage, errType, errMsg string,
) error {
	_, err := bm.db.ExecContext(ctx,
		`UPDATE trace_spans SET completed_at=CURRENT_TIMESTAMP,
		status=?, output_data=?, error_type=?, error_message=?
		WHERE span_id=?`,
		status, jsonArg(outputData), strOrNil(errType), strOrNil(errMsg), spanID)
	if err != nil {
		return fmt.Errorf("pika/botmemory: complete span: %w", err)
	}
	return nil
}

func (bm *BotMemory) recoverStaleSpans(ctx context.Context) error {
	_, err := bm.db.ExecContext(ctx,
		`UPDATE trace_spans SET completed_at=CURRENT_TIMESTAMP,
		status='error', error_type='crash_recovery',
		error_message='process restarted'
		WHERE completed_at IS NULL`)
	if err != nil {
		return fmt.Errorf("pika/botmemory: recover spans: %w", err)
	}
	return nil
}

// ArchiveAndDeleteTurns archives messages+events+reasoning for turnIDs
// into *_archive tables, then deletes from hot. Single TX.
func (bm *BotMemory) ArchiveAndDeleteTurns(ctx context.Context, sid string, turnIDs []int) error {
	if len(turnIDs) == 0 {
		return nil
	}
	tx, err := bm.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("pika/botmemory: begin archive tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	ph := placeholders(len(turnIDs))
	args := inArgs(sid, turnIDs)
	// messages -> messages_archive
	mRows, err := tx.QueryContext(ctx,
		`SELECT id,session_id,turn_id,ts,role,content,tokens,metadata
		FROM messages WHERE session_id=? AND turn_id IN (`+ph+`)
		ORDER BY id`, args...)
	if err != nil {
		return fmt.Errorf("pika/botmemory: archive select msgs: %w", err)
	}
	defer mRows.Close()
	for mRows.Next() {
		var id int64
		var sessID string
		var turnID, tokens int
		var ts, role string
		var content, meta sql.NullString
		scanErr := mRows.Scan(&id, &sessID, &turnID, &ts, &role, &content, &tokens, &meta)
		if scanErr != nil {
			return fmt.Errorf("pika/botmemory: archive scan msg: %w", scanErr)
		}
		pay := msgArchivePayload{Content: content.String}
		if meta.Valid {
			pay.Metadata = json.RawMessage(meta.String)
		}
		j, _ := json.Marshal(pay)
		blob := bm.compress(j)
		_, err = tx.ExecContext(ctx,
			`INSERT INTO messages_archive (id,session_id,turn_id,ts,role,tokens,blob)
			VALUES(?,?,?,?,?,?,?)`,
			id, sessID, turnID, ts, role, tokens, blob)
		if err != nil {
			return fmt.Errorf("pika/botmemory: archive insert msg: %w", err)
		}
	}
	if rowErr := mRows.Err(); rowErr != nil {
		return fmt.Errorf("pika/botmemory: archive iter msgs: %w", rowErr)
	}
	// events -> events_archive
	eRows, err := tx.QueryContext(ctx,
		`SELECT id,ts,type,summary,outcome,tags,data,session_id,turn_id
		FROM events WHERE session_id=? AND turn_id IN (`+ph+`)`, args...)
	if err != nil {
		return fmt.Errorf("pika/botmemory: archive select evts: %w", err)
	}
	defer eRows.Close()
	for eRows.Next() {
		var id int64
		var ts, typ, summary string
		var outcome, tags, data sql.NullString
		var sessID string
		var turnID int
		scanErr := eRows.Scan(&id, &ts, &typ, &summary, &outcome, &tags, &data, &sessID, &turnID)
		if scanErr != nil {
			return fmt.Errorf("pika/botmemory: archive scan evt: %w", scanErr)
		}
		var blob []byte
		if data.Valid {
			blob = bm.compress([]byte(data.String))
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO events_archive (id,session_id,turn_id,ts,type,outcome,summary,tags,blob)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			id, sessID, turnID, ts, typ, outcome.String, summary, tags.String, blob)
		if err != nil {
			return fmt.Errorf("pika/botmemory: archive insert evt: %w", err)
		}
	}
	if rowErr := eRows.Err(); rowErr != nil {
		return fmt.Errorf("pika/botmemory: archive iter evts: %w", rowErr)
	}
	// reasoning_log -> reasoning_log_archive
	rRows, err := tx.QueryContext(ctx,
		`SELECT id,session_id,turn_id,ts,task,mode,reasoning_text,
		reasoning_tokens,prompt_components,tool_calls,context_pct,
		reasoning_keywords,msg_index
		FROM reasoning_log WHERE session_id=? AND turn_id IN (`+ph+`)`, args...)
	if err != nil {
		return fmt.Errorf("pika/botmemory: archive select reas: %w", err)
	}
	defer rRows.Close()
	for rRows.Next() {
		var id int64
		var sessID string
		var turnID int
		var ts string
		var task, mode, rText sql.NullString
		var rTokens int
		var pc, tc sql.NullString
		var cpct sql.NullFloat64
		var rk sql.NullString
		var mi sql.NullInt64
		scanErr := rRows.Scan(&id, &sessID, &turnID, &ts, &task, &mode, &rText, &rTokens, &pc, &tc, &cpct, &rk, &mi)
		if scanErr != nil {
			return fmt.Errorf("pika/botmemory: archive scan reas: %w", scanErr)
		}
		pay := reasoningArchivePayload{ReasoningText: rText.String}
		if tc.Valid {
			pay.ToolCalls = json.RawMessage(tc.String)
		}
		if pc.Valid {
			pay.PromptComponents = json.RawMessage(pc.String)
		}
		j, _ := json.Marshal(pay)
		blob := bm.compress(j)
		var cval float64
		if cpct.Valid {
			cval = cpct.Float64
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO reasoning_log_archive
			(id,session_id,turn_id,ts,task,mode,reasoning_tokens,
			context_pct,reasoning_keywords,msg_index,blob)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, sessID, turnID, ts, task.String, mode.String,
			rTokens, cval, rk.String, mi, blob)
		if err != nil {
			return fmt.Errorf("pika/botmemory: archive insert reas: %w", err)
		}
	}
	if rowErr := rRows.Err(); rowErr != nil {
		return fmt.Errorf("pika/botmemory: archive iter reas: %w", rowErr)
	}
	// Delete hot data
	for _, tbl := range []string{"messages", "events", "reasoning_log"} {
		_, err = tx.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE session_id=? AND turn_id IN (`+ph+`)`, tbl), args...)
		if err != nil {
			return fmt.Errorf("pika/botmemory: archive delete %s: %w", tbl, err)
		}
	}
	return tx.Commit()
}

// ReadArchivedMessage reads and decompresses an archived message by id.
func (bm *BotMemory) ReadArchivedMessage(ctx context.Context, id int64) (string, json.RawMessage, error) {
	var blob []byte
	err := bm.db.QueryRowContext(ctx,
		`SELECT blob FROM messages_archive WHERE id=?`, id).Scan(&blob)
	if err != nil {
		return "", nil, fmt.Errorf("pika/botmemory: read archived msg: %w", err)
	}
	raw, err := bm.decompress(blob)
	if err != nil {
		return "", nil, fmt.Errorf("pika/botmemory: decompress msg: %w", err)
	}
	var p msgArchivePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", nil, fmt.Errorf("pika/botmemory: unmarshal msg: %w", err)
	}
	return p.Content, p.Metadata, nil
}

// SearchEventsArchiveFTS searches archived events via FTS5 index.
func (bm *BotMemory) SearchEventsArchiveFTS(ctx context.Context, q string, limit int) ([]EventArchiveRow, error) {
	rows, err := bm.db.QueryContext(ctx,
		`SELECT ea.id,ea.session_id,ea.turn_id,ea.ts,ea.type,
		ea.outcome,ea.summary,ea.tags
		FROM events_archive ea
		JOIN events_archive_fts ef ON ea.id=ef.rowid
		WHERE events_archive_fts MATCH ? ORDER BY ef.rank LIMIT ?`, q, limit)
	if err != nil {
		return nil, fmt.Errorf("pika/botmemory: search archive fts: %w", err)
	}
	defer rows.Close()
	var out []EventArchiveRow
	for rows.Next() {
		var e EventArchiveRow
		var ts string
		var tags sql.NullString
		if err := rows.Scan(&e.ID, &e.SessionID, &e.TurnID, &ts, &e.Type, &e.Outcome, &e.Summary, &tags); err != nil {
			return nil, fmt.Errorf("pika/botmemory: scan archive evt: %w", err)
		}
		e.Ts = parseSQLiteTime(ts)
		if tags.Valid {
			e.Tags = json.RawMessage(tags.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpsertPromptVersion inserts or ignores a prompt version record.
func (bm *BotMemory) UpsertPromptVersion(
	ctx context.Context, component string, version int,
	hash, content, desc string,
) (string, error) {
	promptID := component + "/v" + strconv.Itoa(version)
	_, err := bm.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO prompt_versions
		(prompt_id,component,version,hash,content,change_description)
		VALUES(?,?,?,?,?,?)`,
		promptID, component, version, hash, content, strOrNil(desc))
	if err != nil {
		return "", fmt.Errorf("pika/botmemory: upsert prompt version: %w", err)
	}
	return promptID, nil
}

// InsertPromptSnapshot records a prompt composition snapshot for a trace.
func (bm *BotMemory) InsertPromptSnapshot(
	ctx context.Context, snapshotID, traceID, sessionID string,
	turnID int, coreID, ctxID, briefHash string,
	tokens map[string]int, fullHash, preview string, buildMs int,
) error {
	_, err := bm.db.ExecContext(ctx,
		`INSERT INTO prompt_snapshots
		(snapshot_id,trace_id,session_id,turn_id,
		core_prompt_id,context_prompt_id,brief_hash,
		core_tokens,context_tokens,brief_tokens,trail_tokens,plan_tokens,
		full_prompt_hash,full_prompt_preview,build_duration_ms)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		snapshotID, traceID, sessionID, turnID,
		strOrNil(coreID), strOrNil(ctxID), strOrNil(briefHash),
		tokens["core"], tokens["context"], tokens["brief"],
		tokens["trail"], tokens["plan"],
		strOrNil(fullHash), strOrNil(preview), buildMs)
	if err != nil {
		return fmt.Errorf("pika/botmemory: insert snapshot: %w", err)
	}
	return nil
}

// InsertAtomUsage records usage of a knowledge atom in a prompt.
func (bm *BotMemory) InsertAtomUsage(
	ctx context.Context, atomID, traceID string, turnID int,
	usedIn string, position, promptTokens *int,
	toolAfter, toolResult, archSpanID string,
) error {
	_, err := bm.db.ExecContext(ctx,
		`INSERT INTO atom_usage
		(atom_id,trace_id,turn_id,used_in,position_in_prompt,
		prompt_tokens,invoked_tool_after,invoked_tool_result,archivarius_span_id)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		atomID, traceID, turnID, usedIn, position, promptTokens,
		strOrNil(toolAfter), strOrNil(toolResult), strOrNil(archSpanID))
	if err != nil {
		return fmt.Errorf("pika/botmemory: insert atom usage: %w", err)
	}
	return nil
}
