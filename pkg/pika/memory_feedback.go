package pika

// PIKA-V3 (D-AUDIT-125): feedback-слой и KWIC-окно для search_memory.
// 0 LLM — чистый SQL по существующим таблицам.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// searchFeedbackMarks — слой «разносив»: сообщения пользователя, помеченные
// ClassifyFeedback (wrong/correction/rephrase, pipeline_setup.go), плюс предмет
// разноса — предыдущий ответ ассистента в том же чате (монотонный id).
func (ms *MemorySearch) searchFeedbackMarks(
	ctx context.Context,
	limit int,
	sessionID string,
) ([]rawResult, error) {
	q := `SELECT id, chat_id, content, ts,
		COALESCE(json_extract(metadata,'$.feedback_signal'),'')
		FROM messages
		WHERE role='user'
		AND json_extract(COALESCE(metadata,'{}'),'$.feedback_signal')
			IN ('wrong','correction','rephrase')`
	var args []any
	if sessionID != "" &&
		ms.bm.GetMemoryScope(ctx, sessionID) == "session" {
		q += ` AND chat_id = ?`
		args = append(args, sessionID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := ms.bm.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("pika/memory_feedback: marks: %w", err)
	}
	defer rows.Close()

	var out []rawResult
	for rows.Next() {
		var id int64
		var chatID, ts, signal string
		var content sql.NullString
		if scanErr := rows.Scan(
			&id, &chatID, &content, &ts, &signal,
		); scanErr != nil {
			return nil, fmt.Errorf(
				"pika/memory_feedback: scan: %w", scanErr,
			)
		}
		subject := ms.precedingAssistant(ctx, chatID, id)
		out = append(out, rawResult{
			Type: "feedback",
			Summary: fmt.Sprintf(
				"[feedback:%s] user: %s | предмет разноса (ответ перед этим): %s",
				signal,
				truncateStr(content.String, 150),
				truncateStr(subject, 150),
			),
			Source:    "messages",
			CreatedAt: parseSQLiteTime(ts),
			IsFTS:     false,
			DedupKey:  fmt.Sprintf("feedback:%d", id),
			LayerPrio: prioKnowledge,
			MsgID:     id,
			ChatID:    chatID,
			FullContent: fmt.Sprintf(
				"[feedback:%s] user: %s | предмет разноса: %s",
				signal, content.String, subject,
			),
		})
	}
	return out, rows.Err()
}

// precedingAssistant — последний ответ ассистента перед сообщением beforeID.
func (ms *MemorySearch) precedingAssistant(
	ctx context.Context,
	chatID string,
	beforeID int64,
) string {
	var c sql.NullString
	_ = ms.bm.db.QueryRowContext(ctx,
		`SELECT content FROM messages
		WHERE chat_id=? AND id<? AND role='assistant'
		ORDER BY id DESC LIMIT 1`,
		chatID, beforeID,
	).Scan(&c)
	return c.String
}

// expandAround — ±N соседних сообщений вокруг хита по монотонному id
// (KWIC-окно: «до/после» без новых таблиц).
func (ms *MemorySearch) expandAround(
	ctx context.Context,
	results []rawResult,
	around int,
) []rawResult {
	if around < 1 {
		return results
	}
	if around > 5 {
		around = 5
	}
	for i, r := range results {
		if r.MsgID == 0 || r.ChatID == "" {
			continue
		}
		parts := ms.neighborsAround(ctx, r.ChatID, r.MsgID, around)
		if len(parts) > 0 {
			results[i].Summary += "\n↕ контекст: " +
				strings.Join(parts, " | ")
		}
	}
	return results
}

// neighborsAround — соседи сообщения msgID в чате (±around по монотонному id).
// defer Close + проверка Err: требования sqlclosecheck/rowserrcheck.
func (ms *MemorySearch) neighborsAround(
	ctx context.Context,
	chatID string,
	msgID int64,
	around int,
) []string {
	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT id, role, substr(COALESCE(content,''),1,120)
		FROM messages
		WHERE chat_id=? AND id BETWEEN ? AND ? AND role != 'tool'
		ORDER BY id`,
		chatID, msgID-int64(around), msgID+int64(around),
	)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var parts []string
	for rows.Next() {
		var nid int64
		var role, text string
		if scanErr := rows.Scan(&nid, &role, &text); scanErr != nil {
			return parts
		}
		marker := " "
		if nid == msgID {
			marker = ">"
		}
		parts = append(
			parts, fmt.Sprintf("%s[%s] %s", marker, role, text),
		)
	}
	if err := rows.Err(); err != nil {
		return parts
	}
	return parts
}
