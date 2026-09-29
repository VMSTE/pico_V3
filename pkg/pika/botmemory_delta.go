package pika

import (
	"context"
	"log"
	"strings"
)

// Волна 124: дельта-вход Архивариуса (модель founder'а, 29 сен).
// Go отдаёт сырьём сообщения чата после watermark последней сборки брифа;
// ротация чат не меняет — хвост умершей сессии доезжает без поиска.

// GetMaxMessageID returns the largest message id for the chat (0 if none).
func (bm *BotMemory) GetMaxMessageID(ctx context.Context, chatID string) int64 {
	var id int64
	err := bm.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(id),0) FROM messages WHERE chat_id=?`,
		chatID,
	).Scan(&id)
	if err != nil {
		log.Printf("pika/botmemory: max msg id: %v", err)
		return 0
	}
	return id
}

// GetWorkSince returns messages with id > afterID for the chat as
// "[role] content" lines. Over the token cap (~4 chars/token) the oldest
// lines drop first — для handoff свежий хвост важнее головы.
func (bm *BotMemory) GetWorkSince(
	ctx context.Context, chatID string, afterID int64, maxTokens int,
) string {
	if maxTokens <= 0 {
		maxTokens = 4000
	}
	rows, err := bm.db.QueryContext(ctx,
		`SELECT role, COALESCE(content,'') FROM messages
		 WHERE chat_id=? AND id>? AND role != 'tool' ORDER BY id`,
		chatID, afterID,
	)
	if err != nil {
		log.Printf("pika/botmemory: work since: %v", err)
		return ""
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var role, content string
		if scanErr := rows.Scan(&role, &content); scanErr != nil {
			continue
		}
		lines = append(lines, "["+role+"] "+content)
	}
	if err := rows.Err(); err != nil {
		log.Printf("pika/botmemory: work since iter: %v", err)
	}
	maxChars := maxTokens * 4
	total := 0
	for _, l := range lines {
		total += len(l) + 1
	}
	for total > maxChars && len(lines) > 1 {
		total -= len(lines[0]) + 1
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}
