package session

// MetadataAwareSessionStore exposes structured session metadata operations.
// Implementations are optional — callers should use type assertions.
// PIKA-V3: extracted from jsonl_backend.go before its removal.
type MetadataAwareSessionStore interface {
	EnsureSessionMetadata(
		sessionKey string,
		scope *SessionScope,
		aliases []string,
	)
	ResolveSessionKey(sessionKey string) string
	GetSessionScope(sessionKey string) *SessionScope
}
