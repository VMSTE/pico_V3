package pika

// ТЗ-122 (срез М4): QuOTE — вопросная индексация (arXiv 2502.10976).
// Запрос — вопрос, цель — ответ, сформулированный иначе: лексика и
// вектора такое не мостят (стенд 15 окт: кейсы 6/9/14/20 — recall-
// провалы, целей нет в топ-20). Мост: на индексации LLM пишет 2
// вопроса «на которые отвечает запись»; вопросы делят токены с живым
// запросом → BM25 по questions_fts находит документ. Нога отдаёт
// ДОКУМЕНТ-ИСТОЧНИК с его DedupKey → document-level RRF фузит хит
// с остальными ногами (буст, а не дубль).
// Стоимость: одноразово ~1 вызов фоновой модели на 8 документов,
// дальше инкремент. Без caller/таблицы слой молча выключен.

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/kljensen/snowball"
)

const (
	qgenBatchDocs  = 8 // документов на один вызов LLM
	qgenMaxTextLen = 1500
	qgenPerDoc     = 4
	qgenPoolLimit  = 100 // как у messages-ноги (срез 1.5)
)

const qgenSysPrompt = "Ты индексируешь память личного ассистента. " +
	"Тебе дают пронумерованные записи из истории (сообщения, заметки, " +
	"рассуждения ассистента). Для КАЖДОЙ записи напиши ровно 4 коротких " +
	"разговорных вопроса на русском, на которые она содержит ответ или факт. " +
	"Формулируй так, как пользователь спросил бы об этом неделю спустя. " +
	"ЖЁСТКИЕ ПРАВИЛА: (1) каждый вопрос содержит минимум один термин " +
	"ДОСЛОВНО из записи — имена, проекты, технологии, коды, цифры (в записи " +
	"«гитхаб» — пиши «гитхаб», не «GitHub»); лексическое совпадение критично " +
	"для поиска; (2) все 4 вопроса с РАЗНЫМ словарём — не перефразировки " +
	"друг друга. Формат строго: «Н.М вопрос?» где Н — номер записи, " +
	"М — 1..4. Никакого другого текста."

func (bm *BotMemory) ensureQuestionTables(ctx context.Context) error {
	if _, err := bm.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS question_done (
		source TEXT NOT NULL,
		ref_id INTEGER NOT NULL,
		n INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (source, ref_id)
	)`); err != nil {
		return err
	}
	_, err := bm.db.ExecContext(ctx, `CREATE VIRTUAL TABLE IF NOT EXISTS questions_fts
		USING fts5(question, source UNINDEXED, ref_id UNINDEXED)`)
	return err
}

// QuestionCoverage — сколько документов прошли генерацию (для стенда).
func (bm *BotMemory) QuestionCoverage(ctx context.Context) (int, error) {
	if err := bm.ensureQuestionTables(ctx); err != nil {
		return 0, err
	}
	var n int
	err := bm.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM question_done").Scan(&n)
	return n, err
}

// pendingQuestionDocs — антиджойн: hot-сообщения и атомы без отметки
// в question_done. Архив сознательно вне v1 (контент сжат, гидратация
// через decompress — отдельный срез при нужде).
func (bm *BotMemory) pendingQuestionDocs(
	ctx context.Context, batch int,
) ([]pendingDoc, error) {
	if err := bm.ensureQuestionTables(ctx); err != nil {
		return nil, err
	}
	var out []pendingDoc
	rows, err := bm.db.QueryContext(ctx, `SELECT m.id, m.content FROM messages m
		WHERE COALESCE(m.content, '') <> ''
		AND NOT EXISTS (SELECT 1 FROM question_done d
			WHERE d.source='hot' AND d.ref_id=m.id)
		LIMIT ?`, batch)
	if err != nil {
		return nil, fmt.Errorf("pika/questions: pending hot: %w", err)
	}
	for rows.Next() {
		var id int64
		var content string
		if err := rows.Scan(&id, &content); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, pendingDoc{source: "hot", id: id, content: content})
	}
	rows.Close()
	if len(out) < batch {
		arows, aErr := bm.db.QueryContext(ctx, `SELECT a.id,
				a.summary || ' ' || COALESCE(a.detail, '')
			FROM knowledge_atoms a
			WHERE COALESCE(a.summary, '') <> ''
			AND NOT EXISTS (SELECT 1 FROM question_done d
				WHERE d.source='atom' AND d.ref_id=a.id)
			LIMIT ?`, batch-len(out))
		if aErr != nil {
			return nil, fmt.Errorf("pika/questions: pending atoms: %w", aErr)
		}
		for arows.Next() {
			var id int64
			var content string
			if err := arows.Scan(&id, &content); err != nil {
				arows.Close()
				return nil, err
			}
			out = append(out, pendingDoc{source: "atom", id: id, content: content})
		}
		arows.Close()
	}
	// ТЗ-122 (срез М4.3): мысли модели. Лексически reasoning ищется
	// (слой prio 0.85), но вопросной индексацией не покрывался — а планы
	// и «почему решили» живут именно тут (промахи 6/10/23).
	// Внутренний цикл: пачка из пустых помечается done и НЕ обрывает
	// бэкфилл (бой 16 окт: преждевременный n=0 на сплошных скипах).
	if len(out) < batch {
		for len(out) < batch {
			rrows, rErr := bm.db.QueryContext(ctx, `SELECT r.id,
					COALESCE(r.task, '') || ' ' || COALESCE(r.reasoning_text, '')
				FROM reasoning_log r
				WHERE COALESCE(r.reasoning_text, '') <> ''
				AND NOT EXISTS (SELECT 1 FROM question_done d
					WHERE d.source='reasoning' AND d.ref_id=r.id)
				ORDER BY r.id LIMIT ?`, batch-len(out))
			if rErr != nil {
				return nil, fmt.Errorf("pika/questions: pending reasoning: %w", rErr)
			}
			var got int
			for rrows.Next() {
				var id int64
				var content string
				if err := rrows.Scan(&id, &content); err != nil {
					rrows.Close()
					return nil, err
				}
				got++
				if strings.TrimSpace(content) == "" {
					_, _ = bm.db.ExecContext(ctx, `INSERT OR REPLACE INTO question_done
						(source, ref_id, n) VALUES ('reasoning', ?, 0)`, id)
					continue
				}
				out = append(out, pendingDoc{source: "reasoning", id: id, content: content})
			}
			rrows.Close()
			if got == 0 {
				break
			}
		}
	}
	// ТЗ-122 (срез М4.1 + ремонт 16 окт): архив — тот же внутренний цикл:
	// пустые/битые блобы помечаются done, пачка не обрывает бэкфилл
	// (117/251 было обработано именно из-за преждевременного break).
	if len(out) < batch {
		for len(out) < batch {
			var archIDs []int64
			archRows, aErr := bm.db.QueryContext(ctx, `SELECT ma.id
				FROM messages_archive ma
				WHERE ma.role != 'tool'
				AND NOT EXISTS (SELECT 1 FROM question_done d
					WHERE d.source='archive' AND d.ref_id=ma.id)
				ORDER BY ma.id LIMIT ?`, batch-len(out))
			if aErr != nil {
				return nil, fmt.Errorf("pika/questions: pending archive: %w", aErr)
			}
			for archRows.Next() {
				var id int64
				if err := archRows.Scan(&id); err != nil {
					archRows.Close()
					return nil, err
				}
				archIDs = append(archIDs, id)
			}
			archRows.Close()
			if len(archIDs) == 0 {
				break
			}
			for _, id := range archIDs {
				content, _, rErr := bm.ReadArchivedMessage(ctx, id)
				if rErr != nil || strings.TrimSpace(content) == "" {
					_, _ = bm.db.ExecContext(ctx, `INSERT OR REPLACE INTO question_done
						(source, ref_id, n) VALUES ('archive', ?, 0)`, id)
					continue
				}
				out = append(out, pendingDoc{source: "archive", id: id, content: content})
			}
		}
	}
	return out, nil
}

var qgenLineRe = regexp.MustCompile(`^(\d+)\.(\d+)\s+(.+)$`)

// parseQuestionsBatch — «Н.М вопрос?» → doc → вопросы (кап qgenPerDoc).
func parseQuestionsBatch(raw string, ndocs int) map[int][]string {
	out := make(map[int][]string, ndocs)
	for _, line := range strings.Split(raw, "\n") {
		m := qgenLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		var doc int
		if _, err := fmt.Sscanf(m[1], "%d", &doc); err != nil {
			continue
		}
		if doc < 1 || doc > ndocs {
			continue
		}
		q := strings.TrimSpace(m[3])
		if !strings.Contains(q, "?") {
			continue
		}
		if len(out[doc]) < qgenPerDoc {
			out[doc] = append(out[doc], q)
		}
	}
	return out
}

// GenerateQuestionsPending — зеркало EmbedPending: пачка документов →
// один вызов LLM → вопросы в questions_fts + отметки в question_done.
// Идемпотентно; сбой LLM прерывает пачку, повтор безопасен.
func (bm *BotMemory) GenerateQuestionsPending(
	ctx context.Context, caller RankCaller, batch int,
) (int, error) {
	if caller == nil {
		return 0, nil
	}
	if err := bm.ensureQuestionTables(ctx); err != nil {
		return 0, fmt.Errorf("pika/questions: schema: %w", err)
	}
	if batch <= 0 {
		batch = qgenBatchDocs
	}
	pending, err := bm.pendingQuestionDocs(ctx, batch)
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		return 0, nil
	}
	var b strings.Builder
	b.WriteString("Записи:\n")
	for i, d := range pending {
		fmt.Fprintf(&b, "\n[%d] %s\n", i+1, truncateStr(d.content, qgenMaxTextLen))
	}
	raw, err := caller(ctx, qgenSysPrompt, b.String())
	if err != nil {
		return 0, fmt.Errorf("pika/questions: LLM: %w", err)
	}
	qs := parseQuestionsBatch(raw, len(pending))
	tx, err := bm.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("pika/questions: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	for i, d := range pending {
		n := 0
		for _, q := range qs[i+1] {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO questions_fts (question, source, ref_id)
				 VALUES (?,?,?)`, q, d.source, d.id); err != nil {
				return 0, fmt.Errorf("pika/questions: fts insert: %w", err)
			}
			n++
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO question_done (source, ref_id, n)
			 VALUES (?,?,?)`, d.source, d.id, n); err != nil {
			return 0, fmt.Errorf("pika/questions: done insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("pika/questions: commit: %w", err)
	}
	return len(pending), nil
}

// prioQuestions — вопросная нога НИЖЕ всех прямых слоёв (минимум 0.8
// у registry): recall-only. Мост по косвенному вопросу тащит документ
// в пул кандидатов, но не должен вытеснять прямое попадание — бой
// 16 окт: 56 вопрос-хитов в топах, кейсы 5/20 утонули (17→15/23).
const prioQuestions = 0.6

// questionsFTSQuery — термы ≥3 символов; RU-стемминг (snowball, паттерн
// buildFTSQuery среза 2) + префикс против сырого индекса: основа запроса
// матчит словоформы вопроса («сделал»→«сдела*» ≈ «сделано» — кейс 20).
var ftsWordRe = regexp.MustCompile(`[^\p{L}\p{N}_]+`)

func questionsFTSQuery(q string) string {
	var terms []string
	for _, w := range strings.Fields(q) {
		w = ftsWordRe.ReplaceAllString(w, "")
		if len([]rune(w)) < 3 {
			continue
		}
		if hasCyrillic(w) {
			stem, err := snowball.Stem(w, "russian", true)
			if err == nil && len([]rune(stem)) >= 3 {
				w = stem
			}
		}
		terms = append(terms, w+"*")
	}
	return strings.Join(terms, " OR ")
}

// searchQuestions — нога М4: FTS по вопросам → гидратация источника →
// rawResult с DedupKey источника (RRF-фьюжн с остальными ногами).
// Без таблицы слой молча выключен; scope=session чтится как у messages
// (D-AUDIT-104: чужие чаты не утекают).
func (ms *MemorySearch) searchQuestions(
	ctx context.Context, query string, limit int, sessionID string,
) ([]rawResult, error) {
	var name string
	if err := ms.bm.db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name='questions_fts'",
	).Scan(&name); err != nil {
		return nil, nil // таблицы нет → слой выкл
	}
	ftsQ := questionsFTSQuery(query)
	if ftsQ == "" {
		return nil, nil
	}
	rows, err := ms.bm.db.QueryContext(ctx, `
		SELECT source, CAST(ref_id AS INTEGER), bm25(questions_fts) AS score
		FROM questions_fts
		WHERE questions_fts MATCH ?
		ORDER BY score
		LIMIT ?`, ftsQ, qgenPoolLimit)
	if err != nil {
		return nil, fmt.Errorf("pika/questions: fts: %w", err)
	}
	defer rows.Close()
	type hit struct {
		source string
		refID  int64
		score  float64
	}
	seen := make(map[string]bool)
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.source, &h.refID, &h.score); err != nil {
			return nil, fmt.Errorf("pika/questions: scan: %w", err)
		}
		key := fmt.Sprintf("%s:%d", h.source, h.refID)
		if seen[key] {
			continue // один документ — один лучший хит
		}
		seen[key] = true
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	scope := ms.bm.GetMemoryScope(ctx, sessionID)
	var out []rawResult
	for _, h := range hits {
		switch h.source {
		case "hot":
			var role, content, chatID, ts string
			err := ms.bm.db.QueryRowContext(ctx,
				`SELECT COALESCE(role,''), COALESCE(content,''),
					COALESCE(chat_id,''), COALESCE(ts,'')
				 FROM messages WHERE id=?`, h.refID,
			).Scan(&role, &content, &chatID, &ts)
			if err != nil {
				continue
			}
			if scope == "session" && chatID != sessionID {
				continue
			}
			out = append(out, rawResult{
				Type:    "session",
				Source:  "questions_fts",
				Summary: fmt.Sprintf("[%s] %s", role, truncateStr(content, 200)),
				FullContent: fmt.Sprintf(
					"[%s] %s", role, content,
				),
				CreatedAt: parseSQLiteTime(ts),
				RawBM25:   h.score,
				IsFTS:     true,
				DedupKey:  fmt.Sprintf("messages:%d", h.refID),
				LayerPrio: prioQuestions,
				Role:      role,
				MsgID:     h.refID,
				ChatID:    chatID,
			})
		case "archive":
			// М4.1: гидратация через распаковку; DedupKey = archive:N —
			// фьюжн с Layer 3b (прямой FTS архива) бесплатно.
			var role, chatID, ts string
			err := ms.bm.db.QueryRowContext(ctx,
				`SELECT COALESCE(role,''), COALESCE(chat_id,''), COALESCE(ts,'')
				 FROM messages_archive WHERE id=?`, h.refID,
			).Scan(&role, &chatID, &ts)
			if err != nil {
				continue
			}
			if scope == "session" && chatID != sessionID {
				continue
			}
			content, _, dErr := ms.bm.ReadArchivedMessage(ctx, h.refID)
			if dErr != nil || strings.TrimSpace(content) == "" {
				continue
			}
			out = append(out, rawResult{
				Type:    "archive",
				Source:  "questions_fts",
				Summary: fmt.Sprintf("[%s] %s", role, truncateStr(content, 200)),
				FullContent: fmt.Sprintf(
					"[%s] %s", role, content,
				),
				CreatedAt: parseSQLiteTime(ts),
				RawBM25:   h.score,
				IsFTS:     true,
				DedupKey:  fmt.Sprintf("archive:%d", h.refID),
				LayerPrio: prioQuestions,
				Role:      role,
				MsgID:     h.refID,
				ChatID:    chatID,
			})
		case "reasoning":
			var chatID, ts, task, rtext string
			err := ms.bm.db.QueryRowContext(ctx,
				`SELECT COALESCE(chat_id,''), COALESCE(ts,''),
					COALESCE(task,''), COALESCE(reasoning_text,'')
				 FROM reasoning_log WHERE id=?`, h.refID,
			).Scan(&chatID, &ts, &task, &rtext)
			if err != nil {
				continue
			}
			if scope == "session" && chatID != sessionID {
				continue
			}
			content := strings.TrimSpace(task + "\n" + rtext)
			out = append(out, rawResult{
				Type:        "reasoning",
				Source:      "questions_fts",
				Summary:     truncateStr(content, 200),
				FullContent: content,
				CreatedAt:   parseSQLiteTime(ts),
				RawBM25:     h.score,
				IsFTS:       true,
				DedupKey:    fmt.Sprintf("reasoning:%d", h.refID),
				LayerPrio:   prioQuestions,
				ChatID:      chatID,
			})
		case "atom":
			var summary, detail string
			err := ms.bm.db.QueryRowContext(ctx,
				`SELECT COALESCE(summary,''), COALESCE(detail,'')
				 FROM knowledge_atoms WHERE id=?`, h.refID,
			).Scan(&summary, &detail)
			if err != nil {
				continue
			}
			full := summary
			if detail != "" {
				full = summary + "\n" + detail
			}
			out = append(out, rawResult{
				Type:        "knowledge",
				Source:      "questions_fts",
				Summary:     summary,
				FullContent: full,
				RawBM25:     h.score,
				IsFTS:       true,
				DedupKey:    fmt.Sprintf("knowledge:%d", h.refID),
				LayerPrio:   prioQuestions,
			})
		}
	}
	return out, nil
}
