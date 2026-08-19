// PIKA-V3: session_metadata.go — MetadataAwareSessionStore for PikaSessionStore (D-AUDIT-109).
// The agent loop already calls ensureSessionMetadata on every allocation, but
// the call was a silent no-op: PikaSessionStore did not implement the
// interface. Now it persists the canonical-key -> raw-chat-id mapping into
// the registry (kind=snapshot, no new tables — founder's rule, D-AUDIT-104).

package pika

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/sipeed/picoclaw/pkg/session"
)

// compile-time interface check
var _ session.MetadataAwareSessionStore = (*PikaSessionStore)(nil)

// Registry key prefixes for chat history metadata (all kind="snapshot").
const (
	chatScopeKeyPrefix  = "chat_scope:"
	chatTitleKeyPrefix  = "chat_title:"
	chatHiddenKeyPrefix = "chat_hidden:"
)

func chatScopeKey(sessionKey string) string { return chatScopeKeyPrefix + sessionKey }
func chatTitleKey(chatID string) string     { return chatTitleKeyPrefix + chatID }
func chatHiddenKey(chatID string) string    { return chatHiddenKeyPrefix + chatID }

// EnsureSessionMetadata persists the session scope into the registry.
// Summary = raw chat id (e.g. "pico:<uuid>"), Data = full scope JSON.
func (s *PikaSessionStore) EnsureSessionMetadata(
	sessionKey string,
	scope *session.SessionScope,
	_ []string,
) {
	if sessionKey == "" || scope == nil {
		return
	}
	data, err := json.Marshal(scope)
	if err != nil {
		log.Printf(
			"pika/session_store: marshal scope %q: %v", sessionKey, err,
		)
		return
	}
	_, err = s.mem.UpsertRegistry(context.Background(), RegistryRow{
		Kind:    "snapshot",
		Key:     chatScopeKey(sessionKey),
		Summary: rawChatIDFromScope(scope),
		Data:    json.RawMessage(data),
	})
	if err != nil {
		log.Printf(
			"pika/session_store: ensure metadata %q: %v", sessionKey, err,
		)
	}
}

// ResolveSessionKey is identity: no alias resolution in the SQLite store.
func (s *PikaSessionStore) ResolveSessionKey(sessionKey string) string {
	return sessionKey
}

// GetSessionScope loads the persisted scope for a session key, or nil.
func (s *PikaSessionStore) GetSessionScope(
	sessionKey string,
) *session.SessionScope {
	if sessionKey == "" {
		return nil
	}
	r, err := s.mem.GetRegistry(
		context.Background(), "snapshot", chatScopeKey(sessionKey),
	)
	if err != nil || r == nil || len(r.Data) == 0 {
		return nil
	}
	var scope session.SessionScope
	if err := json.Unmarshal(r.Data, &scope); err != nil {
		return nil
	}
	return &scope
}

// rawChatIDFromScope extracts the raw chat id (e.g. "pico:<uuid>") from
// scope.Values["chat"], which has the form "<chatType>:<chatID>".
func rawChatIDFromScope(scope *session.SessionScope) string {
	v := strings.TrimSpace(scope.Values["chat"])
	if v == "" {
		return ""
	}
	if idx := strings.Index(v, ":"); idx >= 0 {
		return v[idx+1:]
	}
	return v
}
