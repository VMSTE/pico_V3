package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// GetMessagesBySession returns messages for a given session.
// returns only messages from the specified session.
func (bm *BotMemory) GetMessagesBySession(
	ctx context.Context,
	chatID string,
	pikaSessionID string,
) ([]MessageRow, error) {
	rows, err := bm.db.QueryContext(ctx,
		`SELECT id, chat_id, pika_session_id, ts,
		        role, content, tokens, msg_index,
		        metadata
		 FROM messages
		 WHERE chat_id = ?
		   AND pika_session_id = ?
		 ORDER BY id ASC`,
		chatID, pikaSessionID)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/botmemory: get messages by session: %w",
			err,
		)
	}
	defer rows.Close()

	var out []MessageRow
	for rows.Next() {
		var m MessageRow
		var ts string
		var content, meta sql.NullString
		var mi sql.NullInt64
		if err := rows.Scan(
			&m.ID, &m.ChatID, &m.PikaSessionID,
			&ts, &m.Role, &content,
			&m.Tokens, &mi, &meta,
		); err != nil {
			return nil, fmt.Errorf(
				"pika/botmemory: scan message: %w",
				err,
			)
		}
		m.Ts = parseSQLiteTime(ts)
		m.Content = content.String
		if mi.Valid {
			v := int(mi.Int64)
			m.MsgIndex = &v
		}
		if meta.Valid && meta.String != "" {
			m.Metadata = json.RawMessage(
				meta.String,
			)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
