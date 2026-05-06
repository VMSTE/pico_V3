// PIKA-V3: Reflector — Go pipeline analyzing knowledge_atoms.
// Cheap LLM (structured output, 0 tool calls). 3 modes:
// daily/weekly/monthly. Cron-driven.
// Decision: D-134, D-26, F9-5, D-147, D-59.

package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// --- Mode constants ---

const (
	ReflectorDaily   = "daily"
	ReflectorWeekly  = "weekly"
	ReflectorMonthly = "monthly"
)

// --- Configuration ---

// ReflectorConfig holds reflector-specific parameters.
type ReflectorConfig struct {
	Enabled    bool
	PromptFile string // hot-reload path (D-90)
	MaxRetries int    // LLM validation retries
	Model      string // LLM model name
	TimeoutMs  int    // per-run timeout
}

// DefaultReflectorConfig returns defaults per ТЗ-v2-5b.
func DefaultReflectorConfig() ReflectorConfig {
	return ReflectorConfig{
		Enabled:    true,
		PromptFile: "/workspace/prompts/reflexor.md",
		MaxRetries: 1,
		Model:      "background",
		TimeoutMs:  120000,
	}
}

// ReflectorSchedule holds cron schedule strings.
type ReflectorSchedule struct {
	Daily   string // e.g. "03:00"
	Weekly  string // e.g. "Sun 04:00"
	Monthly string // e.g. "1st 05:00"
}

// --- LLM Output Types ---

type reflectorDuplicate struct {
	KeepID   string   `json:"keep_id"`
	MergeIDs []string `json:"merge_ids"`
	Reason   string   `json:"reason"`
}

type reflectorPattern struct {
	Type            string   `json:"type"`
	Summary         string   `json:"summary"`
	EvidenceAtomIDs []string `json:"evidence_atom_ids"`
	Tags            []string `json:"tags"`
	Polarity        string   `json:"polarity"`
	SuggestedAction string   `json:"suggested_action"`
}

type reflectorConfUpdate struct {
	AtomID         string  `json:"atom_id"`
	CurrentConf    float64 `json:"current_confidence"`
	NewConf        float64 `json:"new_confidence"`
	Direction      string  `json:"direction"`
	Reason         string  `json:"reason"`
	EvidenceAtomID string  `json:"evidence_atom_id"`
}

type reflectorRunbook struct {
	Trigger         string   `json:"trigger"`
	Tags            []string `json:"tags"`
	EvidenceAtomIDs []string `json:"evidence_atom_ids"`
	Steps           []string `json:"steps"`
	Rollback        string   `json:"rollback"`
}

type reflectorLLMResponse struct {
	Duplicates  []reflectorDuplicate  `json:"duplicates"`
	Patterns    []reflectorPattern    `json:"patterns"`
	ConfUpdates []reflectorConfUpdate `json:"confidence_updates"`
	Runbooks    []reflectorRunbook    `json:"runbook_drafts"`
}

// --- atomForLLM is the JSON shape sent to LLM ---

type reflectorAtomForLLM struct {
	ID         string   `json:"id"`
	Category   string   `json:"category"`
	Summary    string   `json:"summary"`
	Detail     string   `json:"detail,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Polarity   string   `json:"polarity"`
	Confidence float64  `json:"confidence"`
	CreatedAt  string   `json:"created_at"`
}

// --- Reflector Pipeline ---

// ReflectorPipeline orchestrates the reflector.
// Stateless between calls (D-90). Thread-safe.
type ReflectorPipeline struct {
	mem       *BotMemory
	atomGen   *AtomIDGenerator
	provider  providers.LLMProvider
	telemetry *Telemetry
	cfg       ReflectorConfig
	diag      *DiagnosticsEngine
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
	return &ReflectorPipeline{
		mem:       mem,
		atomGen:   atomGen,
		provider:  provider,
		telemetry: telemetry,
		cfg:       cfg,
	}
}

// Run executes the reflector pipeline for the given mode.
// Pipeline: data prep → LLM → parse → apply.
func (r *ReflectorPipeline) Run(
	ctx context.Context, mode string,
) error {
	if !r.cfg.Enabled {
		return nil
	}

	// Step 1: Data prep — fetch knowledge_atoms by scope
	atoms, err := r.fetchAtoms(ctx, mode)
	if err != nil {
		r.reportFailure()
		return fmt.Errorf(
			"pika/reflector: fetch atoms: %w", err,
		)
	}
	if len(atoms) == 0 {
		// Empty knowledge_atoms → skip LLM call (normal)
		return nil
	}

	// Step 2: Load prompt (hot-reload, 0 restart)
	promptText, err := r.loadPromptFile()
	if err != nil {
		r.reportFailure()
		return fmt.Errorf(
			"pika/reflector: load prompt: %w", err,
		)
	}

	// Step 3: Build user content
	userContent := r.buildUserContent(atoms, mode)

	// Step 4: LLM call with retry on invalid JSON
	output, err := r.callWithRetry(
		ctx, promptText, userContent, atoms,
	)
	if err != nil {
		r.reportFailure()
		return fmt.Errorf(
			"pika/reflector: LLM pipeline: %w", err,
		)
	}

	// Step 5: Apply results
	if applyErr := r.applyResults(
		ctx, output, atoms,
	); applyErr != nil {
		r.reportFailure()
		return fmt.Errorf(
			"pika/reflector: apply: %w", applyErr,
		)
	}

	// Step 6: Monthly-specific tasks
	if mode == ReflectorMonthly {
		if mErr := r.runMonthlyTasks(
			ctx, atoms,
		); mErr != nil {
			log.Printf(
				"[reflector] monthly tasks warning: %v",
				mErr,
			)
		}
	}

	r.reportSuccess()
	return nil
}

// --- Data Prep ---

func (r *ReflectorPipeline) fetchAtoms(
	ctx context.Context, mode string,
) ([]KnowledgeAtomRow, error) {
	var query string
	switch mode {
	case ReflectorDaily:
		query = `SELECT id, atom_id, session_id, turn_id,
			source_event_id, source_message_id, category,
			summary, detail, confidence, polarity, verified,
			tags, source_turns, history,
			created_at, updated_at
			FROM knowledge_atoms
			WHERE created_at > datetime('now', '-1 day')
			ORDER BY id ASC`
	case ReflectorWeekly:
		query = `SELECT id, atom_id, session_id, turn_id,
			source_event_id, source_message_id, category,
			summary, detail, confidence, polarity, verified,
			tags, source_turns, history,
			created_at, updated_at
			FROM knowledge_atoms
			WHERE created_at > datetime('now', '-7 days')
			ORDER BY id ASC`
	case ReflectorMonthly:
		query = `SELECT id, atom_id, session_id, turn_id,
			source_event_id, source_message_id, category,
			summary, detail, confidence, polarity, verified,
			tags, source_turns, history,
			created_at, updated_at
			FROM knowledge_atoms
			ORDER BY id ASC`
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}

	rows, err := r.mem.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query atoms: %w", err)
	}
	defer rows.Close()
	return scanKnowledgeAtomRows(rows)
}

func scanKnowledgeAtomRows(
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
			return nil, fmt.Errorf("scan atom: %w", err)
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

// --- User Content Builder ---

func (r *ReflectorPipeline) buildUserContent(
	atoms []KnowledgeAtomRow, mode string,
) string {
	type payload struct {
		Scope string                `json:"scope"`
		Atoms []reflectorAtomForLLM `json:"atoms"`
	}
	p := payload{
		Scope: mode,
		Atoms: make(
			[]reflectorAtomForLLM, 0, len(atoms),
		),
	}
	for _, a := range atoms {
		item := reflectorAtomForLLM{
			ID:         a.AtomID,
			Category:   a.Category,
			Summary:    a.Summary,
			Detail:     a.Detail,
			Polarity:   a.Polarity,
			Confidence: a.Confidence,
			CreatedAt:  a.CreatedAt.Format(time.RFC3339),
		}
		if a.Tags != nil {
			var tags []string
			if json.Unmarshal(a.Tags, &tags) == nil {
				item.Tags = tags
			}
		}
		p.Atoms = append(p.Atoms, item)
	}
	j, _ := json.Marshal(p)
	return string(j)
}

// --- LLM Call ---

func (r *ReflectorPipeline) callWithRetry(
	ctx context.Context,
	promptText, userContent string,
	atoms []KnowledgeAtomRow,
) (*reflectorLLMResponse, error) {
	atomIDs := make(map[string]bool, len(atoms))
	for _, a := range atoms {
		atomIDs[a.AtomID] = true
	}

	var lastErr error
	for attempt := 0; attempt <= r.cfg.MaxRetries; attempt++ {
		sysPrompt := promptText
		if attempt > 0 && lastErr != nil {
			sysPrompt += "\n\n## REPAIR\n" +
				"Previous output had a validation error:\n" +
				lastErr.Error() + "\n" +
				"Fix the error and return valid JSON."
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

		if valErr := validateReflectorOutput(
			parsed, atomIDs,
		); valErr != nil {
			lastErr = valErr
			continue
		}

		return parsed, nil
	}
	return nil, fmt.Errorf("retries exhausted: %w", lastErr)
}

func (r *ReflectorPipeline) callLLM(
	ctx context.Context,
	sysPrompt, userContent string,
) (string, error) {
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

func parseReflectorOutput(
	raw string,
) (*reflectorLLMResponse, error) {
	// Reuse extractBalancedPair from atomizer.go
	jsonStr := extractBalancedPair(raw, '{', '}')
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
	return &out, nil
}

func validateReflectorOutput(
	resp *reflectorLLMResponse,
	validIDs map[string]bool,
) error {
	// Validate duplicates
	for i, d := range resp.Duplicates {
		if d.KeepID == "" {
			return fmt.Errorf(
				"duplicates[%d]: empty keep_id", i,
			)
		}
		if !validIDs[d.KeepID] {
			return fmt.Errorf(
				"duplicates[%d]: keep_id %q not found",
				i, d.KeepID,
			)
		}
		if len(d.MergeIDs) == 0 {
			return fmt.Errorf(
				"duplicates[%d]: empty merge_ids", i,
			)
		}
		for _, mid := range d.MergeIDs {
			if !validIDs[mid] {
				return fmt.Errorf(
					"duplicates[%d]: merge_id %q not found",
					i, mid,
				)
			}
		}
	}
	// Validate patterns
	for i, p := range resp.Patterns {
		if p.Summary == "" {
			return fmt.Errorf(
				"patterns[%d]: empty summary", i,
			)
		}
		if !validPolarities[p.Polarity] {
			return fmt.Errorf(
				"patterns[%d]: invalid polarity %q",
				i, p.Polarity,
			)
		}
	}
	// Validate confidence updates
	for i, u := range resp.ConfUpdates {
		if u.AtomID == "" {
			return fmt.Errorf(
				"confidence_updates[%d]: empty atom_id", i,
			)
		}
		if !validIDs[u.AtomID] {
			// Hallucinated ID — warn, will skip in apply
			log.Printf(
				"[reflector] warn: confidence_updates[%d]: "+
					"atom_id %q not found, will skip",
				i, u.AtomID,
			)
		}
		if u.NewConf < 0 || u.NewConf > 1 {
			return fmt.Errorf(
				"confidence_updates[%d]: confidence %.2f "+
					"out of [0,1]",
				i, u.NewConf,
			)
		}
	}
	// Validate runbook drafts
	for i, rd := range resp.Runbooks {
		if len(rd.Steps) == 0 {
			return fmt.Errorf(
				"runbook_drafts[%d]: empty steps", i,
			)
		}
		if rd.Trigger == "" {
			return fmt.Errorf(
				"runbook_drafts[%d]: empty trigger", i,
			)
		}
	}
	return nil
}

// --- Apply Results ---

func (r *ReflectorPipeline) applyResults(
	ctx context.Context,
	resp *reflectorLLMResponse,
	atoms []KnowledgeAtomRow,
) error {
	atomMap := make(
		map[string]*KnowledgeAtomRow, len(atoms),
	)
	for i := range atoms {
		atomMap[atoms[i].AtomID] = &atoms[i]
	}

	// 1. Merge duplicates (D-147)
	for _, dup := range resp.Duplicates {
		if err := r.applyMerge(
			ctx, dup, atomMap,
		); err != nil {
			log.Printf(
				"[reflector] merge error: %v", err,
			)
		}
	}

	// 2. Insert patterns
	for _, pat := range resp.Patterns {
		if err := r.applyPattern(
			ctx, pat,
		); err != nil {
			log.Printf(
				"[reflector] pattern error: %v", err,
			)
		}
	}

	// 3. Confidence updates (D-59)
	for _, upd := range resp.ConfUpdates {
		if err := r.applyConfUpdate(
			ctx, upd, atomMap,
		); err != nil {
			log.Printf(
				"[reflector] confidence error: %v", err,
			)
		}
	}

	// 4. Runbook drafts (D-87, F9-5)
	for _, rd := range resp.Runbooks {
		if err := r.applyRunbook(
			ctx, rd,
		); err != nil {
			log.Printf(
				"[reflector] runbook error: %v", err,
			)
		}
	}

	return nil
}

// applyMerge: INSERT merged + UPDATE atom_usage + DELETE.
// Merge contract (D-147): only same polarity.
func (r *ReflectorPipeline) applyMerge(
	ctx context.Context,
	dup reflectorDuplicate,
	atomMap map[string]*KnowledgeAtomRow,
) error {
	keeper := atomMap[dup.KeepID]
	if keeper == nil {
		return fmt.Errorf(
			"keeper %q not in map", dup.KeepID,
		)
	}

	// D-147: polarity validation
	for _, mid := range dup.MergeIDs {
		m := atomMap[mid]
		if m == nil {
			continue
		}
		if m.Polarity != keeper.Polarity {
			log.Printf(
				"[reflector] skip merge %q+%q: "+
					"polarity mismatch (%s vs %s)",
				dup.KeepID, mid,
				keeper.Polarity, m.Polarity,
			)
			return nil
		}
	}

	// Collect stats from all parents
	allIDs := append(
		[]string{dup.KeepID}, dup.MergeIDs...,
	)
	var confSum float64
	var confCount int
	var allTags []string
	for _, aid := range allIDs {
		a := atomMap[aid]
		if a == nil {
			continue
		}
		confSum += a.Confidence
		confCount++
		if a.Tags != nil {
			var tags []string
			if json.Unmarshal(a.Tags, &tags) == nil {
				allTags = append(allTags, tags...)
			}
		}
	}
	if confCount == 0 {
		confCount = 1
	}
	avgConf := confSum / float64(confCount)
	mergedTags := deduplicateStrings(allTags)

	// Generate new atom_id
	newAtomID, err := r.atomGen.Next(
		ctx, keeper.Category,
	)
	if err != nil {
		return fmt.Errorf("gen atom_id: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	histEntry := map[string]any{
		"v":           1,
		"confidence":  avgConf,
		"by":          "reflector",
		"at":          now,
		"merged_from": allIDs,
		"parent_avg":  avgConf,
	}
	histJSON, _ := json.Marshal([]any{histEntry})

	var tagsJSON json.RawMessage
	if len(mergedTags) > 0 {
		tagsJSON, _ = json.Marshal(mergedTags)
	}

	// Transaction: INSERT + UPDATE + DELETE
	tx, err := r.mem.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.ExecContext(ctx,
		`INSERT INTO knowledge_atoms
		(atom_id, session_id, turn_id, category,
		 summary, detail, confidence, polarity,
		 tags, source_turns, history)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		newAtomID, keeper.SessionID, keeper.TurnID,
		keeper.Category, keeper.Summary,
		strOrNil(keeper.Detail), avgConf,
		keeper.Polarity, jsonArg(tagsJSON),
		jsonArg(keeper.SourceTurns), string(histJSON))
	if err != nil {
		return fmt.Errorf("insert merged: %w", err)
	}

	for _, aid := range allIDs {
		_, err = tx.ExecContext(ctx,
			`UPDATE atom_usage SET atom_id=?
			WHERE atom_id=?`, newAtomID, aid)
		if err != nil {
			return fmt.Errorf(
				"update atom_usage: %w", err,
			)
		}
	}

	for _, aid := range allIDs {
		_, err = tx.ExecContext(ctx,
			`DELETE FROM knowledge_atoms
			WHERE atom_id=?`, aid)
		if err != nil {
			return fmt.Errorf(
				"delete original %s: %w", aid, err,
			)
		}
	}

	return tx.Commit()
}

func (r *ReflectorPipeline) applyPattern(
	ctx context.Context, pat reflectorPattern,
) error {
	atomID, err := r.atomGen.Next(ctx, "pattern")
	if err != nil {
		return fmt.Errorf("gen atom_id: %w", err)
	}

	var tagsJSON json.RawMessage
	if len(pat.Tags) > 0 {
		tagsJSON, _ = json.Marshal(pat.Tags)
	}
	evidenceJSON, _ := json.Marshal(
		pat.EvidenceAtomIDs,
	)

	polarity := pat.Polarity
	if !validPolarities[polarity] {
		polarity = "negative"
	}

	row := KnowledgeAtomRow{
		AtomID:      atomID,
		SessionID:   "reflector",
		TurnID:      0,
		Category:    "pattern",
		Summary:     pat.Summary,
		Detail:      pat.SuggestedAction,
		Confidence:  0.5,
		Polarity:    polarity,
		Tags:        tagsJSON,
		SourceTurns: evidenceJSON,
	}
	return r.mem.InsertAtom(ctx, row)
}

func (r *ReflectorPipeline) applyConfUpdate(
	ctx context.Context,
	upd reflectorConfUpdate,
	atomMap map[string]*KnowledgeAtomRow,
) error {
	existing := atomMap[upd.AtomID]
	if existing == nil {
		return nil // hallucinated ID — skip
	}

	// Clamp confidence to [0, 1]
	newConf := math.Max(0, math.Min(1, upd.NewConf))

	now := time.Now().UTC().Format(time.RFC3339)
	histEntry, _ := json.Marshal(map[string]any{
		"v":          existing.Confidence,
		"confidence": newConf,
		"by":         "reflector",
		"at":         now,
	})

	return r.mem.UpdateAtomConfidence(
		ctx, upd.AtomID, newConf, histEntry,
	)
}

func (r *ReflectorPipeline) applyRunbook(
	ctx context.Context, rd reflectorRunbook,
) error {
	atomID, err := r.atomGen.Next(
		ctx, "runbook_draft",
	)
	if err != nil {
		return fmt.Errorf("gen atom_id: %w", err)
	}

	var tagsJSON json.RawMessage
	if len(rd.Tags) > 0 {
		tagsJSON, _ = json.Marshal(rd.Tags)
	}

	detail, _ := json.Marshal(map[string]any{
		"trigger":  rd.Trigger,
		"steps":    rd.Steps,
		"rollback": rd.Rollback,
	})
	evidenceJSON, _ := json.Marshal(
		rd.EvidenceAtomIDs,
	)

	row := KnowledgeAtomRow{
		AtomID:      atomID,
		SessionID:   "reflector",
		TurnID:      0,
		Category:    "runbook_draft",
		Summary:     rd.Trigger,
		Detail:      string(detail),
		Confidence:  0.5,
		Polarity:    "negative",
		Tags:        tagsJSON,
		SourceTurns: evidenceJSON,
	}
	return r.mem.InsertAtom(ctx, row)
}

// --- Monthly Tasks ---

func (r *ReflectorPipeline) runMonthlyTasks(
	ctx context.Context,
	atoms []KnowledgeAtomRow,
) error {
	// 1. Crystallization: confidence >= 0.8 → max
	for _, a := range atoms {
		if a.Confidence >= 0.8 &&
			a.Category == "pattern" {
			now := time.Now().UTC().Format(time.RFC3339)
			histEntry, _ := json.Marshal(map[string]any{
				"v":          a.Confidence,
				"confidence": 1.0,
				"by":         "reflector_crystallize",
				"at":         now,
			})
			if err := r.mem.UpdateAtomConfidence(
				ctx, a.AtomID, 1.0, histEntry,
			); err != nil {
				log.Printf(
					"[reflector] crystallize %s: %v",
					a.AtomID, err,
				)
			}
		}
	}

	// 2. Stale marking (D-147): confidence < 0.2
	// Log only — no deletion, no escalation
	for _, a := range atoms {
		if a.Confidence < 0.2 {
			log.Printf(
				"[reflector] stale atom: %s "+
					"(confidence=%.2f)",
				a.AtomID, a.Confidence,
			)
		}
	}

	return nil
}

// --- Prompt Loading ---

func (r *ReflectorPipeline) loadPromptFile() (
	string, error,
) {
	if r.diag != nil {
		prompt, err := r.diag.BuildSubagentPrompt(context.Background(), "reflexor")
		if err == nil {
			return prompt, nil
		}
		// fallback to default prompt
	}
	path := r.cfg.PromptFile
	if path == "" {
		return defaultReflectorPrompt, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultReflectorPrompt, nil
		}
		return "", fmt.Errorf(
			"read %s: %w", path, err,
		)
	}
	return string(data), nil
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
		r.telemetry.ReportComponentSuccess(
			"reflector",
		)
	}
}

// --- Default Prompt ---

const defaultReflectorPrompt = `<role>
You are REFLEXOR — a behavioral optimization engine.
You receive knowledge atoms and return structured JSON.
You do NOT have tools. You only analyze data.
</role>

<instructions>
Analyze the provided knowledge_atoms and produce JSON:

1. DUPLICATES — semantically identical atoms.
2. PATTERNS — recurring themes (3+ atoms).
3. CONFIDENCE_UPDATES — confirmed (+0.1) or
   contradicted (-0.2) atoms.
4. RUNBOOK_DRAFTS — 3+ negative atoms with same tag.

For every finding, verify evidence is sufficient.
</instructions>

<output_format>
{
  "duplicates": [{"keep_id": "C-12",
    "merge_ids": ["S-7"], "reason": "..."}],
  "patterns": [{"type": "anti_pattern",
    "summary": "...",
    "evidence_atom_ids": ["S-10"],
    "tags": ["deploy"],
    "polarity": "negative",
    "suggested_action": "..."}],
  "confidence_updates": [{"atom_id": "D-5",
    "current_confidence": 0.5,
    "new_confidence": 0.6,
    "direction": "increase",
    "reason": "...",
    "evidence_atom_id": "S-44"}],
  "runbook_drafts": [{"trigger": "...",
    "tags": ["deploy"],
    "evidence_atom_ids": ["S-10","S-23","S-31"],
    "steps": ["1. Check logs"],
    "rollback": "Revert config"}]
}
</output_format>

<rules>
- Empty arrays are valid. Silence > noise.
- Atom IDs: use exact IDs from input.
- Confidence: evidence-based only. No time decay.
- Runbooks: only when 3+ negative atoms share tags.
</rules>
`
