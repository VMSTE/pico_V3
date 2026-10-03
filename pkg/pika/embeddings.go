package pika

// ТЗ-122 (этап 2, срез М1): векторный слой — фундамент.
// Embedder — OpenAI-compatible /embeddings клиент (OpenRouter, дефолт
// bge-m3). Тихий фолбэк по «Запрещено» ТЗ-122: без ключа или при мёртвом
// API слой просто не работает — поиск живёт на чистом BM25.
// Индекс производный (истина — messages/messages_archive/knowledge_atoms),
// фоновая догонка вместо записи на пути чата (append-first, reorganize
// later — индустриальный канон инкрементальных эмбеддинг-пайплайнов).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultEmbeddingModel — bge-m3 через OpenRouter (ТЗ-122, этап 2).
	DefaultEmbeddingModel = "baai/bge-m3"
	embeddingBatchSize    = 32
	embeddingTick         = 60 * time.Second
	embeddingMaxTextLen   = 2000
)

// Embedder — минимальный клиент /embeddings (OpenAI-совместимый).
type Embedder struct {
	apiBase string
	apiKey  string
	model   string
	hc      *http.Client
}

// NewEmbedder создаёт клиента; nil, если ключа нет (слой выключен).
// Пустые apiBase/model подставляют дефолты OpenRouter / bge-m3.
func NewEmbedder(apiBase, apiKey, model string) *Embedder {
	if apiKey == "" {
		return nil
	}
	if model == "" {
		model = DefaultEmbeddingModel
	}
	apiBase = strings.TrimRight(apiBase, "/")
	if apiBase == "" {
		apiBase = "https://openrouter.ai/api/v1"
	}
	return &Embedder{
		apiBase: apiBase,
		apiKey:  apiKey,
		model:   model,
		hc:      &http.Client{Timeout: 60 * time.Second},
	}
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed — один батч текстов → векторы в порядке входа. До 3 попыток
// с линейным бэкоффом; тело ошибки не обрезаем жёстко (300 символов —
// как волна 85 для провайдеров).
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("pika/embeddings: marshal: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		req, rErr := http.NewRequestWithContext(
			ctx, http.MethodPost, e.apiBase+"/embeddings", bytes.NewReader(body))
		if rErr != nil {
			return nil, fmt.Errorf("pika/embeddings: request: %w", rErr)
		}
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, dErr := e.hc.Do(req)
		if dErr != nil {
			lastErr = dErr
			continue
		}
		raw, rErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if rErr != nil {
			lastErr = rErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf(
				"pika/embeddings: %s: %s",
				resp.Status, truncateStr(string(raw), 300))
			continue
		}
		var parsed embedResponse
		if uErr := json.Unmarshal(raw, &parsed); uErr != nil {
			return nil, fmt.Errorf("pika/embeddings: decode: %w", uErr)
		}
		if len(parsed.Data) != len(texts) {
			return nil, fmt.Errorf(
				"pika/embeddings: count %d != %d", len(parsed.Data), len(texts))
		}
		out := make([][]float32, len(texts))
		for _, d := range parsed.Data {
			if d.Index < 0 || d.Index >= len(texts) {
				return nil, fmt.Errorf("pika/embeddings: index %d out of range", d.Index)
			}
			out[d.Index] = d.Embedding
		}
		return out, nil
	}
	return nil, lastErr
}

// pendingDoc — документ, ждущий эмбеддинга.
type pendingDoc struct {
	source  string
	id      int64
	content string
}

// pendingEmbeddings — антиджойн: записи без строки в embeddings_meta.
// Порядок: hot → архив → атомы. role=tool пропускается (паритет с
// FTS-слоями, волна 86). Контент архива распаковывается через
// ReadArchivedMessage.
func (bm *BotMemory) pendingEmbeddings(
	ctx context.Context, limit int,
) ([]pendingDoc, error) {
	var out []pendingDoc
	rows, err := bm.db.QueryContext(ctx, `
		SELECT m.id, m.content FROM messages m
		LEFT JOIN embeddings_meta em ON em.source='hot' AND em.ref_id=m.id
		WHERE em.id IS NULL AND m.role != 'tool'
		  AND m.content IS NOT NULL AND m.content != ''
		ORDER BY m.id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("pika/embeddings: pending hot: %w", err)
	}
	for rows.Next() {
		var id int64
		var content string
		if sErr := rows.Scan(&id, &content); sErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("pika/embeddings: scan hot: %w", sErr)
		}
		out = append(out, pendingDoc{source: "hot", id: id, content: content})
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(out) < limit {
		archRows, aErr := bm.db.QueryContext(ctx, `
			SELECT ma.id FROM messages_archive ma
			LEFT JOIN embeddings_meta em ON em.source='archive' AND em.ref_id=ma.id
			WHERE em.id IS NULL AND ma.role != 'tool'
			ORDER BY ma.id LIMIT ?`, limit-len(out))
		if aErr != nil {
			return nil, fmt.Errorf("pika/embeddings: pending archive: %w", aErr)
		}
		var archIDs []int64
		for archRows.Next() {
			var id int64
			if sErr := archRows.Scan(&id); sErr != nil {
				break
			}
			archIDs = append(archIDs, id)
		}
		_ = archRows.Close()
		for _, id := range archIDs {
			content, _, rErr := bm.ReadArchivedMessage(ctx, id)
			if rErr != nil || strings.TrimSpace(content) == "" {
				continue
			}
			out = append(out, pendingDoc{source: "archive", id: id, content: content})
		}
	}

	if len(out) < limit {
		atomRows, atErr := bm.db.QueryContext(ctx, `
			SELECT ka.id, ka.summary FROM knowledge_atoms ka
			LEFT JOIN embeddings_meta em ON em.source='atom' AND em.ref_id=ka.id
			WHERE em.id IS NULL
			ORDER BY ka.id LIMIT ?`, limit-len(out))
		if atErr != nil {
			return nil, fmt.Errorf("pika/embeddings: pending atoms: %w", atErr)
		}
		for atomRows.Next() {
			var id int64
			var summary string
			if sErr := atomRows.Scan(&id, &summary); sErr != nil {
				break
			}
			out = append(out, pendingDoc{source: "atom", id: id, content: summary})
		}
		_ = atomRows.Close()
		if err := atomRows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// EmbedPending — один проход индексера: пачка ожидающих → один вызов
// /embeddings → meta + vec в одной транзакции. nil-эмбеддер = 0, nil
// (слой выключен). Возвращает число заэмбеженных документов.
func (bm *BotMemory) EmbedPending(
	ctx context.Context, e *Embedder, batch int,
) (int, error) {
	if e == nil {
		return 0, nil
	}
	if batch <= 0 {
		batch = embeddingBatchSize
	}
	pending, err := bm.pendingEmbeddings(ctx, batch)
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		return 0, nil
	}
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

// EmbeddingCoverage — embedded/pending (метрика покрытия; М3 → /pika).
// Pending считается COUNT-запросами, без чтения контента архива.
func (bm *BotMemory) EmbeddingCoverage(
	ctx context.Context,
) (embedded int, pending int, err error) {
	if err = bm.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM embeddings_meta`).Scan(&embedded); err != nil {
		return 0, 0, fmt.Errorf("pika/embeddings: coverage embedded: %w", err)
	}
	counts := []string{
		`SELECT COUNT(*) FROM messages m
		LEFT JOIN embeddings_meta em ON em.source='hot' AND em.ref_id=m.id
		WHERE em.id IS NULL AND m.role != 'tool'
		  AND m.content IS NOT NULL AND m.content != ''`,
		`SELECT COUNT(*) FROM messages_archive ma
		LEFT JOIN embeddings_meta em ON em.source='archive' AND em.ref_id=ma.id
		WHERE em.id IS NULL AND ma.role != 'tool'`,
		`SELECT COUNT(*) FROM knowledge_atoms ka
		LEFT JOIN embeddings_meta em ON em.source='atom' AND em.ref_id=ka.id
		WHERE em.id IS NULL`,
	}
	for _, q := range counts {
		var c int
		if cErr := bm.db.QueryRowContext(ctx, q).Scan(&c); cErr != nil {
			return embedded, 0, fmt.Errorf("pika/embeddings: coverage pending: %w", cErr)
		}
		pending += c
	}
	return embedded, pending, nil
}

// StartEmbeddingIndexer — фоновая догонка: пока есть работа — без паузы,
// затем раз в минуту. Ошибки API — WARN и следующая попытка (фолбэк);
// закрытая БД (шатдаун) — тихий выход. nil-эмбеддер — не стартует.
func (bm *BotMemory) StartEmbeddingIndexer(e *Embedder) {
	if e == nil {
		return
	}
	go func() {
		for {
			n, err := bm.EmbedPending(context.Background(), e, embeddingBatchSize)
			if err != nil {
				if strings.Contains(err.Error(), "database is closed") {
					return
				}
				log.Printf("WARN pika/embeddings: indexer: %v", err)
				time.Sleep(embeddingTick)
				continue
			}
			if n > 0 {
				continue
			}
			time.Sleep(embeddingTick)
		}
	}()
}

// vecToJSON — float32-слайс в JSON-текст для vec0.
func vecToJSON(v []float32) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}
