// PIKA-V3: metadata.go — MetadataAwareSessionStore interface
// for session scope retrieval by upstream steering.go.
package session

// MetadataAwareSessionStore extends SessionStore with session
// scope metadata access. Used by pkg/agent/steering.go to
// retrieve SessionScope for the Continue flow.
//
// Implementations that do not track scope metadata can return
// nil from GetSessionScope — callers handle nil gracefully.
type MetadataAwareSessionStore interface {
	SessionStore
	// GetSessionScope returns the scope for a session key,
	// or nil if no scope metadata is tracked.
	GetSessionScope(sessionKey string) *SessionScope
}
