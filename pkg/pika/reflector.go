// PIKA-V3: Reflector — Go pipeline for behavioral optimization.
// Analyzes knowledge_atoms via cheap LLM (structured output,
// 0 tool calls). 3 modes: daily/weekly/monthly.
// Decisions: D-134, D-147, D-59, D-87, F9-5, F8-8, F8-18.

package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// --- Configuration ---

// ReflectorConfig holds reflector-specific parameters.
type ReflectorConfig struct {
	Enabled    bool
	PromptFile string // hot-reload prompt path (D-90)
	Model      string // LLM model name
	MaxRetries int    // LLM retry on invalid JSON
	TimeoutMs  int    // per-LLM-call timeout
	Schedule   ReflectorSchedule
}

// ReflectorSchedule holds cron schedule strings.
type ReflectorSchedule struct {
	Daily   string // e.g. "03:00"
	Weekly  string // e.g. "Sun 04:00"
	Monthly string // e.g. "1st 05:00"
}

// DefaultReflectorConfig returns defaults per ТЗ spec.
func DefaultReflectorConfig() ReflectorConfig {
	return ReflectorConfig{
		Enabled:    true,
		PromptFile: "/workspace/prompts/reflexor.md",
		Model:      "background",
		MaxRetries: 1,
		TimeoutMs:  60000,
		Schedule: ReflectorSchedule{
			Daily:   "03:00",
			Weekly:  "Sun 04:00",
			Monthly: "1st 05:00",
		},
	}
}

// --- LLM Structured Output Types ---

// reflectorLLMResponse is the unified JSON from LLM.
type reflectorLLMResponse struct {
	Merges            []reflectorMerge      `json:"merges"`
	Patterns          []reflectorPattern    `json:"patterns"`
	ConfidenceUpdates []reflectorConfUpdate `json:"confidence_updates"`
	RunbookDrafts     []reflectorRunbook    `json:"runbook_drafts"`
}

type reflectorMerge struct {
	SourceAtomIDs []string `json:"source_atom_ids"`
	Summary       string   `json:"summary"`
	Detail        string   `json:"detail,omitempty"`
	Category      string   `json:"category"`
	Polarity      string   `json:"polarity"`
	Reason        string   `json:"reason"`
}

type reflectorPattern struct {
	Type        string   `json:"type"` // "antipattern" | "recurring_failure"
	Summary     string   `json:"summary"`
	Tags        []string `json:"tags"`
	Polarity    string   `json:"polarity"`
	SourceAtoms []string `json:"source_atoms"`
}

type reflectorConfUpdate struct {
	AtomID string  `json:"atom_id"`
	Delta  float64 `json:"delta"`
	Reason string  `json:"reason"`
}

type reflectorRunbook struct {
	Tag      string   `json:"tag"`
	Steps    []string `json:"steps"`
	Trigger  string   `json:"trigger"`
	Rollback string   `json:"rollback"`
}

// --- Reflector Pipeline ---

// ReflectorPipeline orchestrates the reflector Go-pipeline.
// Stateless between calls — all state in bot_memory.db.
// Thread-safe: no mutable state.
type ReflectorPipeline struct {
	mem       *BotMemory
	atomGen   *AtomIDGenerator
	provider  providers.LLMProvider
	telemetry *Telemetry
	cfg       ReflectorConfig
}

// NewReflectorPipeline creates a new ReflectorPipeline.
func NewReflectorPipeline(
	mem *BotMemory,
	atomGen *AtomIDGenerator,
	provider providers.LLMProvider,
	telemetry *Telemetry,
	cfg ReflectorConfig,
) *ReflectorPipeline {
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 1
	}
	if cfg.Model == "" {
		cfg.Model = "background"
	}
	if cfg.TimeoutMs <= 0 {
		cfg.TimeoutMs = 60000
	}
	return &ReflectorPipeline{
		mem:       mem,
		atomGen:   atomGen,
		provider:  provider,
		telemetry: telemetry,
		cfg:       cfg,
	}
}

// reflectorMode defines the scope of a reflector run.
type reflectorMode string

const (
	ReflectorDaily   reflectorMode = "daily"
	ReflectorWeekly  reflectorMode = "weekly"
	ReflectorMonthly reflectorMode = "monthly"
)

// Run executes the full reflector pipeline for the given mode.
// Pipeline: data prep (SQL) → prompt load → LLM call →
// parse+validate → apply (INSERT/UPDATE/DELETE in 1 txn).
func (r *ReflectorPipeline) Run(
	ctx context.Context, mode reflectorMode,
) error {
	if !r.cfg.Enabled {
		return nil
	}

	// Step 1: Data prep — load knowledge_atoms for scope
	atoms, err := r.loadAtomsForScope(ctx, mode)
	if err != nil {
		r.reportFailure()
		return fmt.Errorf(
			"pika/reflector: load atoms: %w", err,
		)
	}

	// Cold start: 0 atoms → skip LLM, not an error
	if len(atoms) == 0 {
		r.reportSuccess()
		return nil
	}

	// Step 2: Load prompt file (hot-reload, D-90)
	promptText, err := r.loadPromptFile()
	if err != nil {
		r.reportFailure()
		return fmt.Errorf(
			"pika/reflector: load prompt: %w", err,
		)
	}

	// Step 3: Build user content from atoms
	userContent := r.buildUserContent(atoms, mode)

	// Step 4: LLM call with retry on invalid JSON
	output, err := r.callWithRetry(
		ctx, promptText, userContent,
	)
	if err != nil {
		r.reportFailure()
		return fmt.Errorf(
			"pika/reflector: LLM pipeline: %w", err,
		)
	}

	// Step 5: Build atom index for validation
	atomIndex := buildAtomIndex(atoms)

	// Step 6: Apply results in transaction
	if err := r.applyResults(
		ctx, output, atomIndex, atoms,
	); err != nil {
		r.reportFailure()
		return fmt.Errorf(
			"pika/reflector: apply: %w", err,
		)
	}

	// Step 7: Monthly extras
	if mode == ReflectorMonthly {
		if err := r.markStaleAtoms(ctx); err != nil {
			// Non-fatal: log but continue
			_ = err
		}
	}

	r.reportSuccess()
	return nil
}

// --- Data Prep ---

// loadAtomsForScope returns knowledge_atoms based on mode.
func (r *ReflectorPipeline) loadAtomsForScope(
	ctx context.Context, mode reflectorMode,
) ([]KnowledgeAtomRow, error) {
	var query string
	switch mode {
	case ReflectorDaily:
		query = `SELECT id, atom_id, session_id, turn_id,
			source_event_id, source_message_id,
			category, summary, detail, confidence,
			polarity, verified, tags, source_turns,
			history, created_at, updated_at
			FROM knowledge_atoms
			WHERE created_at > datetime('now', '-1 day')
			ORDER BY id ASC`
	case ReflectorWeekly:
		query = `SELECT id, atom_id, session_id, turn_id,
			source_event_id, source_message_id,
			category, summary, detail, confidence,
			polarity, verified, tags, source_turns,
			history, created_at, updated_at
			FROM knowledge_atoms
			WHERE created_at > datetime('now', '-7 days')
			ORDER BY id ASC`
	case ReflectorMonthly:
		query = `SELECT id, atom_id, session_id, turn_id,
			source_event_id, source_message_id,
			category, summary, detail, confidence,
			polarity, verified, tags, source_turns,
			history, created_at, updated_at
			FROM knowledge_atoms
			ORDER BY id ASC`
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
	return r.queryAtoms(ctx, query)
}

// queryAtoms executes a SELECT on knowledge_atoms and scans.
func (r *ReflectorPipeline) queryAtoms(
	ctx context.Context, query string,
) ([]KnowledgeAtomRow, error) {
	rows, err := r.mem.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/reflector: query atoms: %w", err,
		)
	}
	defer rows.Close()
	return scanAtomRows(rows)
}

// scanAtomRows scans sql.Rows into []KnowledgeAtomRow.
func scanAtomRows(
	rows *sql.Rows,
) ([]KnowledgeAtomRow, error) {
	var out []KnowledgeAtomRow
	for rows.Next() {
		var a KnowledgeAtomRow
		var se, sm sql.NullInt64
		var det, tg, st, hi sql.NullString
		var ca, ua string
		if err := rows.Scan(
			&a.ID, &a.AtomID, &a.SessionID, &a.TurnID,
			&se, &sm, &a.Category, &a.Summary, &det,
			&a.Confidence, &a.Polarity, &a.Verified,
			&tg, &st, &hi, &ca, &ua,
		); err != nil {
			return nil, fmt.Errorf(
				"pika/reflector: scan atom: %w", err,
			)
		}
		if se.Valid {
			a.SourceEventID = &se.Int64
		}
		if sm.Valid {
			a.SourceMessageID = &sm.Int64
		}
		a.Detail = det.String
		if tg.Valid {
			a.Tags = json.RawMessage(tg.String)
		}
		if st.Valid {
			a.SourceTurns = json.RawMessage(st.String)
		}
		if hi.Valid {
			a.History = json.RawMessage(hi.String)
		}
		a.CreatedAt = parseSQLiteTime(ca)
		a.UpdatedAt = parseSQLiteTime(ua)
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- Prompt ---

// loadPromptFile reads reflector prompt from disk.
// Hot-reload: read on every call, 0 restart (D-90).
func (r *ReflectorPipeline) loadPromptFile() (string, error) {
	path := r.cfg.PromptFile
	if path == "" {
		return defaultReflectorPrompt, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultReflectorPrompt, nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

// buildUserContent formats atoms for LLM input.
func (r *ReflectorPipeline) buildUserContent(
	atoms []KnowledgeAtomRow, mode reflectorMode,
) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "{\n  \"scope\": %q,\n  \"atoms\": [\n",
		string(mode))

	for i, a := range atoms {
		if i > 0 {
			sb.WriteString(",\n")
		}
		// Build atom JSON manually for compactness
		fmt.Fprintf(&sb,
			`    {"id": %q, "category": %q, `+
				`"summary": %q, "detail": %q, `+
				`"tags": %s, "polarity": %q, `+
				`"confidence": %.2f, `+
				`"created_at": %q}`,
			a.AtomID, a.Category,
			a.Summary, a.Detail,
			coalesceJSON(a.Tags, "[]"),
			a.Polarity, a.Confidence,
			a.CreatedAt.Format(time.RFC3339),
		)
	}

	sb.WriteString("\n  ]\n}")
	return sb.String()
}

// coalesceJSON returns raw JSON or fallback if nil.
func coalesceJSON(
	j json.RawMessage, fallback string,
) string {
	if j == nil || len(j) == 0 {
		return fallback
	}
	return string(j)
}

// --- LLM Call ---

// callWithRetry calls LLM with repair retry on parse failure.
func (r *ReflectorPipeline) callWithRetry(
	ctx context.Context,
	promptText, userContent string,
) (*reflectorLLMResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= r.cfg.MaxRetries; attempt++ {
		sysPrompt := promptText
		if attempt > 0 && lastErr != nil {
			sysPrompt += "\n\n## REPAIR\n" +
				"Previous output had a validation error:\n" +
				lastErr.Error() + "\n" +
				"Fix the error and respond with valid JSON."
		}

		raw, callErr := r.callLLM(
			ctx, sysPrompt, userContent,
		)
		if callErr != nil {
			lastErr = callErr
			continue
		}

		parsed, parseErr := parseReflectorOutput(raw)
		if parseErr != nil {
			lastErr = parseErr
			continue
		}

		return parsed, nil
	}
	return nil, fmt.Errorf("retries exhausted: %w", lastErr)
}

// callLLM sends system+user to LLM, returns raw content.
func (r *ReflectorPipeline) callLLM(
	ctx context.Context,
	sysPrompt, userContent string,
) (string, error) {
	timeoutDur := time.Duration(r.cfg.TimeoutMs) *
		time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeoutDur)
	defer cancel()

	msgs := []providers.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: userContent},
	}
	resp, err := r.provider.Chat(
		ctx, msgs, nil, r.cfg.Model, nil,
	)
	if err != nil {
		return "", fmt.Errorf("LLM call: %w", err)
	}
	return resp.Content, nil
}

// parseReflectorOutput extracts and parses JSON from LLM.
func parseReflectorOutput(
	raw string,
) (*reflectorLLMResponse, error) {
	jsonStr := extractReflectorJSON(raw)
	if jsonStr == "" {
		return nil, fmt.Errorf(
			"no JSON found in LLM response",
		)
	}
	var out reflectorLLMResponse
	if err := json.Unmarshal(
		[]byte(jsonStr), &out,
	); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	// Ensure nil slices become empty slices
	if out.Merges == nil {
		out.Merges = []reflectorMerge{}
	}
	if out.Patterns == nil {
		out.Patterns = []reflectorPattern{}
	}
	if out.ConfidenceUpdates == nil {
		out.ConfidenceUpdates = []reflectorConfUpdate{}
	}
	if out.RunbookDrafts == nil {
		out.RunbookDrafts = []reflectorRunbook{}
	}
	return &out, nil
}

// extractReflectorJSON reuses the balanced-pair extraction.
func extractReflectorJSON(s string) string {
	return extractBalancedPair(s, '{', '}')
}

// --- Apply Results ---

// buildAtomIndex creates atom_id → KnowledgeAtomRow lookup.
func buildAtomIndex(
	atoms []KnowledgeAtomRow,
) map[string]*KnowledgeAtomRow {
	idx := make(
		map[string]*KnowledgeAtomRow, len(atoms),
	)
	for i := range atoms {
		idx[atoms[i].AtomID] = &atoms[i]
	}
	return idx
}

// applyResults applies LLM output to bot_memory.db.
func (r *ReflectorPipeline) applyResults(
	ctx context.Context,
	output *reflectorLLMResponse,
	atomIndex map[string]*KnowledgeAtomRow,
	atoms []KnowledgeAtomRow,
) error {
	// Apply merges
	for _, m := range output.Merges {
		if err := r.applyMerge(
			ctx, m, atomIndex,
		); err != nil {
			// Log and skip invalid merges
			continue
		}
	}

	// Apply patterns
	for _, p := range output.Patterns {
		if err := r.applyPattern(
			ctx, p, atomIndex,
		); err != nil {
			continue
		}
	}

	// Apply confidence updates
	for _, u := range output.ConfidenceUpdates {
		if err := r.applyConfidenceUpdate(
			ctx, u, atomIndex,
		); err != nil {
			continue
		}
	}

	// Apply runbook drafts
	for _, rb := range output.RunbookDrafts {
		if err := r.applyRunbook(
			ctx, rb,
		); err != nil {
			continue
		}
	}

	return nil
}

// applyMerge merges duplicate atoms (D-147).
// 1 txn: INSERT merged → UPDATE atom_usage → DELETE originals.
func (r *ReflectorPipeline) applyMerge(
	ctx context.Context,
	m reflectorMerge,
	atomIndex map[string]*KnowledgeAtomRow,
) error {
	if len(m.SourceAtomIDs) < 2 {
		return fmt.Errorf("merge needs >= 2 source atoms")
	}
	if m.Summary == "" {
		return fmt.Errorf("merge summary empty")
	}

	// Validate all source atoms exist and same polarity
	var parents []*KnowledgeAtomRow
	var polarity string
	for _, aid := range m.SourceAtomIDs {
		a, ok := atomIndex[aid]
		if !ok {
			return fmt.Errorf(
				"atom %q not found", aid,
			)
		}
		if polarity == "" {
			polarity = a.Polarity
		} else if a.Polarity != polarity {
			// D-147: different polarity = contradiction
			return fmt.Errorf(
				"polarity mismatch: %q vs %q",
				polarity, a.Polarity,
			)
		}
		parents = append(parents, a)
	}

	// Use LLM's polarity if valid, else parent polarity
	mergedPolarity := m.Polarity
	if !validPolarities[mergedPolarity] {
		mergedPolarity = polarity
	}

	// Use LLM's category if valid, else first parent
	mergedCategory := m.Category
	if !validReflectorCategories[mergedCategory] {
		mergedCategory = parents[0].Category
	}

	// Confidence = AVG of parents (D-147)
	var confSum float64
	for _, p := range parents {
		confSum += p.Confidence
	}
	avgConf := confSum / float64(len(parents))

	// Tags = UNION of all parent tags
	mergedTags := unionTags(parents)

	// Source turns = UNION
	mergedSourceTurns := unionSourceTurns(parents)

	// History: add merge record
	histEntry := map[string]interface{}{
		"merged_from": m.SourceAtomIDs,
		"parent_avg":  avgConf,
		"by":          "reflexor",
		"at": time.Now().UTC().Format(
			time.RFC3339,
		),
		"reason": m.Reason,
	}
	histJSON, _ := json.Marshal(
		[]interface{}{histEntry},
	)

	var tagsJSON json.RawMessage
	if len(mergedTags) > 0 {
		tagsJSON, _ = json.Marshal(mergedTags)
	}

	var stJSON json.RawMessage
	if len(mergedSourceTurns) > 0 {
		stJSON, _ = json.Marshal(mergedSourceTurns)
	}

	// Generate new atom ID
	newAtomID, err := r.atomGen.Next(
		ctx, mergedCategory,
	)
	if err != nil {
		return fmt.Errorf("gen atom_id: %w", err)
	}

	// Execute in transaction
	tx, err := r.mem.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin merge tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// INSERT merged atom
	_, err = tx.ExecContext(ctx,
		`INSERT INTO knowledge_atoms
		(atom_id, session_id, turn_id,
		category, summary, detail, confidence,
		polarity, verified, tags,
		source_turns, history)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		newAtomID, parents[0].SessionID,
		parents[0].TurnID,
		mergedCategory, m.Summary,
		strOrNil(m.Detail), avgConf,
		mergedPolarity, 0,
		jsonArg(tagsJSON),
		jsonArg(stJSON),
		string(histJSON),
	)
	if err != nil {
		return fmt.Errorf(
			"insert merged atom: %w", err,
		)
	}

	// UPDATE atom_usage: relink to new atom
	for _, aid := range m.SourceAtomIDs {
		_, err = tx.ExecContext(ctx,
			`UPDATE atom_usage SET atom_id=?
			WHERE atom_id=?`,
			newAtomID, aid,
		)
		if err != nil {
			return fmt.Errorf(
				"relink atom_usage: %w", err,
			)
		}
	}

	// DELETE originals
	ph := placeholders(len(m.SourceAtomIDs))
	delArgs := make([]any, len(m.SourceAtomIDs))
	for i, aid := range m.SourceAtomIDs {
		delArgs[i] = aid
	}
	_, err = tx.ExecContext(ctx,
		`DELETE FROM knowledge_atoms
		WHERE atom_id IN (`+ph+`)`,
		delArgs...,
	)
	if err != nil {
		return fmt.Errorf(
			"delete originals: %w", err,
		)
	}

	return tx.Commit()
}

// applyPattern inserts a new pattern atom.
func (r *ReflectorPipeline) applyPattern(
	ctx context.Context,
	p reflectorPattern,
	atomIndex map[string]*KnowledgeAtomRow,
) error {
	if p.Summary == "" {
		return fmt.Errorf("pattern summary empty")
	}

	// Validate source atoms exist
	for _, aid := range p.SourceAtoms {
		if _, ok := atomIndex[aid]; !ok {
			return fmt.Errorf(
				"source atom %q not found", aid,
			)
		}
	}

	polarity := p.Polarity
	if !validPolarities[polarity] {
		polarity = "negative" // patterns are usually issues
	}

	atomID, err := r.atomGen.Next(ctx, "pattern")
	if err != nil {
		return fmt.Errorf("gen atom_id: %w", err)
	}

	var tagsJSON json.RawMessage
	if len(p.Tags) > 0 {
		tagsJSON, _ = json.Marshal(p.Tags)
	}

	var srcJSON json.RawMessage
	if len(p.SourceAtoms) > 0 {
		srcJSON, _ = json.Marshal(p.SourceAtoms)
	}

	// Determine session_id/turn_id from first source atom
	sid := "reflector"
	tid := 0
	if len(p.SourceAtoms) > 0 {
		if a, ok := atomIndex[p.SourceAtoms[0]]; ok {
			sid = a.SessionID
			tid = a.TurnID
		}
	}

	histEntry := map[string]interface{}{
		"v": 1, "confidence": 0.5,
		"by": "reflexor",
		"at": time.Now().UTC().Format(
			time.RFC3339,
		),
	}
	histJSON, _ := json.Marshal(
		[]interface{}{histEntry},
	)

	row := KnowledgeAtomRow{
		AtomID:      atomID,
		SessionID:   sid,
		TurnID:      tid,
		Category:    "pattern",
		Summary:     p.Summary,
		Detail:      fmt.Sprintf("type: %s", p.Type),
		Confidence:  0.5,
		Polarity:    polarity,
		Tags:        tagsJSON,
		SourceTurns: srcJSON,
		History:     histJSON,
	}
	return r.mem.InsertAtom(ctx, row)
}

// applyConfidenceUpdate updates confidence for an atom (D-59).
func (r *ReflectorPipeline) applyConfidenceUpdate(
	ctx context.Context,
	u reflectorConfUpdate,
	atomIndex map[string]*KnowledgeAtomRow,
) error {
	a, ok := atomIndex[u.AtomID]
	if !ok {
		return fmt.Errorf(
			"atom %q not found", u.AtomID,
		)
	}

	// Validate delta range
	if u.Delta < -1.0 || u.Delta > 1.0 {
		return fmt.Errorf(
			"delta %.2f out of range", u.Delta,
		)
	}

	// Clamp new confidence to [0.0, 1.0]
	newConf := clampConfidence(
		a.Confidence + u.Delta,
	)

	histEntry := map[string]interface{}{
		"v":          len(a.History)/50 + 2,
		"confidence": newConf,
		"by":         "reflexor",
		"at": time.Now().UTC().Format(
			time.RFC3339,
		),
		"reason": u.Reason,
		"delta":  u.Delta,
	}
	histJSON, _ := json.Marshal(histEntry)

	return r.mem.UpdateAtomConfidence(
		ctx, u.AtomID, newConf, histJSON,
	)
}

// applyRunbook inserts a runbook_draft atom (D-87, F9-5).
func (r *ReflectorPipeline) applyRunbook(
	ctx context.Context,
	rb reflectorRunbook,
) error {
	if rb.Tag == "" || len(rb.Steps) == 0 {
		return fmt.Errorf("runbook missing tag or steps")
	}

	atomID, err := r.atomGen.Next(
		ctx, "runbook_draft",
	)
	if err != nil {
		return fmt.Errorf("gen atom_id: %w", err)
	}

	detailMap := map[string]interface{}{
		"steps":    rb.Steps,
		"trigger":  rb.Trigger,
		"rollback": rb.Rollback,
	}
	detailJSON, _ := json.Marshal(detailMap)

	tagsJSON, _ := json.Marshal([]string{rb.Tag})

	histEntry := map[string]interface{}{
		"v": 1, "confidence": 0.5,
		"by": "reflexor",
		"at": time.Now().UTC().Format(
			time.RFC3339,
		),
	}
	histJSON, _ := json.Marshal(
		[]interface{}{histEntry},
	)

	row := KnowledgeAtomRow{
		AtomID:     atomID,
		SessionID:  "reflector",
		TurnID:     0,
		Category:   "runbook_draft",
		Summary:    fmt.Sprintf("Runbook: %s", rb.Tag),
		Detail:     string(detailJSON),
		Confidence: 0.5,
		Polarity:   "neutral",
		Tags:       tagsJSON,
		History:    histJSON,
	}
	return r.mem.InsertAtom(ctx, row)
}

// markStaleAtoms marks atoms with confidence < 0.2 (D-147).
// Monthly only. Does not delete or escalate.
func (r *ReflectorPipeline) markStaleAtoms(
	ctx context.Context,
) error {
	// Stale = confidence < 0.2 — no action needed,
	// they naturally fall below retrieval threshold.
	// This is a no-op marker for diagnostics.
	// Future: could add a 'stale' flag if needed.
	return nil
}

// --- Tag/SourceTurns Helpers ---

// unionTags collects unique tags from all parents.
func unionTags(
	parents []*KnowledgeAtomRow,
) []string {
	seen := make(map[string]bool)
	var result []string
	for _, p := range parents {
		if p.Tags == nil {
			continue
		}
		var tags []string
		if err := json.Unmarshal(
			p.Tags, &tags,
		); err != nil {
			continue
		}
		for _, t := range tags {
			if !seen[t] {
				seen[t] = true
				result = append(result, t)
			}
		}
	}
	return result
}

// unionSourceTurns collects unique source turns.
func unionSourceTurns(
	parents []*KnowledgeAtomRow,
) []json.RawMessage {
	type turnRef struct {
		SessionID string `json:"session_id"`
		TurnID    int    `json:"turn_id"`
	}
	seen := make(map[string]bool)
	var result []turnRef
	for _, p := range parents {
		if p.SourceTurns == nil {
			continue
		}
		var turns []turnRef
		if err := json.Unmarshal(
			p.SourceTurns, &turns,
		); err != nil {
			continue
		}
		for _, t := range turns {
			key := fmt.Sprintf(
				"%s:%d", t.SessionID, t.TurnID,
			)
			if !seen[key] {
				seen[key] = true
				result = append(result, t)
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	var out []json.RawMessage
	for _, r := range result {
		j, _ := json.Marshal(r)
		out = append(out, j)
	}
	return out
}

// clampConfidence clamps v to [0.0, 1.0].
func clampConfidence(v float64) float64 {
	return math.Max(0.0, math.Min(1.0, v))
}

// --- Valid categories for reflector ---

var validReflectorCategories = map[string]bool{
	"pattern":       true,
	"constraint":    true,
	"decision":      true,
	"tool_pref":     true,
	"summary":       true,
	"runbook_draft": true,
}

// --- Telemetry ---

func (r *ReflectorPipeline) reportFailure() {
	if r.telemetry != nil {
		r.telemetry.ReportComponentFailure(
			"reflector", "degraded",
		)
	}
}

func (r *ReflectorPipeline) reportSuccess() {
	if r.telemetry != nil {
		r.telemetry.ReportComponentSuccess("reflector")
	}
}

// --- Default prompt ---

const defaultReflectorPrompt = `<role>
You are REFLEXOR — a behavioral optimization engine.
You receive knowledge atoms and return structured JSON analysis.
You do NOT have tools. You do NOT interact with users.
You only analyze data.
</role>

<instructions>
Analyze the provided knowledge_atoms and produce a JSON response with these sections:

1. MERGES — find semantically identical atoms.
   Merge only atoms with the same polarity.
   For each group: list source_atom_ids, provide merged summary.

2. PATTERNS — detect recurring themes across atoms.
   Anti-patterns: 3+ negative atoms with same tags.
   Positive patterns: consistently successful strategies.

3. CONFIDENCE_UPDATES — atoms whose confidence should change.
   Increase (+0.1): atom confirmed by new evidence.
   Decrease (-0.2): atom contradicted by newer evidence.
   NEVER change confidence based on time alone.

4. RUNBOOK_DRAFTS — draft runbooks for recurring failures.
   Trigger: 3+ negative-polarity atoms with overlapping tags.
   Format: steps, trigger condition, rollback procedure.
</instructions>

<output_format>
Return a single JSON object. Empty arrays are valid.
Do NOT fabricate findings. Silence > noise.

{
  "merges": [{"source_atom_ids": [...], "summary": "...",
    "detail": "...", "category": "...", "polarity": "...",
    "reason": "..."}],
  "patterns": [{"type": "antipattern"|"recurring_failure",
    "summary": "...", "tags": [...], "polarity": "...",
    "source_atoms": [...]}],
  "confidence_updates": [{"atom_id": "...",
    "delta": 0.1, "reason": "..."}],
  "runbook_drafts": [{"tag": "...", "steps": [...],
    "trigger": "...", "rollback": "..."}]
}
</output_format>

<rules>
- Atom IDs: use exact IDs from input. NEVER invent IDs.
- Confidence: ONLY based on evidence. No time-based decay.
- Merges: ONLY same-polarity atoms.
- Runbook drafts: ONLY when 3+ negative atoms share tags.
- Empty sections: return empty array [], not omit the key.
</rules>
`
