// PIKA-V3: session_store.go — PikaSessionStore implements
// session.SessionStore with bot_memory.db (SQLite WAL) backend.
package pika

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tokenizer"
)

// PIKA-V3: compile-time checks
var _ session.SessionStore = (*PikaSessionStore)(nil)
var _ session.MetadataAwareSessionStore = (*PikaSessionStore)(nil)

// PikaSessionStore implements session.SessionStore with
// bot_memory.db backend. All persistence goes through BotMemory.
// No JSONL, no files.
type PikaSessionStore struct {
	mem *BotMemory

	// Per-session turn_id counter (in-memory, recovered
	// from DB on first access).
	mu      sync.Mutex
	turnIDs map[string]int // session_id → current turn_id

	// In-memory summary cache (legacy ContextManager compat).
	// Will be removed when PikaContextManager takes over
	// (wave 2+).
	summaryCache map[string]string

	// PIKA-V3: fix3 — in-memory session scope cache for
	// MetadataAwareSessionStore compliance. Maps session key
	// to its structured scope metadata.
	scopes map[string]*session.SessionScope
}

// NewPikaSessionStore creates a PikaSessionStore backed by
// the given BotMemory instance.
func NewPikaSessionStore(
	mem *BotMemory,
) *PikaSessionStore {
	return &PikaSessionStore{
		mem:          mem,
		turnIDs:      make(map[string]int),
		summaryCache: make(map[string]string),
		scopes:       make(map[string]*session.SessionScope),
	}
}

// msgMetadata captures ToolCalls / ToolCallID for JSON
// serialization into the messages.metadata column.
type msgMetadata struct {
	ToolCalls  []providers.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
}

// PIKA-V3: AddFullMessage persists a message to bot_memory.db.
// turn_id: user → increment, others → same turn.
// tokens: estimated via tokenizer.EstimateMessageTokens.
// metadata: serialized from msg.ToolCalls / msg.ToolCallID.
func (s *PikaSessionStore) AddFullMessage(
	key string, msg providers.Message,
) {
	ctx := context.Background()

	// 1. Resolve turn_id under lock.
	turnID := s.resolveTurnID(ctx, key, msg.Role)

	// 2. Build metadata JSON.
	var meta json.RawMessage
	if len(msg.ToolCalls) > 0 || msg.ToolCallID != "" {
		md := msgMetadata{
			ToolCalls:  msg.ToolCalls,
			ToolCallID: msg.ToolCallID,
		}
		if raw, err := json.Marshal(md); err == nil {
			meta = raw
		}
	}

	// 3. Estimate tokens.
	tokens := tokenizer.EstimateMessageTokens(msg)

	// 4. Persist via BotMemory.
	row := MessageRow{
		SessionID: key,
		TurnID:    turnID,
		Role:      msg.Role,
		Content:   msg.Content,
		Tokens:    tokens,
		Metadata:  meta,
	}
	if _, err := s.mem.SaveMessage(ctx, row); err != nil {
		logger.WarnCF("pika",
			"AddFullMessage: save failed",
			map[string]any{
				"session": key,
				"role":    msg.Role,
				"error":   err.Error(),
			})
	}
}

// resolveTurnID returns the current turn_id for the session,
// incrementing if the new message role is "user".
func (s *PikaSessionStore) resolveTurnID(
	ctx context.Context, key, role string,
) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	tid, ok := s.turnIDs[key]
	if !ok {
		// Recovery: read max turn_id from DB.
		maxTID, err := s.mem.GetMaxTurnID(ctx, key)
		if err != nil {
			logger.WarnCF("pika",
				"resolveTurnID: GetMaxTurnID failed",
				map[string]any{
					"session": key,
					"error":   err.Error(),
				})
		}
		tid = maxTID
	}

	if role == "user" {
		tid++
	}
	s.turnIDs[key] = tid
	return tid
}

// AddMessage appends a simple role/content message.
func (s *PikaSessionStore) AddMessage(
	key, role, content string,
) {
	s.AddFullMessage(key, providers.Message{
		Role:    role,
		Content: content,
	})
}

// PIKA-V3: GetHistory returns all messages for session from
// bot_memory.db. Deserializes metadata back into
// providers.Message fields (ToolCalls, ToolCallID).
func (s *PikaSessionStore) GetHistory(
	key string,
) []providers.Message {
	ctx := context.Background()
	rows, err := s.mem.GetMessages(ctx, key)
	if err != nil {
		logger.WarnCF("pika",
			"GetHistory: GetMessages failed",
			map[string]any{
				"session": key,
				"error":   err.Error(),
			})
		return nil
	}

	msgs := make([]providers.Message, 0, len(rows))
	for _, row := range rows {
		msg := providers.Message{
			Role:    row.Role,
			Content: row.Content,
		}
		if len(row.Metadata) > 0 {
			var md msgMetadata
			if err := json.Unmarshal(
				row.Metadata, &md,
			); err == nil {
				msg.ToolCalls = md.ToolCalls
				msg.ToolCallID = md.ToolCallID
			}
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

// GetSummary returns cached summary (in-memory, transitional).
// Legacy ContextManager calls this. Returns "" if no cache →
// triggers re-summarization.
func (s *PikaSessionStore) GetSummary(
	key string,
) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryCache[key]
}

// SetSummary stores summary in memory (transitional).
func (s *PikaSessionStore) SetSummary(
	key, summary string,
) {
	s.mu.Lock()
	s.summaryCache[key] = summary
	s.mu.Unlock()
}

// PIKA-V3: SetHistory replaces all messages for session.
// Deletes existing, resets turn counter, re-inserts.
func (s *PikaSessionStore) SetHistory(
	key string, history []providers.Message,
) {
	ctx := context.Background()
	if err := s.mem.DeleteAllMessages(
		ctx, key,
	); err != nil {
		logger.WarnCF("pika",
			"SetHistory: delete failed",
			map[string]any{
				"session": key,
				"error":   err.Error(),
			})
	}
	s.mu.Lock()
	s.turnIDs[key] = 0
	s.mu.Unlock()
	for _, msg := range history {
		s.AddFullMessage(key, msg)
	}
}

// TruncateHistory — no-op. Session rotation, not truncation.
func (s *PikaSessionStore) TruncateHistory(
	_ string, _ int,
) {
}

// Save — no-op. SQLite WAL = immediate persistence.
func (s *PikaSessionStore) Save(_ string) error {
	return nil
}

// ListSessions returns all session keys from bot_memory.db.
func (s *PikaSessionStore) ListSessions() []string {
	ctx := context.Background()
	ids, err := s.mem.GetDistinctSessionIDs(ctx)
	if err != nil {
		logger.WarnCF("pika",
			"ListSessions failed",
			map[string]any{"error": err.Error()})
		return nil
	}
	return ids
}

// Close — no-op. BotMemory manages db lifecycle.
func (s *PikaSessionStore) Close() error {
	return nil
}

// PIKA-V3: fix3 — GetSessionScope returns the stored session
// scope for the given key, or nil if not found. Satisfies the
// MetadataAwareSessionStore interface.
func (s *PikaSessionStore) GetSessionScope(
	sessionKey string,
) *session.SessionScope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scopes[sessionKey]
}

// PIKA-V3: fix3 — EnsureSessionMetadata stores session scope
// metadata and associated aliases for the given session key.
// Satisfies the MetadataAwareSessionStore interface.
func (s *PikaSessionStore) EnsureSessionMetadata(
	sessionKey string,
	scope *session.SessionScope,
	aliases []string,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes[sessionKey] = scope
}
