package pika

// PIKA-V3: ТЗ-122 (этап 2, срез М2.1) — эмбеддинг мыслей (reasoning_log).
// Измерение зонда (reasoning_probe, 20 окт): reasoning-only KNN даёт
// hit@5 11/23 и поднимает в топ-20 цели 3 из 7 residual-кейсов (6, 9, 14),
// которые не берёт весь лексический конвейер. Мысль — ответо-образный
// документ (вопрос + рассуждение + вывод рядом), в отличие от messages,
// где эхо-вопросы геометрически ближе запросу, чем ответы (ноль М2).
// Индекс производный: истина — reasoning_log. Миграция v11 расширяет
// CHECK embeddings_meta под source='reasoning'.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// reasoningMinTextLen — порог содержательности мысли (как в зонде 20 окт).
const reasoningMinTextLen = 20

// pendingReasoningEmbeddings — антиджойн: мысли без строки в
// embeddings_meta. Пустые/короткие тексты возвращаются в skipped
// (помечаются skipped-empty вызывающим, чтобы не висеть в pending вечно).
func (bm *BotMemory) pendingReasoningEmbeddings(
	ctx context.Context, limit int,
) (out []pendingDoc, skipped []int64, err error) {
	rows, err := bm.db.QueryContext(ctx, `
		SELECT rl.id, rl.reasoning_text FROM reasoning_log rl
		LEFT JOIN embeddings_meta em ON em.source='reasoning' AND em.ref_id=rl.id
		WHERE em.id IS NULL AND rl.reasoning_text IS NOT NULL
		ORDER BY rl.id LIMIT ?`, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("pika/embeddings: pending reasoning: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var text sql.NullString
		if sErr := rows.Scan(&id, &text); sErr != nil {
			return nil, nil, fmt.Errorf("pika/embeddings: scan reasoning: %w", sErr)
		}
		if len(strings.TrimSpace(text.String)) <= reasoningMinTextLen {
			skipped = append(skipped, id)
			continue
		}
		out = append(out, pendingDoc{source: "reasoning", id: id, content: text.String})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return out, skipped, nil
}

// EmbedPendingReasoning — один проход индексера по мыслям: пачка
// ожидающих → один вызов /embeddings → meta + vec в одной транзакции.
// nil-эмбеддер = 0, nil (слой выключен). Паритет с EmbedPending.
func (bm *BotMemory) EmbedPendingReasoning(
	ctx context.Context, e *Embedder, batch int,
) (int, error) {
	if e == nil {
		return 0, nil
	}
	if batch <= 0 {
		batch = embeddingBatchSize
	}
	pending, skipped, err := bm.pendingReasoningEmbeddings(ctx, batch)
	if err != nil {
		return 0, err
	}
	bm.markEmbeddingsSkipped(ctx, "reasoning", skipped)
	if len(pending) == 0 {
		return 0, nil
	}
	return bm.embedDocs(ctx, e, pending)
}

// embedDocs — общий хвост проходов индексера: эмбеддинг пачки + запись
// meta/vec одной транзакцией. Возвращает число заэмбеженных документов.
func (bm *BotMemory) embedDocs(
	ctx context.Context, e *Embedder, pending []pendingDoc,
) (int, error) {
	texts := make([]string, len(pending))
	for i, d := range pending {
		texts[i] = truncateStr(d.content, embeddingMaxTextLen)
	}
	vecs, err := e.Embed(ctx, texts)
	if err != nil {
		return 0, err
	}
	tx, err := bm.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("pika/embeddings: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	for i, d := range pending {
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO embeddings_meta (source, ref_id, model, dims)
			VALUES (?,?,?,?)`,
			d.source, d.id, e.model, len(vecs[i]))
		if err != nil {
			return 0, fmt.Errorf("pika/embeddings: meta insert: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // параллельный индексер успел раньше
		}
		metaID, err := res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("pika/embeddings: meta id: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO embeddings_vec (rowid, embedding) VALUES (?,?)`,
			metaID, vecToJSON(vecs[i])); err != nil {
			return 0, fmt.Errorf("pika/embeddings: vec insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("pika/embeddings: commit: %w", err)
	}
	return len(pending), nil
}
