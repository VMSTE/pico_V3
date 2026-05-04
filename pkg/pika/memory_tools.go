package pika

// PIKA-V3: memory_tools.go — Go-native search_memory tool (D-NEW-1)
// Unified memory search across all knowledge layers.
// 0 LLM tokens — pure Go + FTS5 + SQL.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// SessionIDKey is the context key for the current session ID.
// Used by MemorySearch to scope layer 1 (messages) to current session.
type SessionIDKey struct{}

// SearchMemoryArgs holds the parsed arguments for search_memory.
type SearchMemoryArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"` // default 10, clamp 1..20
}

// SearchResult represents a single result from memory search.
type SearchResult struct {
	Type      string  `json:"type"` // "session"|"knowledge"|"archive"|"event"|"reasoning"|"snapshot"
	Summary   string  `json:"summary"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"` // table name
	CreatedAt string  `json:"created_at"`
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
}

// PIKA-V3: Layer priority constants for scoring.
const (
	prioKnowledge   = 1.0
	prioEvents      = 0.9
	prioArchive     = 0.8
	prioReasoning   = 0.7
	prioRegistry    = 0.6
	prioMessages    = 0.5
	recencyMaxDays  = 30.0
	recencyMaxBoost = 0.1
	searchTimeout   = 5 * time.Second
)

// MemorySearch is a stateless singleton implementing toolshared.Tool.
// Registered via toolRouter.RegisterBrain(ms).
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
		"Model sends query \u2014 Go searches everywhere."
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

	sessionID, _ := ctx.Value(SessionIDKey{}).(string)

	results := ms.fanOut(
		ctx, parsed.Query, parsed.Limit, sessionID,
	)
	results = dedupResults(results)
	scored := scoreResults(results)

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	if len(scored) > parsed.Limit {
		scored = scored[:parsed.Limit]
	}

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
	return parsed, nil
}

func (ms *MemorySearch) fanOut(
	ctx context.Context,
	query string,
	limit int,
	sessionID string,
) []rawResult {
	var mu sync.Mutex
	var all []rawResult

	g, gCtx := errgroup.WithContext(ctx)

	// Layer 1: messages (current session, SQL LIKE)
	g.Go(func() error {
		res, err := ms.searchMessages(
			gCtx, query, limit, sessionID,
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

	_ = g.Wait()
	return all
}

// Layer 1: messages — current session, SQL LIKE.
func (ms *MemorySearch) searchMessages(
	ctx context.Context,
	query string,
	limit int,
	sessionID string,
) ([]rawResult, error) {
	if sessionID == "" {
		return nil, nil
	}
	pat := "%" + query + "%"
	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT id, role, content, ts
		FROM messages
		WHERE session_id = ? AND content LIKE ?
		ORDER BY id DESC LIMIT ?`,
		sessionID, pat, limit)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/memory_tools: messages: %w", err,
		)
	}
	defer rows.Close()

	var out []rawResult
	for rows.Next() {
		var id int64
		var role string
		var content sql.NullString
		var ts string
		if scanErr := rows.Scan(
			&id, &role, &content, &ts,
		); scanErr != nil {
			return nil, fmt.Errorf(
				"pika/memory_tools: messages scan: %w", scanErr,
			)
		}
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
			IsFTS:     false,
			DedupKey:  fmt.Sprintf("messages:%d", id),
			LayerPrio: prioMessages,
		})
	}
	return out, rows.Err()
}

// Layer 2: knowledge — FTS5 MATCH.
func (ms *MemorySearch) searchKnowledge(
	ctx context.Context,
	query string,
	limit int,
) ([]rawResult, error) {
	fq := buildFTSQuery(query)
	rows, err := ms.bm.db.QueryContext(ctx,
		`SELECT ka.id, ka.atom_id, ka.category,
		ka.summary, ka.confidence, ka.created_at,
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
		var atomID, cat, summary, ca string
		var conf, bm25Score float64
		scanErr := rows.Scan(
			&id, &atomID, &cat, &summary,
			&conf, &ca, &bm25Score,
		)
		if scanErr != nil {
			return nil, fmt.Errorf(
				"pika/memory_tools: knowledge scan: %w",
				scanErr,
			)
		}
		out = append(out, rawResult{
			Type:      "knowledge",
			Summary:   fmt.Sprintf("[%s] %s", cat, summary),
			Source:    "knowledge_atoms",
			CreatedAt: parseSQLiteTime(ca),
			RawBM25:   bm25Score,
			IsFTS:     true,
			DedupKey:  fmt.Sprintf("knowledge:%d", id),
			LayerPrio: prioKnowledge,
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
			Type:      "archive",
			Summary:   snippet,
			Source:    "messages_archive",
			CreatedAt: parseSQLiteTime(h.createdAt),
			RawBM25:   h.bm25Score,
			IsFTS:     true,
			DedupKey:  fmt.Sprintf("archive:%d", h.msgID),
			LayerPrio: prioArchive,
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

	// Hot reasoning_log
	hotRows, hotErr := ms.bm.db.QueryContext(ctx,
		`SELECT id, task, mode, ts
		FROM reasoning_log
		WHERE EXISTS (
			SELECT 1 FROM json_each(reasoning_keywords)
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
			SELECT 1 FROM json_each(reasoning_keywords)
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
func scoreResults(results []rawResult) []SearchResult {
	if len(results) == 0 {
		return []SearchResult{}
	}

	// Collect BM25 range for normalization
	var minBM, maxBM float64
	hasFTS := false
	for _, r := range results {
		if !r.IsFTS {
			continue
		}
		if !hasFTS {
			minBM = r.RawBM25
			maxBM = r.RawBM25
			hasFTS = true
		} else {
			if r.RawBM25 < minBM {
				minBM = r.RawBM25
			}
			if r.RawBM25 > maxBM {
				maxBM = r.RawBM25
			}
		}
	}

	now := time.Now()
	out := make([]SearchResult, 0, len(results))
	for _, r := range results {
		// Normalized BM25: 0..1 (1 = best match)
		// bm25() returns negative; more negative = better
		var norm float64
		if r.IsFTS && maxBM != minBM {
			norm = (maxBM - r.RawBM25) / (maxBM - minBM)
		} else {
			norm = 1.0 // non-FTS or single FTS result
		}

		// Recency: linear decay 30d, clamp 0..0.1
		days := now.Sub(r.CreatedAt).Hours() / 24.0
		recency := 0.0
		if days >= 0 && days < recencyMaxDays {
			recency = recencyMaxBoost *
				(1.0 - days/recencyMaxDays)
		}

		s := norm*r.LayerPrio + recency
		s = math.Round(s*1000) / 1000

		out = append(out, SearchResult{
			Type:      r.Type,
			Summary:   r.Summary,
			Score:     s,
			Source:    r.Source,
			CreatedAt: r.CreatedAt.Format(time.RFC3339),
		})
	}
	return out
}

// buildFTSQuery converts natural language to FTS5 OR query.
// Each word is quoted for literal matching.
func buildFTSQuery(query string) string {
	words := strings.Fields(query)
	if len(words) == 0 {
		return query
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		w = strings.ReplaceAll(w, "\"", "")
		if w == "" {
			continue
		}
		quoted[i] = "\"" + w + "\""
	}
	return strings.Join(quoted, " OR ")
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
