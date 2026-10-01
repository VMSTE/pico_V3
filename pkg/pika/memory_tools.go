package pika

// PIKA-V3: memory_tools.go — Go-native search_memory tool (D-NEW-1)
// Unified memory search across all knowledge layers.
// 0 LLM tokens — pure Go + FTS5 + SQL.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kljensen/snowball"
	"golang.org/x/sync/errgroup"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// SearchMemoryArgs holds the parsed arguments for search_memory.
type SearchMemoryArgs struct {
	Query    string `json:"query"`
	Limit    int    `json:"limit"`    // default 10, clamp 1..20
	Feedback bool   `json:"feedback"` // D-AUDIT-125: слой разносив
	Around   int    `json:"around"`   // D-AUDIT-125: ±N соседей вокруг хита
	Full     bool   `json:"full"`     // D-AUDIT-126: снять обрезку хитов
}

// SearchResult represents a single result from memory search.
type SearchResult struct {
	Type      string  `json:"type"` // "session"|"knowledge"|"archive"|"event"|"reasoning"|"snapshot"
	Summary   string  `json:"summary"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"` // table name
	CreatedAt string  `json:"created_at"`
	// Wave 122 (slice D): additive; omitempty keeps tool JSON unchanged.
	Role       string  `json:"role,omitempty"`
	Category   string  `json:"category,omitempty"`
	Polarity   string  `json:"polarity,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	AtomID     string  `json:"atom_id,omitempty"`
	MsgID      int64   `json:"msg_id,omitempty"`
}

// rawResult is an internal result before scoring.
type rawResult struct {
	Type      string
	Summary   string
	Source    string
	CreatedAt time.Time
	RawBM25   float64
	IsFTS     bool
	DedupKey  string
	LayerPrio float64
	MsgID     int64
	ChatID    string
	// Wave 122 (slice D): fields for in-process consumers
	// (Archivist convergence on this engine).
	Role       string
	Category   string
	Polarity   string
	Confidence float64
	AtomID     string
	// ТЗ-122 (срез 1.5): буст «горячего кластера» — соседи хита по id
	// тоже матчатся (ответ размазан по 2-3 сообщениям диалога).
	Boost float64
	// D-AUDIT-126: сырой текст хита (до обрезки) — отдаётся при full=true.
	FullContent string
}

// errNoUsableFTSTerms — в запросе не осталось ни одного пригодного
// терма после чистки (ТЗ-122, срез 1): FTS-слои пропускаются, а не
// падают syntax error.
var errNoUsableFTSTerms = errors.New("fts: no usable terms")

// PIKA-V3: Layer priority constants for scoring.
// ТЗ-122 (срез 3): мягкие веса слоёв вместо жёсткого потолка.
// Пользователь не обязан знать, где лежит ответ (атом/горячее/архив) —
// слой это происхождение, а не приговор. Потолок messages=0.5 структурно
// не пускал живое сообщение в топ-5 при пяти атомах (стенд 30 сен:
// макс. скор сообщения 0.82 против 0.938 у пятого атома).
const (
	prioKnowledge   = 1.0
	prioEvents      = 0.95
	prioArchive     = 0.95
	prioMessages    = 0.95
	prioReasoning   = 0.85
	prioArtifacts   = 0.85 // волна 108: паспорта артефактов
	prioRegistry    = 0.8
	recencyMaxDays  = 30.0
	recencyMaxBoost = 0.1
	searchTimeout   = 5 * time.Second
)

// MemorySearch is a stateless singleton implementing toolshared.Tool.
// Registered via toolsRegistry.Register() in instance.go — IsCore=true.
type MemorySearch struct {
	bm *BotMemory
}

// NewMemorySearch creates a new MemorySearch tool.
func NewMemorySearch(bm *BotMemory) *MemorySearch {
	return &MemorySearch{bm: bm}
}

// Name returns the tool name.
func (ms *MemorySearch) Name() string {
	return "search_memory"
}

// Description returns the tool description.
func (ms *MemorySearch) Description() string {
	return "Unified memory search across all knowledge layers. " +
		"Returns top-N results with type and relevance score. " +
		"Model sends query \u2014 Go searches everywhere. " +
		"feedback=true: dissatisfied-user messages with the criticized answer. " +
		"around=N: N neighbor messages before/after each hit. " +
		"full=true: untruncated raw content of hits (messages/archive/thoughts)."
}

// Parameters returns the JSON schema for the tool arguments.
func (ms *MemorySearch) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural language search query",
			},
			"limit": map[string]any{
				"type":        "integer",
				"default":     10,
				"description": "Max results (1-20)",
			},
			"feedback": map[string]any{
				"type":        "boolean",
				"description": "Include messages marked as negative feedback, with the criticized answer",
			},
			"around": map[string]any{
				"type":        "integer",
				"description": "N neighbor messages before/after each hit (0=off, max 5)",
			},
			"full": map[string]any{
				"type":        "boolean",
				"description": "Return untruncated raw content of hits (default: snippets)",
			},
		},
		"required": []string{"query"},
	}
}

// Execute runs the unified memory search.
func (ms *MemorySearch) Execute(
	ctx context.Context, args map[string]any,
) *toolshared.ToolResult {
	parsed, err := parseSearchArgs(args)
	if err != nil {
		return toolshared.ErrorResult(
			fmt.Sprintf(
				"pika/memory_tools: invalid args: %s", err,
			),
		)
	}

	// PIKA-V3: clamp limit 1..20
	if parsed.Limit < 1 {
		parsed.Limit = 1
	}
	if parsed.Limit > 20 {
		parsed.Limit = 20
	}

	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	// D-AUDIT-104: canonical session key из tool ctx (sk_v1_...).
	// Старый SessionIDKey никем не наполнялся (мёртвая проводка с мая,
	// писали только тесты) → scope молча был session+пусто → layer 1
	// был мёртв всегда (бой 19 авг). Ключ удалён из кодовой базы.
	sessionID := toolshared.ToolSessionKey(ctx)

	results := ms.fanOut(
		ctx, parsed.Query, parsed.Limit, parsed.Feedback, sessionID,
	)
	results = dedupResults(results)
	// D-AUDIT-126 (волна 103): full=true — снятие обрезки по требованию
	// (двухстадийный retrieval: сниппет → полный текст).
	if parsed.Full {
		for i := range results {
			if results[i].FullContent != "" {
				results[i].Summary = results[i].FullContent
			}
		}
	}
	if parsed.Around > 0 {
		results = ms.expandAround(ctx, results, parsed.Around)
	}
	scored := scoreResults(results)

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	scored = ensureLayerDiversity(scored, parsed.Limit)

	out, _ := json.Marshal(scored)
	return toolshared.SilentResult(string(out))
}

func parseSearchArgs(
	args map[string]any,
) (SearchMemoryArgs, error) {
	var parsed SearchMemoryArgs
	q, ok := args["query"]
	if !ok {
		return parsed, fmt.Errorf(
			"missing required field: query",
		)
	}
	parsed.Query, ok = q.(string)
	if !ok {
		return parsed, fmt.Errorf("query must be a string")
	}
	if parsed.Query == "" {
		return parsed, fmt.Errorf("query must not be empty")
	}

	parsed.Limit = 10
	if l, exists := args["limit"]; exists {
		switch v := l.(type) {
		case float64:
			parsed.Limit = int(v)
		case int:
			parsed.Limit = v
		case json.Number:
			n, _ := v.Int64()
			parsed.Limit = int(n)
		}
	}
	if f, ok := args["feedback"].(bool); ok {
		parsed.Feedback = f
	}
	if a, exists := args["around"]; exists {
		switch v := a.(type) {
		case float64:
			parsed.Around = int(v)
		case int:
			parsed.Around = v
		case json.Number:
			n, _ := v.Int64()
			parsed.Around = int(n)
		}
	}
	if fl, ok := args["full"].(bool); ok {
		parsed.Full = fl
	}
	return parsed, nil
}

func (ms *MemorySearch) fanOut(
	ctx context.Context,
	query string,
	limit int,
	feedback bool,
	sessionID string,
) []rawResult {
	var mu sync.Mutex
	var all []rawResult

	g, gCtx := errgroup.WithContext(ctx)

	// Layer 1: messages (per-chat scope, SQL LIKE per word)
	g.Go(func() error {
		scope := ms.bm.GetMemoryScope(gCtx, sessionID)
		res, err := ms.searchMessages(
			gCtx, query, limit, sessionID, scope,
		)
		if err != nil {
			logLayerWarn("messages", err)
			return nil
		}
		mu.Lock()
		all = append(all, res...)
		mu.Unlock()
		return nil
	})

	// Layer 2: knowledge (FTS5)
	g.Go(func() error {
		res, err := ms.searchKnowledge(gCtx, query, limit)
		if err != nil {
			logLayerWarn("knowledge", err)
			return nil
		}
		mu.Lock()
		all = append(all, res...)
		mu.Unlock()
		return nil
	})

	// Layer 3b: messages_archive — ПРЯМОЙ FTS холодного архива (срез 6)
	g.Go(func() error {
		res, err := ms.searchMessagesArchive(gCtx, query, limit)
		if err != nil {
			logLayerWarn("messages_archive", err)
			return nil
		}
		mu.Lock()
		all = append(all, res...)
		mu.Unlock()
		return nil
	})

	// Layer 3: archive (atom -> decompress -> snippet)
	g.Go(func() error {
		res, err := ms.searchArchive(gCtx, query, limit)
		if err != nil {
			logLayerWarn("archive", err)
			return nil
		}
		mu.Lock()
		all = append(all, res...)
		mu.Unlock()
		return nil
	})

	// Layer 4: events archive (FTS5)
	g.Go(func() error {
		res, err := ms.searchEventsArchive(
			gCtx, query, limit,
		)
		if err != nil {
			logLayerWarn("events_archive", err)
			return nil
		}
		mu.Lock()
		all = append(all, res...)
		mu.Unlock()
		return nil
	})

	// Layer 5: reasoning (json_each LIKE)
	g.Go(func() error {
		res, err := ms.searchReasoning(
			gCtx, query, limit,
		)
		if err != nil {
			logLayerWarn("reasoning", err)
			return nil
		}
		mu.Lock()
		all = append(all, res...)
		mu.Unlock()
		return nil
	})

	// Layer 6b: artifact passports (волна 108, D-AUDIT-131) — что писали в файлы
	g.Go(func() error {
		res, err := ms.searchArtifacts(gCtx, query, limit)
		if err != nil {
			logLayerWarn("artifacts", err)
			return nil
		}
		mu.Lock()
		all = append(all, res...)
		mu.Unlock()
		return nil
	})

	// Layer 6: registry (LIKE search)
	g.Go(func() error {
		res, err := ms.searchRegistry(
			gCtx, query, limit,
		)
		if err != nil {
			logLayerWarn("registry", err)
			return nil
		}
		mu.Lock()
		all = append(all, res...)
		mu.Unlock()
		return nil
	})

	if feedback {
		g.Go(func() error {
			res, err := ms.searchFeedbackMarks(gCtx, limit, sessionID)
			if err != nil {
				logLayerWarn("feedback", err)
				return nil
			}
			mu.Lock()
			all = append(all, res...)
			mu.Unlock()
			return nil
		})
	}

	_ = g.Wait()
	return all
}

// Layer 1: messages — per-chat scope (D-AUDIT-104).
// session = only current chat (chat_id), all = whole base.
// NB: scoped by chat_id, not pika_session_id — the latter is turn-level
// and changes on every rotation, so the old filter matched nothing.
// Multi-word queries are split and AND-ed (one LIKE never matched).
func (ms *MemorySearch) searchMessages(
	ctx context.Context,
	query string,
	limit int,
	sessionID string,
	scope string,
) ([]rawResult, error) {
	// D-AUDIT-106: FTS5 + BM25 (индустриальный стандарт) вместо
	// LIKE+счётчика слов: IDF взвешивает редкие слова выше шума.
	// Scope session — фильтр chat_id (D-AUDIT-104); all — вся база.
	fq := buildFTSQuery(query) // слова через OR, каждое в кавычках
	if fq == "" {
		return nil, nil
	}
	var args []any
	// #nosec G202 -- WHERE из статических фрагментов; значения параметризованы
	// Волна 86 (бой 20 авг): role=tool вне выдачи — иначе поиск находит
	// собственное эхо (выдача из 10 строк = копии вопроса + вложенный
	// прошлый tool-вывод).
	q := `SELECT m.id, m.chat_id, m.role, m.content, m.ts,
		bm25(messages_fts) AS score
		FROM messages_fts f
		JOIN messages m ON m.id = f.rowid
		WHERE messages_fts MATCH ? AND m.role != 'tool'`
	args = append(args, fq)
	if scope == "session" {
		if sessionID == "" {
			return nil, nil
		}
		q += ` AND m.chat_id = ?`
		args = append(args, sessionID)
	}
	// Волна 90 (бой 20 авг): over-fetch x4 + tie-break по id.
	// LIMIT отсекал в SQL ДО дедупа: топ-10 заполняли 10 копий эха
	// со скором -8.62, факт с ответом не выезжал из SQL вообще.
	// Теперь SQL тянет с запасом, дедуп ниже схлопывает дубли,
	// и слоты достаются разным сообщениям. Финальный срез до limit —
	// в Execute после скоринга, как и было.
	q += ` ORDER BY score, m.id LIMIT ?` // bm25: меньше = лучше
	// ТЗ-122 (срез 1.5): over-fetch x10 — эталон №1 сидел на ранге 46
	// при отсечке 40 и не доезжал до скоринга в принципе (стенд 30 сен).
	// Pool size is constant, not a function of limit: otherwise the
	// top composition drifts with the requested limit (wave 122 bench).
	args = append(args, 100)
	rows, err := ms.bm.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/memory_tools: messages fts: %w", err,
		)
	}
	defer rows.Close()

	// Волна 86: дедуп почти-дублей (нормализация регистра/пробелов) —
	// 8 копий «какой мой любимый цвет?» схлопываются в одну строку.
	seenContent := make(map[string]bool)
	var out []rawResult
	for rows.Next() {
		var id int64
		var chatID string
		var role string
		var content sql.NullString
		var ts string
		var bm25Score float64
		if scanErr := rows.Scan(
			&id, &chatID, &role, &content, &ts, &bm25Score,
		); scanErr != nil {
			return nil, fmt.Errorf(
				"pika/memory_tools: messages fts scan: %w", scanErr,
			)
		}
		norm := normalizeContentKey(content.String)
		if seenContent[norm] {
			continue
		}
		seenContent[norm] = true
		// truncateStr is defined in archivist.go (same package)
		summary := fmt.Sprintf(
			"[%s] %s", role,
			truncateStr(content.String, 200),
		)
		out = append(out, rawResult{
			Type:      "session",
			Summary:   summary,
			Source:    "messages",
			CreatedAt: parseSQLiteTime(ts),
			RawBM25:   bm25Score,
			IsFTS:     true,
			DedupKey:  fmt.Sprintf("messages:%d", id),
			Role:      role,
			LayerPrio: prioMessages,
			MsgID:     id,
			ChatID:    chatID,
			FullContent: fmt.Sprintf(
				"[%s] %s", role, content.String,
			),
		})
	}

	// ТЗ-122 (срез 4): кластер как единица выдачи — хиты, идущие подряд
	// по id в одном чате, сливаются в один результат (EmergenceMem:
	// скоринг сессии + sentence-window small-to-big). Дословный ответ
	// часто в НЕматчнувшемся сообщении между хитами (эталон №1: цитата
	// в 3128, в топ-10 вошли только соседи 3127/3129) — gap-fill
	// дотягивает цепочку целиком.
	out = ms.mergeMessageClusters(ctx, out)
	return out, rows.Err()
}

// Layer 2: knowledge — FTS5 MATCH.
func (ms *MemorySearch) searchKnowledge(
	ctx context.Context,
	query string,
	limit int,
) ([]rawResult, error) {
	fq := buildFTSQuery(query)
	if fq == "" {
		return nil, nil
	}
	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT ka.id, ka.atom_id, ka.category,
		ka.summary, ka.polarity, ka.confidence, ka.created_at,
		bm25(knowledge_fts) AS score
		FROM knowledge_atoms ka
		JOIN knowledge_fts kf ON ka.id = kf.rowid
		WHERE knowledge_fts MATCH ?
		ORDER BY score LIMIT ?`,
		fq, limit)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/memory_tools: knowledge: %w", err,
		)
	}
	defer rows.Close()

	var out []rawResult
	for rows.Next() {
		var id int64
		var atomID, cat, summary, pol, ca string
		var conf, bm25Score float64
		scanErr := rows.Scan(
			&id, &atomID, &cat, &summary, &pol,
			&conf, &ca, &bm25Score,
		)
		if scanErr != nil {
			return nil, fmt.Errorf(
				"pika/memory_tools: knowledge scan: %w",
				scanErr,
			)
		}
		out = append(out, rawResult{
			Type:       "knowledge",
			Summary:    fmt.Sprintf("[%s] %s", cat, summary),
			Source:     "knowledge_atoms",
			CreatedAt:  parseSQLiteTime(ca),
			RawBM25:    bm25Score,
			IsFTS:      true,
			DedupKey:   fmt.Sprintf("knowledge:%d", id),
			MsgID:      id,
			AtomID:     atomID,
			Category:   cat,
			Polarity:   pol,
			Confidence: conf,
			LayerPrio:  prioKnowledge,
		})
	}
	return out, rows.Err()
}

// Layer 3: archive — atom as index -> decompress -> snippet.
func (ms *MemorySearch) searchArchive(
	ctx context.Context,
	query string,
	limit int,
) ([]rawResult, error) {
	fq := buildFTSQuery(query)
	if fq == "" {
		return nil, nil
	}
	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT ka.id, ka.source_message_id,
		ka.summary, ka.created_at,
		bm25(knowledge_fts) AS score
		FROM knowledge_atoms ka
		JOIN knowledge_fts kf ON ka.id = kf.rowid
		WHERE knowledge_fts MATCH ?
		AND ka.source_message_id IS NOT NULL
		ORDER BY score LIMIT ?`,
		fq, limit)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/memory_tools: archive fts: %w", err,
		)
	}
	defer rows.Close()

	type archiveHit struct {
		atomID    int64
		msgID     int64
		summary   string
		createdAt string
		bm25Score float64
	}
	var hits []archiveHit
	for rows.Next() {
		var h archiveHit
		scanErr := rows.Scan(
			&h.atomID, &h.msgID,
			&h.summary, &h.createdAt, &h.bm25Score,
		)
		if scanErr != nil {
			return nil, fmt.Errorf(
				"pika/memory_tools: archive scan: %w", scanErr,
			)
		}
		hits = append(hits, h)
	}
	if rowErr := rows.Err(); rowErr != nil {
		return nil, rowErr
	}

	var out []rawResult
	for _, h := range hits {
		content, _, readErr := ms.bm.ReadArchivedMessage(
			ctx, h.msgID,
		)
		if readErr != nil {
			continue // skip unreadable archives
		}
		snippet := extractSnippet(content, query, 200)
		if snippet == "" {
			snippet = h.summary
		}
		out = append(out, rawResult{
			Type:        "archive",
			Summary:     snippet,
			Source:      "messages_archive",
			CreatedAt:   parseSQLiteTime(h.createdAt),
			RawBM25:     h.bm25Score,
			IsFTS:       true,
			DedupKey:    fmt.Sprintf("archive:%d", h.msgID),
			LayerPrio:   prioArchive,
			FullContent: content,
		})
	}
	return out, nil
}

// Layer 4: events archive — FTS5 MATCH.
func (ms *MemorySearch) searchEventsArchive(
	ctx context.Context,
	query string,
	limit int,
) ([]rawResult, error) {
	fq := buildFTSQuery(query)
	if fq == "" {
		return nil, nil
	}
	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT ea.id, ea.type, ea.outcome,
		ea.summary, ea.ts,
		bm25(events_archive_fts) AS score
		FROM events_archive ea
		JOIN events_archive_fts ef ON ea.id = ef.rowid
		WHERE events_archive_fts MATCH ?
		ORDER BY score LIMIT ?`,
		fq, limit)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/memory_tools: events archive: %w", err,
		)
	}
	defer rows.Close()

	var out []rawResult
	for rows.Next() {
		var id int64
		var typ, summary, ts string
		var outcome sql.NullString
		var bm25Score float64
		scanErr := rows.Scan(
			&id, &typ, &outcome,
			&summary, &ts, &bm25Score,
		)
		if scanErr != nil {
			return nil, fmt.Errorf(
				"pika/memory_tools: events scan: %w", scanErr,
			)
		}
		label := "[" + typ
		if outcome.Valid && outcome.String != "" {
			label += ":" + outcome.String
		}
		label += "] " + summary
		out = append(out, rawResult{
			Type:      "event",
			Summary:   label,
			Source:    "events_archive",
			CreatedAt: parseSQLiteTime(ts),
			RawBM25:   bm25Score,
			IsFTS:     true,
			DedupKey: fmt.Sprintf(
				"events_archive:%d", id,
			),
			LayerPrio: prioEvents,
		})
	}
	return out, rows.Err()
}

// Layer 5: reasoning — json_each LIKE on reasoning_keywords.
func (ms *MemorySearch) searchReasoning(
	ctx context.Context,
	query string,
	limit int,
) ([]rawResult, error) {
	pat := "%" + query + "%"
	var out []rawResult

	// D-AUDIT-125 (wave 101): дословный поиск по тексту мыслей (FTS5).
	// Ключевой слой для «о чём она думала, когда…» — keywords-ярлыки
	// такого не содержат. Сниппет — существующий extractSnippet (окно).
	fq := buildFTSQuery(query)
	var rows *sql.Rows
	var err error
	if fq == "" {
		err = errNoUsableFTSTerms
	} else {
		rows, err = ms.bm.db.QueryContext(ctx,
			`SELECT rl.id, rl.task, rl.mode, rl.ts, rl.reasoning_text,
		bm25(reasoning_fts) AS score
		FROM reasoning_log rl
		JOIN reasoning_fts rf ON rl.id = rf.rowid
		WHERE reasoning_fts MATCH ?
		ORDER BY score LIMIT ?`,
			fq, limit)
	}
	if err != nil {
		logLayerWarn("reasoning_fts", err)
	} else {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id int64
			var task, mode, text sql.NullString
			var ts string
			var bm25Score float64
			if scanErr := rows.Scan(
				&id, &task, &mode, &ts, &text, &bm25Score,
			); scanErr != nil {
				break
			}
			snippet := extractSnippet(text.String, query, 200)
			if snippet == "" {
				snippet = reasoningSummary(task.String, mode.String)
			}
			out = append(out, rawResult{
				Type:        "reasoning",
				Summary:     "[мысль] " + snippet,
				Source:      "reasoning_log",
				CreatedAt:   parseSQLiteTime(ts),
				RawBM25:     bm25Score,
				IsFTS:       true,
				DedupKey:    fmt.Sprintf("reasoning:%d", id),
				LayerPrio:   prioReasoning,
				FullContent: "[мысль] " + text.String,
			})
		}
		if err := rows.Err(); err != nil {
			logLayerWarn("reasoning_fts", err)
		}
	}

	// Hot reasoning_log
	hotRows, hotErr := ms.bm.db.QueryContext(ctx,
		`SELECT id, task, mode, ts
		FROM reasoning_log
		WHERE EXISTS (
			SELECT 1 FROM json_each(CASE WHEN json_valid(reasoning_keywords) THEN reasoning_keywords END)
			WHERE value LIKE ?
		)
		ORDER BY ts DESC LIMIT ?`,
		pat, limit)
	if hotErr != nil {
		return nil, fmt.Errorf(
			"pika/memory_tools: reasoning hot: %w", hotErr,
		)
	}
	defer hotRows.Close()

	for hotRows.Next() {
		var id int64
		var task, mode sql.NullString
		var ts string
		if scanErr := hotRows.Scan(
			&id, &task, &mode, &ts,
		); scanErr != nil {
			return nil, fmt.Errorf(
				"pika/memory_tools: reasoning scan: %w",
				scanErr,
			)
		}
		out = append(out, rawResult{
			Type: "reasoning",
			Summary: reasoningSummary(
				task.String, mode.String,
			),
			Source:    "reasoning_log",
			CreatedAt: parseSQLiteTime(ts),
			IsFTS:     false,
			DedupKey:  fmt.Sprintf("reasoning:%d", id),
			LayerPrio: prioReasoning,
		})
	}
	if rowErr := hotRows.Err(); rowErr != nil {
		return nil, rowErr
	}

	// Archive reasoning_log_archive
	archRows, archErr := ms.bm.db.QueryContext(ctx,
		`SELECT id, task, mode, ts
		FROM reasoning_log_archive
		WHERE EXISTS (
			SELECT 1 FROM json_each(CASE WHEN json_valid(reasoning_keywords) THEN reasoning_keywords END)
			WHERE value LIKE ?
		)
		ORDER BY ts DESC LIMIT ?`,
		pat, limit)
	if archErr != nil {
		return nil, fmt.Errorf(
			"pika/memory_tools: reasoning arch: %w", archErr,
		)
	}
	defer archRows.Close()

	for archRows.Next() {
		var id int64
		var task, mode sql.NullString
		var ts string
		if scanErr := archRows.Scan(
			&id, &task, &mode, &ts,
		); scanErr != nil {
			return nil, fmt.Errorf(
				"pika/memory_tools: reasoning arch scan: %w",
				scanErr,
			)
		}
		out = append(out, rawResult{
			Type: "reasoning",
			Summary: reasoningSummary(
				task.String, mode.String,
			),
			Source:    "reasoning_log_archive",
			CreatedAt: parseSQLiteTime(ts),
			IsFTS:     false,
			DedupKey: fmt.Sprintf(
				"reasoning_archive:%d", id,
			),
			LayerPrio: prioReasoning,
		})
	}
	return out, archRows.Err()
}

// Layer 6: registry — LIKE search on snapshots.
func (ms *MemorySearch) searchRegistry(
	ctx context.Context,
	query string,
	limit int,
) ([]rawResult, error) {
	pat := "%" + query + "%"
	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT id, key, summary, ts
		FROM registry
		WHERE kind = 'snapshot'
		AND (key LIKE ? OR summary LIKE ?
			OR data LIKE ? OR tags LIKE ?)
		ORDER BY last_used DESC NULLS LAST
		LIMIT ?`,
		pat, pat, pat, pat, limit)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/memory_tools: registry: %w", err,
		)
	}
	defer rows.Close()

	var out []rawResult
	for rows.Next() {
		var id int64
		var key string
		var summary sql.NullString
		var ts string
		if scanErr := rows.Scan(
			&id, &key, &summary, &ts,
		); scanErr != nil {
			return nil, fmt.Errorf(
				"pika/memory_tools: registry scan: %w", scanErr,
			)
		}
		label := key
		if summary.Valid && summary.String != "" {
			label = key + ": " + summary.String
		}
		out = append(out, rawResult{
			Type:      "snapshot",
			Summary:   label,
			Source:    "registry",
			CreatedAt: parseSQLiteTime(ts),
			IsFTS:     false,
			DedupKey:  fmt.Sprintf("registry:%d", id),
			LayerPrio: prioRegistry,
		})
	}
	return out, rows.Err()
}

// dedupResults removes duplicate results by DedupKey.
func dedupResults(results []rawResult) []rawResult {
	seen := make(map[string]bool, len(results))
	out := make([]rawResult, 0, len(results))
	for _, r := range results {
		if seen[r.DedupKey] {
			continue
		}
		seen[r.DedupKey] = true
		out = append(out, r)
	}
	return out
}

// scoreResults applies normalized_bm25 * layer_priority + recency.
// ТЗ-122 (срез 1.5): RRF (reciprocal rank fusion, Cormack 2009) —
// слияние слоёв по ПОЗИЦИЯМ, не по сырым bm25: шкалы таблиц
// несопоставимы, позиции сопоставимы. k=60 — классика без тюнинга;
// вес слоя = LayerPrio. Индустриальный дефолт hybrid search (Redis,
// Elasticsearch). Склейка min-max по общему котлу — антипаттерн:
// слой с "более отрицательной" шкалой получал систематическое
// преимущество независимо от релевантности.
const rrfK = 60.0

func scoreResults(results []rawResult) []SearchResult {
	if len(results) == 0 {
		return []SearchResult{}
	}

	// Ранг внутри своего слоя: FTS — по bm25 (меньше = лучше),
	// при равенстве и для не-FTS слоёв — по свежести.
	byLayer := map[string][]int{}
	for i, r := range results {
		byLayer[r.Source] = append(byLayer[r.Source], i)
	}
	rrf := make([]float64, len(results))
	for _, idxs := range byLayer {
		sort.Slice(idxs, func(a, b int) bool {
			ra, rb := results[idxs[a]], results[idxs[b]]
			if ra.IsFTS && rb.IsFTS && ra.RawBM25 != rb.RawBM25 {
				return ra.RawBM25 < rb.RawBM25
			}
			return ra.CreatedAt.After(rb.CreatedAt)
		})
		for rank0, i := range idxs {
			rrf[i] = results[i].LayerPrio *
				(rrfK + 1) / (rrfK + float64(rank0) + 1)
		}
	}

	now := time.Now()
	out := make([]SearchResult, 0, len(results))
	for i, r := range results {
		// Recency: linear decay 30d, clamp 0..0.1
		days := now.Sub(r.CreatedAt).Hours() / 24.0
		recency := 0.0
		if days >= 0 && days < recencyMaxDays {
			recency = recencyMaxBoost *
				(1.0 - days/recencyMaxDays)
		}

		// Кластерный буст (горячие соседи) мультипликативно усиливает
		// позиционный скор слоя.
		s := rrf[i]*(1+r.Boost) + recency
		s = math.Round(s*1000) / 1000

		out = append(out, SearchResult{
			Type:       r.Type,
			Summary:    r.Summary,
			Score:      s,
			Source:     r.Source,
			CreatedAt:  r.CreatedAt.Format(time.RFC3339),
			Role:       r.Role,
			Category:   r.Category,
			Polarity:   r.Polarity,
			Confidence: r.Confidence,
			AtomID:     r.AtomID,
			MsgID:      r.MsgID,
		})
	}
	return out
}

// buildFTSQuery converts natural language to FTS5 OR query.
// Each word is quoted for literal matching.
// ТЗ-122 (срез 1): пунктуация срезается с краёв, термы приводятся к
// нижнему регистру и дедупятся; пустые НЕ попадают в запрос — старый
// код оставлял дыры в заранее аллоцированном слайсе (continue при
// пустом слове), давая `"a" OR  OR "b"` -> syntax error -> FTS-слой
// молча возвращал пусто на любом запросе с "пустым" словом.
func buildFTSQuery(query string) string {
	words := strings.Fields(query)
	if len(words) == 0 {
		return query
	}
	seen := make(map[string]bool, len(words))
	quoted := make([]string, 0, len(words)*2)
	for _, w := range words {
		w = strings.ToLower(strings.Trim(w, "\"'`.,;:!?()[]{}<>*+^~—–-=|/\\"))
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		quoted = append(quoted, "\""+w+"\"")
		// ТЗ-122 (срез 2): RU-стемминг на стороне ЗАПРОСА (snowball) —
		// стем идёт префиксным термом stem*: "ноушена" находит "ноушен"
		// (бой 30 сен, эталон №1) без переиндексации контента.
		if !hasCyrillic(w) {
			continue
		}
		stem, err := snowball.Stem(w, "russian", true)
		if err != nil || stem == "" || stem == w ||
			len([]rune(stem)) < 3 || seen[stem] {
			continue
		}
		seen[stem] = true
		quoted = append(quoted, stem+"*")
	}
	return strings.Join(quoted, " OR ")
}

// hasCyrillic — стеммим только русские слова; прочее snowball-ru
// переваривает непредсказуемо.
func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// extractSnippet finds query in content, returns context.
func extractSnippet(
	content, query string, maxLen int,
) string {
	lower := strings.ToLower(content)
	qLower := strings.ToLower(query)
	idx := strings.Index(lower, qLower)
	if idx < 0 {
		for _, w := range strings.Fields(qLower) {
			idx = strings.Index(lower, w)
			if idx >= 0 {
				break
			}
		}
	}
	if idx < 0 {
		// truncateStr is defined in archivist.go (same package)
		return truncateStr(content, maxLen)
	}
	start := idx - maxLen/4
	if start < 0 {
		start = 0
	}
	end := start + maxLen
	if end > len(content) {
		end = len(content)
	}
	s := content[start:end]
	if start > 0 {
		s = "..." + s
	}
	if end < len(content) {
		s = s + "..."
	}
	return s
}

// NOTE: truncateStr is NOT defined here — it lives in archivist.go
// (same package pika). Reused via package-level visibility.

func reasoningSummary(task, mode string) string {
	if task == "" && mode == "" {
		return "reasoning entry"
	}
	parts := make([]string, 0, 2)
	if mode != "" {
		parts = append(parts, "mode:"+mode)
	}
	if task != "" {
		parts = append(parts, task)
	}
	return strings.Join(parts, " \u2014 ")
}

func logLayerWarn(layer string, err error) {
	log.Printf(
		"WARN pika/memory_tools: layer %s failed: %v",
		layer, err,
	)
}

// normalizeContentKey — ключ дедупа (волна 90, бой 20 авг): регистр,
// пунктуация и пробелы не делают сообщения разными («цвет?» ≡ «цвет»).
// Индустриальный паттерн: нормализация + хеш перед фильтром near-дублей.
func normalizeContentKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// searchArtifacts — паспорта артефактов (волна 108, D-AUDIT-131).
// LIKE по пути и тулу: «что я писала», «где лежит X».
func (ms *MemorySearch) searchArtifacts(
	ctx context.Context,
	query string,
	limit int,
) ([]rawResult, error) {
	pat := "%" + query + "%"
	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT id, path, tool, session, ts, updated_at
		FROM artifact_passports
		WHERE path LIKE ? OR tool LIKE ?
		ORDER BY updated_at DESC
		LIMIT ?`,
		pat, pat, limit)
	if err != nil {
		return nil, fmt.Errorf("pika/memory_tools: artifacts: %w", err)
	}
	defer rows.Close()

	var out []rawResult
	for rows.Next() {
		var id int64
		var path, tool string
		var sess, ts, ua sql.NullString
		if scanErr := rows.Scan(&id, &path, &tool, &sess, &ts, &ua); scanErr != nil {
			return nil, fmt.Errorf("pika/memory_tools: artifact scan: %w", scanErr)
		}
		created := ts.String
		if ua.String != "" {
			created = ua.String
		}
		out = append(out, rawResult{
			Type:      "artifact",
			Summary:   path + " (via " + tool + ")",
			Source:    "artifact_passports",
			CreatedAt: parseSQLiteTime(created),
			IsFTS:     false,
			DedupKey:  fmt.Sprintf("artifact:%d", id),
			LayerPrio: prioArtifacts,
		})
	}
	return out, rows.Err()
}

// mergeMessageClusters — срез 4: слияние сообщений-кластеров.
// Сортировка по (chat_id, id), цепочка = тот же чат и разрыв <= 3.
func (ms *MemorySearch) mergeMessageClusters(
	ctx context.Context,
	in []rawResult,
) []rawResult {
	if len(in) == 0 {
		return in
	}
	sorted := make([]rawResult, len(in))
	copy(sorted, in)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ChatID != sorted[j].ChatID {
			return sorted[i].ChatID < sorted[j].ChatID
		}
		return sorted[i].MsgID < sorted[j].MsgID
	})

	var out []rawResult
	cluster := []rawResult{sorted[0]}
	flush := func() {
		if len(cluster) == 1 {
			out = append(out, cluster[0])
		} else {
			out = append(out, ms.mergeCluster(ctx, cluster))
		}
		cluster = nil
	}
	for i := 1; i < len(sorted); i++ {
		prev := cluster[len(cluster)-1]
		cur := sorted[i]
		if cur.ChatID == prev.ChatID && cur.MsgID-prev.MsgID <= 3 {
			cluster = append(cluster, cur)
			continue
		}
		flush()
		cluster = []rawResult{cur}
	}
	flush()
	return out
}

// mergeCluster — один кластер: скор = лучший bm25 члена + буст за
// размер (0.2 за члена сверх первого, кап 0.6); текст — ВСЯ цепочка
// сообщений от первого до последнего id (gap-fill из базы, без
// role=tool), кап 800 символов.
func (ms *MemorySearch) mergeCluster(
	ctx context.Context,
	members []rawResult,
) rawResult {
	best := members[0]
	for _, m := range members[1:] {
		if m.RawBM25 < best.RawBM25 {
			best = m
		}
	}
	lo := members[0].MsgID
	hi := members[len(members)-1].MsgID

	type msgLine struct{ role, content string }
	var lines []msgLine
	gapRows, err := ms.bm.db.QueryContext(ctx,
		`SELECT role, content FROM messages
		WHERE chat_id = ? AND id BETWEEN ? AND ? AND role != 'tool'
		ORDER BY id LIMIT 12`,
		best.ChatID, lo, hi)
	if err == nil {
		defer gapRows.Close()
		for gapRows.Next() {
			var role string
			var content sql.NullString
			if scanErr := gapRows.Scan(&role, &content); scanErr != nil {
				break
			}
			lines = append(lines, msgLine{role, content.String})
		}
		if rowsErr := gapRows.Err(); rowsErr != nil {
			lines = nil // упадём в fallback на сами хиты
		}
	}
	if len(lines) == 0 {
		for _, m := range members {
			lines = append(lines, msgLine{"", m.Summary})
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[диалог %d-%d] ", lo, hi)
	for i, l := range lines {
		if i > 0 {
			sb.WriteString(" | ")
		}
		if l.role != "" {
			fmt.Fprintf(&sb, "%s: ", l.role)
		}
		sb.WriteString(truncateStr(l.content, 150))
	}
	summary := truncateStr(sb.String(), 800)

	// Скор кластера = СУММА bm25 членов (аддитивное доказательство,
	// как RRF суммирует по спискам; EmergenceMem: скор сессии = число
	// turn'ов в топе). Три подтверждения сильнее одного яркого.
	sumBM := 0.0
	for _, m := range members {
		sumBM += m.RawBM25
	}
	best.RawBM25 = sumBM
	boost := 0.2 * float64(len(members)-1)
	if boost > 0.6 {
		boost = 0.6
	}
	best.Summary = summary
	best.FullContent = summary
	best.Boost = boost
	return best
}

// ensureLayerDiversity — ТЗ-122 (срез 5): федеративный минимум.
// Пользователь не обязан знать, где лежит ответ (атом/горячее/архив) —
// значит ни один слой с хитами не исчезает из выдачи целиком
// (маятник боя 30 сен–1 окт: сначала топ был только атомы, после
// срезов 2-4 — только сообщения). Слой с лучшим хитом >= 60% лидера
// окна получает последний слот, вытесняя самый слабый хвост.
func ensureLayerDiversity(scored []SearchResult, limit int) []SearchResult {
	if len(scored) <= limit {
		return scored
	}
	top := scored[:limit]
	rest := scored[limit:]
	inTop := map[string]bool{}
	topScore := 0.0
	for _, r := range top {
		inTop[r.Source] = true
		if r.Score > topScore {
			topScore = r.Score
		}
	}
	for _, r := range rest {
		if inTop[r.Source] {
			continue
		}
		if topScore > 0 && r.Score < 0.6*topScore {
			continue // слишком слабый слой не впрыскиваем
		}
		top[len(top)-1] = r
		inTop[r.Source] = true
	}
	return top
}

// searchMessagesArchive — ТЗ-122 (срез 6): прямой FTS по messages_archive.
// До среза архив был достижим только через атом (дистилляция лосси);
// кейс №4 стенда (эпопея OAuth 22 сен) доказал: неатомизированный архив
// лексически недостижим. rowid из FTS -> распаковка через
// ReadArchivedMessage; DedupKey совпадает с атомным путём -> кросс-дедуп
// бесплатно.
func (ms *MemorySearch) searchMessagesArchive(
	ctx context.Context,
	query string,
	limit int,
) ([]rawResult, error) {
	fq := buildFTSQuery(query)
	if fq == "" {
		return nil, nil
	}
	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT ma.id, ma.chat_id, ma.role, ma.ts,
		bm25(messages_archive_fts) AS score
		FROM messages_archive_fts f
		JOIN messages_archive ma ON ma.id = f.rowid
		WHERE messages_archive_fts MATCH ? AND ma.role != 'tool'
		ORDER BY score LIMIT ?`,
		fq, limit)
	if err != nil {
		return nil, fmt.Errorf("pika/memory_tools: messages archive fts: %w", err)
	}
	defer rows.Close()

	type archHit struct {
		id     int64
		chatID string
		role   string
		ts     string
		score  float64
	}
	var hits []archHit
	for rows.Next() {
		var h archHit
		if scanErr := rows.Scan(&h.id, &h.chatID, &h.role, &h.ts, &h.score); scanErr != nil {
			return nil, fmt.Errorf("pika/memory_tools: msg archive scan: %w", scanErr)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []rawResult
	for _, h := range hits {
		content, _, readErr := ms.bm.ReadArchivedMessage(ctx, h.id)
		if readErr != nil {
			continue
		}
		snippet := extractSnippet(content, query, 200)
		if snippet == "" {
			snippet = truncateStr(content, 200)
		}
		out = append(out, rawResult{
			Type:        "archive",
			Summary:     fmt.Sprintf("[%s] %s", h.role, snippet),
			Source:      "messages_archive",
			CreatedAt:   parseSQLiteTime(h.ts),
			RawBM25:     h.score,
			IsFTS:       true,
			DedupKey:    fmt.Sprintf("archive:%d", h.id),
			Role:        h.role,
			MsgID:       h.id,
			LayerPrio:   prioArchive,
			FullContent: fmt.Sprintf("[%s] %s", h.role, content),
		})
	}
	return out, nil
}

// Search — programmatic entry into the engine for in-process consumers
// (wave 122, slice D: Archivist convergence). Same pipeline as Execute
// (fanOut -> dedup -> score -> sort -> diversity) without the tool's
// JSON wrapper. SSOT retrieval: engine improvements reach all consumers.
func (ms *MemorySearch) Search(
	ctx context.Context,
	query string,
	limit int,
	sessionID string,
) []SearchResult {
	if limit < 1 {
		limit = 1
	}
	if limit > 20 {
		limit = 20
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	results := ms.fanOut(ctx, query, limit, false, sessionID)
	results = dedupResults(results)
	scored := scoreResults(results)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	return ensureLayerDiversity(scored, limit)
}
