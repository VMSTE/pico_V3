// PIKA-V3: PikaSessionStore implements session.SessionStore via BotMemory.

package pika

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tokenizer"
)

// compile-time interface check
var _ session.SessionStore = (*PikaSessionStore)(nil)

// PikaSessionStore implements session.SessionStore using BotMemory
// as the SQLite backend. Thread-safe via mutex for in-memory state.
// All DB operations delegate to BotMemory (no direct SQL here).
type PikaSessionStore struct {
	mem          *BotMemory
	mu           sync.Mutex
	turnIDs      map[string]int
	summaryCache map[string]string
}

// NewPikaSessionStore creates a PikaSessionStore backed by BotMemory.
func NewPikaSessionStore(mem *BotMemory) *PikaSessionStore {
	return &PikaSessionStore{
		mem:          mem,
		turnIDs:      make(map[string]int),
		summaryCache: make(map[string]string),
	}
}

// messageMetadata holds providers.Message fields not stored in DB
// columns (role, content, tokens). Serialized as JSON into the
// metadata column. On read, fields are restored into Message.
type messageMetadata struct {
	Media            []string                `json:"media,omitempty"`
	Attachments      []providers.Attachment  `json:"attachments,omitempty"`
	ReasoningContent string                  `json:"reasoning_content,omitempty"`
	SystemParts      []providers.ContentBlock `json:"system_parts,omitempty"`
	ToolCalls        []providers.ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID       string                  `json:"tool_call_id,omitempty"`
}

func buildMetadata(msg providers.Message) json.RawMessage {
	meta := messageMetadata{
		Media:            msg.Media,
		Attachments:      msg.Attachments,
		ReasoningContent: msg.ReasoningContent,
		SystemParts:      msg.SystemParts,
		ToolCalls:        msg.ToolCalls,
		ToolCallID:       msg.ToolCallID,
	}
	if len(meta.Media) == 0 &&
		len(meta.Attachments) == 0 &&
		meta.ReasoningContent == "" &&
		len(meta.SystemParts) == 0 &&
		len(meta.ToolCalls) == 0 &&
		meta.ToolCallID == "" {
		return nil
	}
	data, err := json.Marshal(meta)
	if err != nil {
		log.Printf(
			"pika/session_store: marshal metadata: %v", err,
		)
		return nil
	}
	return data
}

// currentTurnID returns the current turn_id for a session,
// recovering from DB if not in the in-memory cache.
// Must be called with s.mu held.
func (s *PikaSessionStore) currentTurnID(key string) int {
	tid, ok := s.turnIDs[key]
	if ok {
		return tid
	}
	maxTID, err := s.mem.GetMaxTurnID(
		context.Background(), key,
	)
	if err != nil {
		log.Printf(
			"pika/session_store: recover turn_id %q: %v",
			key, err,
		)
		return 0
	}
	s.turnIDs[key] = maxTID
	return maxTID
}

// addFullMessageLocked is the internal implementation.
// s.mu must be held by the caller.
func (s *PikaSessionStore) addFullMessageLocked(
	key string, msg providers.Message,
) {
	tid := s.currentTurnID(key)
	if msg.Role == "user" {
		tid++
		s.turnIDs[key] = tid
	}

	meta := buildMetadata(msg)
	tokens := tokenizer.EstimateMessageTokens(msg)

	row := MessageRow{
		SessionID: key,
		TurnID:    tid,
		Role:      msg.Role,
		Content:   msg.Content,
		Tokens:    tokens,
		Metadata:  meta,
	}
	_, err := s.mem.SaveMessage(context.Background(), row)
	if err != nil {
		log.Printf(
			"pika/session_store: save msg %q: %v",
			key, err,
		)
	}
}

// AddFullMessage appends a complete message including tool calls.
func (s *PikaSessionStore) AddFullMessage(
	key string, msg providers.Message,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addFullMessageLocked(key, msg)
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

// GetHistory returns the full message history for the session.
// Returns an empty (non-nil) slice if no messages or on error.
func (s *PikaSessionStore) GetHistory(
	key string,
) []providers.Message {
	rows, err := s.mem.GetMessages(
		context.Background(), key,
	)
	if err != nil {
		log.Printf(
			"pika/session_store: get history %q: %v",
			key, err,
		)
		return []providers.Message{}
	}
	msgs := make([]providers.Message, 0, len(rows))
	for _, r := range rows {
		msg := providers.Message{
			Role:    r.Role,
			Content: r.Content,
		}
		if len(r.Metadata) > 0 {
			var meta messageMetadata
			if jErr := json.Unmarshal(
				r.Metadata, &meta,
			); jErr != nil {
				log.Printf(
					"pika/session_store: "+
						"unmarshal metadata id=%d: %v",
					r.ID, jErr,
				)
			} else {
				msg.Media = meta.Media
				msg.Attachments = meta.Attachments
				msg.ReasoningContent = meta.ReasoningContent
				msg.SystemParts = meta.SystemParts
				msg.ToolCalls = meta.ToolCalls
				msg.ToolCallID = meta.ToolCallID
			}
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

// GetSummary returns the cached conversation summary, or "".
func (s *PikaSessionStore) GetSummary(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryCache[key]
}

// SetSummary replaces the cached conversation summary.
func (s *PikaSessionStore) SetSummary(key, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.summaryCache[key] = summary
}

// SetHistory replaces the full message history for a session.
func (s *PikaSessionStore) SetHistory(
	key string, history []providers.Message,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.mem.DeleteAllMessages(
		context.Background(), key,
	)
	if err != nil {
		log.Printf(
			"pika/session_store: delete msgs %q: %v",
			key, err,
		)
		return
	}
	s.turnIDs[key] = 0
	for _, msg := range history {
		s.addFullMessageLocked(key, msg)
	}
}

// TruncateHistory is a no-op. Pika uses session rotation
// instead of message truncation for context management.
func (s *PikaSessionStore) TruncateHistory(
	_ string, _ int,
) {
	// PIKA-V3: no-op — session rotation handles overflow
}

// Save is a no-op. SQLite WAL mode = immediate durability.
func (s *PikaSessionStore) Save(_ string) error {
	// PIKA-V3: no-op — WAL mode = immediate persistence
	return nil
}

// ListSessions returns all distinct session keys from the DB.
func (s *PikaSessionStore) ListSessions() []string {
	ids, err := s.mem.GetDistinctSessionIDs(
		context.Background(),
	)
	if err != nil {
		log.Printf(
			"pika/session_store: list sessions: %v", err,
		)
		return nil
	}
	return ids
}

// Close is a no-op. BotMemory manages the DB lifecycle.
func (s *PikaSessionStore) Close() error {
	// PIKA-V3: no-op — BotMemory owns the db connection
	return nil
}
