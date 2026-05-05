// PIKA-V3: session_store_accessor.go — Accessor methods for
// wiring dependencies between pika components.
// GetBotMemory: used by context_pika.go to wire Archivist.
// GetLastReasoningText: used by PikaContextManager for
// ACTIVE_PLAN extraction from reasoning_log.

package pika

import (
	"context"
	"database/sql"
	"fmt"
)

// GetBotMemory returns the underlying BotMemory instance.
// Used by context_pika.go to wire the real Archivist and
// pass BotMemory to PikaContextManager.
func (s *PikaSessionStore) GetBotMemory() *BotMemory {
	return s.mem
}

// GetLastReasoningText queries reasoning_log for the last
// non-empty reasoning_text in the given session.
// Returns "" if none found or on error.
// Used by PikaContextManager for ACTIVE_PLAN extraction.
func (bm *BotMemory) GetLastReasoningText(
	ctx context.Context,
	sessionID string,
) (string, error) {
	var text sql.NullString
	err := bm.db.QueryRowContext(ctx,
		`SELECT reasoning_text FROM reasoning_log
		 WHERE session_id = ?
		   AND reasoning_text IS NOT NULL
		   AND reasoning_text != ''
		 ORDER BY id DESC LIMIT 1`,
		sessionID,
	).Scan(&text)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf(
			"pika/botmemory: last reasoning: %w", err,
		)
	}
	if !text.Valid {
		return "", nil
	}
	return text.String, nil
}
