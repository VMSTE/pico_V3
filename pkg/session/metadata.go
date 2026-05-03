package session

// MetadataAwareSessionStore extends SessionStore with structured
// session scope metadata. Implementations can associate each session
// key with routing information (agent, channel, peer dimensions)
// so that components like steering and session allocation can resolve
// sessions to agents without relying on legacy key parsing.
type MetadataAwareSessionStore interface {
	SessionStore
	GetSessionScope(sessionKey string) *SessionScope
	// EnsureSessionMetadata stores or updates session scope metadata
	// and associated aliases for the given session key. If the session
	// already has metadata, it is overwritten with the provided scope
	// and aliases.
	EnsureSessionMetadata(sessionKey string, scope *SessionScope, aliases []string)
}
