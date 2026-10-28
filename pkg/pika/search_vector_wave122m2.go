package pika

// ТЗ-122 (этап 2, срез М2): векторная нога search_memory.
// Канон гибридного поиска (ресёрч 3 окт, gist nichm: 2471 запросов /
// 68 движков): параллельные ноги + RRF; BM25 и вектор фейлят на РАЗНЫХ
// запросах (пересечение ~30%) → гибрид супераддитивен. Векторные хиты —
// свои RRF-слои (vec_*): шкалы bm25 и distance несопоставимы, смешивать
// их в одном слое — антипаттерн общего котла. DedupKey общий с
// лексическими ногами (messages:123 / archive:123 / knowledge:123) —
// кросс-дедуп бесплатно.
// «Запрещено» ТЗ-122: нет эмбеддера / мёртвый API → нога молча пуста,
// поиск остаётся чистым BM25.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// vecPoolK — константный KNN-пул (как knowledge=40 в срезе Л):
// состав выдачи не должен дрейфовать от запрошенного limit.
const vecPoolK = 40

// vecReasoningClusterCap — потолок мыслей из одного чата за день в
// выдаче ноги. Зонд 20 окт: хабы ноги — соседние id одной сессии
// (кластер 1832–1874); больше капа — эхо, а не покрытие.
const vecReasoningClusterCap = 3

// SetEmbedder подключает векторную ногу (nil = выключена).
func (ms *MemorySearch) SetEmbedder(e *Embedder) { ms.embedder = e }

// SetVectorWeight — калибровочная ручка веса ноги (стенд М2):
// LayerPrio векторных хитов = базовый приоритет слоя × weight.
func (ms *MemorySearch) SetVectorWeight(w float64) {
	if w > 0 {
		ms.vecWeight = w
	}
}

// SetVectorMaxDist — порог L2-дистанции: хиты дальше отбрасываются.
// Проба 3 окт: цели живут на 0.77–1.00, мусор густеет ниже 0.95 —
// порог отсекает дальнее, вес решает судьбу ближнего. 0 = выкл.
func (ms *MemorySearch) SetVectorMaxDist(d float64) {
	if d >= 0 {
		ms.vecMaxDist = d
	}
}

// SetVectorSources — белый список источников ноги (пусто/nil = все, как
// в М2). Срез М2.1: стенд ставит "reasoning" — единственный источник с
// измеренным сигналом; messages/atom остаются в индексе, но не в выдаче.
func (ms *MemorySearch) SetVectorSources(sources []string) {
	if len(sources) == 0 {
		ms.vecSources = nil
		return
	}
	ms.vecSources = make(map[string]bool, len(sources))
	for _, s := range sources {
		ms.vecSources[strings.TrimSpace(s)] = true
	}
}

type vecHit struct {
	source string
	refID  int64
	dist   float64
}

// searchVector — векторная нога: embed запроса → KNN по vec0 →
// гидратация по source (hot → кластеры с gap-fill, как BM25-нога).
// Границы after/before применяются к hot/archive (как в лексике).
func (ms *MemorySearch) searchVector(
	ctx context.Context,
	query string,
	sessionID string,
	scope string,
	tb ...[2]string,
) ([]rawResult, error) {
	if ms.embedder == nil {
		return nil, nil
	}
	vec, err := ms.embedder.EmbedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("pika/memory_tools: vector embed: %w", err)
	}
	if len(vec) == 0 {
		return nil, nil
	}
	vecs := [][]float32{vec}

	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT rowid, distance FROM embeddings_vec
		WHERE embedding MATCH ? AND k = ?`,
		vecToJSON(vecs[0]), vecPoolK)
	if err != nil {
		return nil, fmt.Errorf("pika/memory_tools: vector knn: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hits []vecHit
	for rows.Next() {
		var rowid int64
		var dist float64
		if sErr := rows.Scan(&rowid, &dist); sErr != nil {
			return nil, fmt.Errorf("pika/memory_tools: vector knn scan: %w", sErr)
		}
		var source string
		var refID int64
		mErr := ms.bm.db.QueryRowContext(ctx,
			`SELECT source, ref_id FROM embeddings_meta WHERE id=?`,
			rowid).Scan(&source, &refID)
		if mErr != nil {
			continue // метка skipped-empty и чужие строки — не хиты
		}
		if ms.vecSources != nil && !ms.vecSources[source] {
			continue // М2.1: источник вне белого списка ноги
		}
		if ms.vecMaxDist > 0 && dist > ms.vecMaxDist {
			continue // дальше порога — «наименее нерелевантное», не хит
		}
		hits = append(hits, vecHit{source: source, refID: refID, dist: dist})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	w := ms.vecWeight
	// Эхо-дедуп (волна 90, перенос на векторный слой): проба 3 окт —
	// верх KNN забит семантическими почти-дублями (13 эхо выше цели
	// кейса 6). Схлопываем по нормализованному контенту ДО выдачи.
	seenContent := make(map[string]bool)
	seenCluster := make(map[string]int) // М2.1: мысли, ключ чат+день
	var msgHits []rawResult             // hot — после цикла идут в mergeMessageClusters
	var out []rawResult
	for _, h := range hits {
		switch h.source {
		case "reasoning":
			q := `SELECT chat_id, task, mode, ts, reasoning_text
				FROM reasoning_log WHERE id=?`
			args := []any{h.refID}
			if len(tb) > 0 {
				if tb[0][0] != "" {
					q += ` AND datetime(ts) >= datetime(?)`
					args = append(args, tb[0][0])
				}
				if tb[0][1] != "" {
					q += ` AND datetime(ts) <= datetime(?)`
					args = append(args, tb[0][1])
				}
			}
			var chatID, task, mode, ts string
			var text sql.NullString
			if sErr := ms.bm.db.QueryRowContext(ctx, q, args...).Scan(
				&chatID, &task, &mode, &ts, &text); sErr != nil {
				continue // в т.ч. отсечённый границами времени (ErrNoRows)
			}
			// D-AUDIT-104: нога чтит scope, как лексическая.
			if scope == "session" && chatID != sessionID {
				continue
			}
			norm := "reasoning:" + normalizeContentKey(text.String)
			if seenContent[norm] {
				continue
			}
			seenContent[norm] = true
			cluster := chatID + "|" + parseSQLiteTime(ts).Format("2006-01-02")
			if seenCluster[cluster] >= vecReasoningClusterCap {
				continue // анти-хаб: сессионный кластер исчерпан
			}
			seenCluster[cluster]++
			snippet := extractSnippet(text.String, query, 200)
			if snippet == "" {
				snippet = reasoningSummary(task, mode)
			}
			out = append(out, rawResult{
				Type:        "reasoning",
				Summary:     "[мысль] " + snippet,
				Source:      "vec_reasoning",
				CreatedAt:   parseSQLiteTime(ts),
				RawBM25:     h.dist, // distance: меньше = лучше (как bm25)
				IsFTS:       true,
				DedupKey:    fmt.Sprintf("reasoning:%d", h.refID), // кросс-дедуп с BM25-ногой
				LayerPrio:   prioReasoning * w,
				FullContent: "[мысль] " + text.String,
			})
		case "hot":
			q := `SELECT chat_id, role, content, ts FROM messages WHERE id=?`
			args := []any{h.refID}
			if len(tb) > 0 {
				if tb[0][0] != "" {
					q += ` AND datetime(ts) >= datetime(?)`
					args = append(args, tb[0][0])
				}
				if tb[0][1] != "" {
					q += ` AND datetime(ts) <= datetime(?)`
					args = append(args, tb[0][1])
				}
			}
			var chatID, role, ts string
			var content sql.NullString
			if sErr := ms.bm.db.QueryRowContext(ctx, q, args...).Scan(
				&chatID, &role, &content, &ts); sErr != nil {
				continue // в т.ч. отсечённый границами времени (ErrNoRows)
			}
			// D-AUDIT-104: нога чтит scope, как лексическая. Иначе в
			// сессионном поиске утекают чужие чаты (дефект М2, бой аудита).
			if scope == "session" && chatID != sessionID {
				continue
			}
			norm := "hot:" + normalizeContentKey(content.String)
			if seenContent[norm] {
				continue
			}
			seenContent[norm] = true
			msgHits = append(msgHits, rawResult{
				Type:      "session",
				Summary:   fmt.Sprintf("[%s] %s", role, truncateStr(content.String, 200)),
				Source:    "vec_messages",
				CreatedAt: parseSQLiteTime(ts),
				RawBM25:   h.dist, // distance: меньше = лучше (как bm25)
				IsFTS:     true,
				DedupKey:  fmt.Sprintf("messages:%d", h.refID),
				Role:      role,
				LayerPrio: prioMessages * w,
				MsgID:     h.refID,
				ChatID:    chatID,
				FullContent: fmt.Sprintf(
					"[%s] %s", role, content.String,
				),
			})
		case "archive":
			content, _, rErr := ms.bm.ReadArchivedMessage(ctx, h.refID)
			if rErr != nil {
				continue
			}
			q := `SELECT role, ts FROM messages_archive WHERE id=?`
			args := []any{h.refID}
			if len(tb) > 0 {
				if tb[0][0] != "" {
					q += ` AND datetime(ts) >= datetime(?)`
					args = append(args, tb[0][0])
				}
				if tb[0][1] != "" {
					q += ` AND datetime(ts) <= datetime(?)`
					args = append(args, tb[0][1])
				}
			}
			var role, ts string
			if sErr := ms.bm.db.QueryRowContext(ctx, q, args...).Scan(
				&role, &ts); sErr != nil {
				continue
			}
			norm := "archive:" + normalizeContentKey(content)
			if seenContent[norm] {
				continue
			}
			seenContent[norm] = true
			snippet := truncateStr(content, 200)
			out = append(out, rawResult{
				Type:        "archive",
				Summary:     fmt.Sprintf("[%s] %s", role, snippet),
				Source:      "vec_archive",
				CreatedAt:   parseSQLiteTime(ts),
				RawBM25:     h.dist,
				IsFTS:       true,
				DedupKey:    fmt.Sprintf("archive:%d", h.refID),
				Role:        role,
				MsgID:       h.refID,
				LayerPrio:   prioArchive * w,
				FullContent: fmt.Sprintf("[%s] %s", role, content),
			})
		case "atom":
			var atomID, cat, summary, pol, ca string
			var conf float64
			if sErr := ms.bm.db.QueryRowContext(ctx,
				`SELECT atom_id, category, summary, polarity, confidence,
				created_at FROM knowledge_atoms WHERE id=?`, h.refID,
			).Scan(&atomID, &cat, &summary, &pol, &conf, &ca); sErr != nil {
				continue
			}
			norm := "atom:" + normalizeContentKey(summary)
			if seenContent[norm] {
				continue
			}
			seenContent[norm] = true
			out = append(out, rawResult{
				Type:       "knowledge",
				Summary:    fmt.Sprintf("[%s] %s", cat, summary),
				Source:     "vec_knowledge",
				CreatedAt:  parseSQLiteTime(ca),
				RawBM25:    h.dist,
				IsFTS:      true,
				DedupKey:   fmt.Sprintf("knowledge:%d", h.refID),
				MsgID:      h.refID,
				AtomID:     atomID,
				Category:   cat,
				Polarity:   pol,
				Confidence: conf,
				LayerPrio:  prioKnowledge * w,
			})
		}
	}
	// Требование среза К: векторные хиты сообщений — через те же
	// кластеры с gap-fill (вопрос со словом цели живёт у края цепочки).
	msgHits = ms.mergeMessageClusters(ctx, msgHits)
	out = append(out, msgHits...)
	return out, nil
}

// VectorProbe (стенд М2): сырая KNN-выдача ноги — калибровка порога
// distance. Без RRF и кластеров; Score = distance (меньше = ближе).
// Гидратация дублирует searchVector намеренно: это отдельный
// инструмент стенда, нога не должна меняться под пробу.
func (ms *MemorySearch) VectorProbe(
	ctx context.Context, query string,
) ([]SearchResult, error) {
	if ms.embedder == nil {
		return nil, fmt.Errorf("pika/memory_tools: probe without embedder")
	}
	vec, err := ms.embedder.EmbedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("pika/memory_tools: probe embed: %w", err)
	}
	if len(vec) == 0 {
		return nil, nil
	}
	vecs := [][]float32{vec}
	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT rowid, distance FROM embeddings_vec
		WHERE embedding MATCH ? AND k = ?`,
		vecToJSON(vecs[0]), 20)
	if err != nil {
		return nil, fmt.Errorf("pika/memory_tools: probe knn: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []SearchResult
	for rows.Next() {
		var rowid int64
		var dist float64
		if sErr := rows.Scan(&rowid, &dist); sErr != nil {
			return nil, sErr
		}
		var source string
		var refID int64
		if mErr := ms.bm.db.QueryRowContext(ctx,
			`SELECT source, ref_id FROM embeddings_meta WHERE id=?`,
			rowid).Scan(&source, &refID); mErr != nil {
			continue
		}
		hit := SearchResult{Source: source, MsgID: refID, Score: dist}
		switch source {
		case "hot":
			var role, ts string
			var content sql.NullString
			if sErr := ms.bm.db.QueryRowContext(ctx,
				`SELECT role, content, ts FROM messages WHERE id=?`,
				refID).Scan(&role, &content, &ts); sErr != nil {
				continue
			}
			hit.Type = "session"
			hit.Summary = truncateStr(content.String, 200)
			hit.CreatedAt = parseSQLiteTime(ts).Format("2006-01-02")
		case "archive":
			content, _, rErr := ms.bm.ReadArchivedMessage(ctx, refID)
			if rErr != nil {
				continue
			}
			hit.Type = "archive"
			hit.Summary = truncateStr(content, 200)
		case "atom":
			var summary string
			if sErr := ms.bm.db.QueryRowContext(ctx,
				`SELECT summary FROM knowledge_atoms WHERE id=?`,
				refID).Scan(&summary); sErr != nil {
				continue
			}
			hit.Type = "knowledge"
			hit.Summary = truncateStr(summary, 200)
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}
