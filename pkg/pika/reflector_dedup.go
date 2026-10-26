// PIKA-V3: Reflector dedup gate — защита от дублей pattern/runbook_draft.
// Проблема (бой, кластеры P-463/470/478/483/563 и R-87/91/95/145/146):
// applyPattern/applyRunbook делали слепой INSERT, investigate-прогон
// по каждому fail-событию регенерировал один и тот же паттерн.
// Решение: перед INSERT ищем похожий атом той же категории; нашли —
// подтверждаем существующий (D-59: +0.1), а не создаём дубль.

package pika

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
	"unicode"
)

// existingTypedAtomsLimit — потолок существующих pattern/runbook_draft,
// подмешиваемых во вход LLM (дедуп-контекст).
const existingTypedAtomsLimit = 200

// dedupSimilarityThreshold — порог Jaccard по словам summary.
// Консервативно: ниже порога атом считается НОВЫМ (лучше лишний атом,
// чем подавленный паттерн). Калибровка на реальной паре дублей:
// P-463 vs P-478 → 0.65; разные WebSocket-темы → <0.3.
const dedupSimilarityThreshold = 0.6

// fetchExistingTypedAtoms возвращает существующие pattern/runbook_draft
// атомы независимо от scope режима. Нужны во входе LLM, чтобы задачи
// PATTERNS/RUNBOOK_DRAFTS видели уже созданное и не плодили дубли.
func (r *ReflectorPipeline) fetchExistingTypedAtoms(
	ctx context.Context, limit int,
) ([]KnowledgeAtomRow, error) {
	rows, err := r.mem.db.QueryContext(ctx,
		`SELECT id, atom_id, chat_id, pika_session_id,
			source_event_id, source_message_id, category,
			summary, detail, confidence, polarity, verified,
			tags, source_turns, history,
			created_at, updated_at
		FROM knowledge_atoms
		WHERE category IN ('pattern','runbook_draft')
		ORDER BY updated_at DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query typed atoms: %w", err)
	}
	defer rows.Close()
	return scanKnowledgeAtomRows(rows)
}

// summaryWords — нормализация summary в множество слов:
// lowercase, пунктуация и кавычки удалены, split по пробелам.
func summaryWords(s string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(s),
		func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
	out := make(map[string]bool, len(words))
	for _, w := range words {
		if w != "" {
			out[w] = true
		}
	}
	return out
}

// summarySimilarity — Jaccard по множествам слов двух summary.
func summarySimilarity(a, b string) float64 {
	wa, wb := summaryWords(a), summaryWords(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	union := len(wa) + len(wb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// findDuplicateAtom ищет существующий атом той же категории и polarity
// с похожим summary. FTS сужает кандидатов, Jaccard принимает решение.
// Возвращает (nil, nil), если дубля нет или запрос непригоден для FTS.
func (r *ReflectorPipeline) findDuplicateAtom(
	ctx context.Context,
	category, polarity, summary string,
) (*KnowledgeAtomRow, error) {
	fq := buildFTSQuery(escapeFTSSpecial(summary))
	if fq == "" {
		return nil, nil
	}
	cands, err := r.mem.QueryKnowledgeFTS(ctx, fq, 10)
	if err != nil {
		return nil, fmt.Errorf("dedup fts: %w", err)
	}
	for i := range cands {
		c := &cands[i]
		if c.Category != category || c.Polarity != polarity {
			continue
		}
		if summarySimilarity(c.Summary, summary) >=
			dedupSimilarityThreshold {
			return c, nil
		}
	}
	return nil, nil
}

// confirmExistingAtom — штатный D-59 путь вместо создания дубля:
// подтверждение повторным событием = confidence +0.1 (clamp 1.0),
// запись в history с evidence-атомами.
func (r *ReflectorPipeline) confirmExistingAtom(
	ctx context.Context,
	existing *KnowledgeAtomRow,
	evidence []string,
) error {
	newConf := math.Min(1.0, existing.Confidence+0.1)
	now := time.Now().UTC().Format(time.RFC3339)
	histEntry, _ := json.Marshal(map[string]any{
		"v":          existing.Confidence,
		"confidence": newConf,
		"by":         "reflector_dedup",
		"at":         now,
		"evidence":   evidence,
	})
	log.Printf(
		"[reflector] dedup gate: %s confirmed (%.2f -> %.2f), "+
			"duplicate skipped",
		existing.AtomID, existing.Confidence, newConf,
	)
	return r.mem.UpdateAtomConfidence(
		ctx, existing.AtomID, newConf, histEntry,
	)
}

// provenanceFromEvidence возвращает (source_event_id, source_message_id)
// первого evidence-атома с заполненным provenance. Раньше рефлекторные
// атомы до первоисточника не дотягивались вообще.
func provenanceFromEvidence(
	evidenceIDs []string,
	atomMap map[string]*KnowledgeAtomRow,
) (eventID, messageID *int64) {
	for _, id := range evidenceIDs {
		a := atomMap[id]
		if a == nil {
			continue
		}
		if a.SourceMessageID != nil || a.SourceEventID != nil {
			return a.SourceEventID, a.SourceMessageID
		}
	}
	return nil, nil
}
