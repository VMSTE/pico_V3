// PIKA-V3: Atomizer — Go pipeline extracting knowledge atoms
// from the hot buffer. LLM provides structured analysis only
// (0 tool calls). Decision: D-58, D-105, D-117, D-133.

package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// --- Configuration ---

// AtomizerConfig holds atomizer-specific parameters (D-133).
type AtomizerConfig struct {
	Enabled        bool
	TriggerTokens  int    // threshold: SUM(tokens) in messages
	ChunkMaxTokens int    // max tokens per chunk
	PromptFile     string // hot-reload prompt path
	MaxRetries     int    // LLM validation retries
	Model          string // LLM model name
}

// DefaultAtomizerConfig returns defaults per D-133.
func DefaultAtomizerConfig() AtomizerConfig {
	return AtomizerConfig{
		Enabled:        true,
		TriggerTokens:  800000,
		ChunkMaxTokens: 200000,
		PromptFile:     "/workspace/prompts/atomizer.md",
		MaxRetries:     2,
		Model:          "background",
	}
}

// --- LLM Output Types ---

// AtomLLMOutput represents a single knowledge atom from LLM.
type AtomLLMOutput struct {
	Category    string  `json:"category"`
	Summary     string  `json:"summary"`
	Detail      string  `json:"detail,omitempty"`
	Polarity    string  `json:"polarity"`
	Confidence  float64 `json:"confidence"`
	SourceTurns []int   `json:"source_turns"`
}

// atomizerLLMResponse is the full structured output from LLM.
type atomizerLLMResponse struct {
	Atoms []AtomLLMOutput `json:"atoms"`
}

// PIKA-V3: valid enum sets for validation.
var validAtomCategories = map[string]bool{
	"summary":    true,
	"tool_pref":  true,
	"decision":   true,
	"constraint": true,
}

var validPolarities = map[string]bool{
	"positive": true,
	"negative": true,
	"neutral":  true,
}

// --- Atomizer ---

// Atomizer extracts knowledge atoms from the hot buffer.
// Go orchestrates the pipeline; LLM provides structured
// analysis only (0 tool calls). Thread-safe: no mutable state.
type Atomizer struct {
	mem       *BotMemory
	atomGen   *AtomIDGenerator
	provider  providers.LLMProvider
	telemetry *Telemetry
	cfg       AtomizerConfig
	diag      *DiagnosticsEngine
}

// NewAtomizer creates a new Atomizer with validated config.
func NewAtomizer(
	mem *BotMemory,
	atomGen *AtomIDGenerator,
	provider providers.LLMProvider,
	telemetry *Telemetry,
	cfg AtomizerConfig,
) *Atomizer {
	if cfg.TriggerTokens <= 0 {
		cfg.TriggerTokens = 800000
	}
	if cfg.ChunkMaxTokens <= 0 {
		cfg.ChunkMaxTokens = 200000
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 2
	}
	if cfg.Model == "" {
		cfg.Model = "background"
	}
	return &Atomizer{
		mem:       mem,
		atomGen:   atomGen,
		provider:  provider,
		telemetry: telemetry,
		cfg:       cfg,
	}
}

// ShouldAtomize checks if session tokens exceed threshold.
func (a *Atomizer) ShouldAtomize(
	ctx context.Context, sessionID string,
) (bool, error) {
	if !a.cfg.Enabled {
		return false, nil
	}
	total, err := a.mem.SumTokensBySession(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf(
			"pika/atomizer: sum tokens: %w", err,
		)
	}
	return total >= int64(a.cfg.TriggerTokens), nil
}

// Run executes the full atomizer pipeline for a session.
// Pipeline: chunk select -> prompt load -> LLM call ->
// parse+validate -> INSERT atoms -> archive+delete (1 txn).
func (a *Atomizer) Run(
	ctx context.Context, sessionID string,
) error {
	// PIKA-V3: Trace span (TZ-v2-9a block 3)
	spanIDatomizer := fmt.Sprintf("span_atomizer_%d", time.Now().UnixNano())
	_ = a.mem.InsertSpan(ctx, TraceSpanRow{
		SpanID: spanIDatomizer, Component: "atomizer", Operation: "run",
		StartedAt: time.Now(), Status: "running",
	})
	defer func() {
		_ = a.mem.CompleteSpan(ctx, spanIDatomizer, "done", nil, "", "")
	}()
	// Step 1: Select chunk (oldest turns <= budget)
	turnIDs, err := a.mem.GetOldestTurnIDs(
		ctx, sessionID, a.cfg.ChunkMaxTokens,
	)
	if err != nil {
		a.reportFailure()
		return fmt.Errorf(
			"pika/atomizer: oldest turns: %w", err,
		)
	}
	if len(turnIDs) == 0 {
		return nil
	}

	// Step 2: Load chunk data (messages + events)
	msgs, err := a.getMessagesByTurns(
		ctx, sessionID, turnIDs,
	)
	if err != nil {
		a.reportFailure()
		return fmt.Errorf(
			"pika/atomizer: get messages: %w", err,
		)
	}
	events, err := a.mem.GetEventsByTurns(
		ctx, sessionID, turnIDs,
	)
	if err != nil {
		a.reportFailure()
		return fmt.Errorf(
			"pika/atomizer: get events: %w", err,
		)
	}

	// Step 3: Load prompt file (hot-reload, 0 restart)
	promptText, err := a.loadPromptFile()
	if err != nil {
		a.reportFailure()
		return fmt.Errorf(
			"pika/atomizer: load prompt: %w", err,
		)
	}

	// Step 4: Build user content from chunk data
	userContent := a.buildUserContent(
		msgs, events, turnIDs,
	)

	// Step 5: LLM call with retry on validation error
	output, err := a.callWithRetry(
		ctx, promptText, userContent, turnIDs,
	)
	if err != nil {
		a.reportFailure()
		return fmt.Errorf(
			"pika/atomizer: LLM pipeline: %w", err,
		)
	}

	// Step 6: Collect tags from events per turn (D-75)
	tagsByTurn := collectTagsByTurn(events)

	// Step 7: INSERT atoms
	for _, atom := range output.Atoms {
		if insErr := a.insertAtom(
			ctx, sessionID, atom, tagsByTurn,
		); insErr != nil {
			a.reportFailure()
			return fmt.Errorf(
				"pika/atomizer: insert: %w", insErr,
			)
		}
	}

	// Step 8: Archive + delete processed turns (1 txn)
	if archErr := a.mem.ArchiveAndDeleteTurns(
		ctx, sessionID, turnIDs,
	); archErr != nil {
		a.reportFailure()
		return fmt.Errorf(
			"pika/atomizer: archive: %w", archErr,
		)
	}

	a.reportSuccess()
	return nil
}

// --- Internal helpers ---

// callWithRetry calls LLM with repair retries on failure.
func (a *Atomizer) callWithRetry(
	ctx context.Context,
	promptText, userContent string,
	validTurns []int,
) (*atomizerLLMResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= a.cfg.MaxRetries; attempt++ {
		sysPrompt := promptText
		if attempt > 0 && lastErr != nil {
			sysPrompt += "\n\n## REPAIR\n" +
				"Previous output had a validation error:\n" +
				lastErr.Error() + "\n" +
				"Fix the error and return valid JSON."
		}

		raw, callErr := a.callLLM(
			ctx, sysPrompt, userContent,
		)
		if callErr != nil {
			lastErr = callErr
			continue
		}

		parsed, parseErr := parseAtomizerOutput(raw)
		if parseErr != nil {
			lastErr = parseErr
			continue
		}

		if valErr := validateAtoms(
			parsed.Atoms, validTurns,
		); valErr != nil {
			lastErr = valErr
			continue
		}

		return parsed, nil
	}
	return nil, fmt.Errorf("retries exhausted: %w", lastErr)
}

// callLLM sends system+user to LLM, returns raw content.
func (a *Atomizer) callLLM(
	ctx context.Context,
	sysPrompt, userContent string,
) (string, error) {
	msgs := []providers.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: userContent},
	}
	resp, err := a.provider.Chat(
		ctx, msgs, nil, a.cfg.Model, nil,
	)
	if err != nil {
		return "", fmt.Errorf("LLM call: %w", err)
	}
	return resp.Content, nil
}

// parseAtomizerOutput extracts and parses JSON from LLM text.
func parseAtomizerOutput(
	raw string,
) (*atomizerLLMResponse, error) {
	jsonStr := extractAtomizerJSON(raw)
	if jsonStr == "" {
		return nil, fmt.Errorf(
			"no JSON found in LLM response",
		)
	}
	var out atomizerLLMResponse
	if err := json.Unmarshal(
		[]byte(jsonStr), &out,
	); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	return &out, nil
}

// validateAtoms checks schema constraints on all atoms.
func validateAtoms(
	atoms []AtomLLMOutput, validTurns []int,
) error {
	if len(atoms) == 0 {
		return fmt.Errorf("no atoms in output")
	}
	turnSet := make(map[int]bool, len(validTurns))
	for _, t := range validTurns {
		turnSet[t] = true
	}
	for i, atom := range atoms {
		if !validAtomCategories[atom.Category] {
			return fmt.Errorf(
				"atom[%d]: invalid category %q",
				i, atom.Category,
			)
		}
		if !validPolarities[atom.Polarity] {
			return fmt.Errorf(
				"atom[%d]: invalid polarity %q",
				i, atom.Polarity,
			)
		}
		if atom.Summary == "" {
			return fmt.Errorf(
				"atom[%d]: empty summary", i,
			)
		}
		if atom.Confidence < 0 || atom.Confidence > 1 {
			return fmt.Errorf(
				"atom[%d]: confidence %.2f out of [0,1]",
				i, atom.Confidence,
			)
		}
		if len(atom.SourceTurns) == 0 {
			return fmt.Errorf(
				"atom[%d]: empty source_turns", i,
			)
		}
		for _, tid := range atom.SourceTurns {
			if !turnSet[tid] {
				return fmt.Errorf(
					"atom[%d]: turn_id %d not in chunk",
					i, tid,
				)
			}
		}
	}
	return nil
}

// insertAtom generates atom_id, merges tags, inserts row.
func (a *Atomizer) insertAtom(
	ctx context.Context,
	sessionID string,
	atom AtomLLMOutput,
	tagsByTurn map[int][]string,
) error {
	atomID, err := a.atomGen.Next(ctx, atom.Category)
	if err != nil {
		return fmt.Errorf("gen atom_id: %w", err)
	}

	// D-75: tags inherited from events by source_turns
	tags := mergeTagsForTurns(
		atom.SourceTurns, tagsByTurn,
	)
	var tagsJSON json.RawMessage
	if len(tags) > 0 {
		tagsJSON, _ = json.Marshal(tags)
	}

	stJSON, _ := json.Marshal(atom.SourceTurns)

	row := KnowledgeAtomRow{
		AtomID:      atomID,
		SessionID:   sessionID,
		TurnID:      atom.SourceTurns[0],
		Category:    atom.Category,
		Summary:     atom.Summary,
		Detail:      atom.Detail,
		Confidence:  atom.Confidence,
		Polarity:    atom.Polarity,
		Tags:        tagsJSON,
		SourceTurns: stJSON,
	}
	return a.mem.InsertAtom(ctx, row)
}

// loadPromptFile reads atomizer prompt from disk.
// Hot-reload: read on every call, 0 restart (D-90).
func (a *Atomizer) loadPromptFile() (string, error) {
	if a.diag != nil {
		prompt, err := a.diag.BuildSubagentPrompt(context.Background(), "atomizer")
		if err == nil {
			return prompt, nil
		}
		// fallback to default prompt
	}
	path := a.cfg.PromptFile
	if path == "" {
		return defaultAtomizerPrompt, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultAtomizerPrompt, nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

// buildUserContent formats chunk data for the LLM.
func (a *Atomizer) buildUserContent(
	msgs []MessageRow,
	events []EventRow,
	turnIDs []int,
) string {
	var sb strings.Builder

	sb.WriteString("## Chunk Turn IDs\n")
	for i, tid := range turnIDs {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%d", tid)
	}
	sb.WriteString("\n\n")

	sb.WriteString("## Messages\n")
	for _, m := range msgs {
		fmt.Fprintf(&sb,
			"[turn=%d role=%s ts=%s]\n%s\n\n",
			m.TurnID, m.Role,
			m.Ts.Format(time.RFC3339), m.Content,
		)
	}

	sb.WriteString("## Events\n")
	for _, e := range events {
		fmt.Fprintf(&sb,
			"[turn=%d type=%s outcome=%s ts=%s]\n%s\n",
			e.TurnID, e.Type, e.Outcome,
			e.Ts.Format(time.RFC3339), e.Summary,
		)
		if e.Tags != nil {
			fmt.Fprintf(&sb,
				"tags: %s\n", string(e.Tags),
			)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// getMessagesByTurns fetches messages for specific turn IDs.
// Same package as BotMemory — accesses unexported db field.
func (a *Atomizer) getMessagesByTurns(
	ctx context.Context, sid string, tids []int,
) ([]MessageRow, error) {
	if len(tids) == 0 {
		return nil, nil
	}
	args := inArgs(sid, tids)
	ph := placeholders(len(tids))
	q := `SELECT id, session_id, turn_id, ts, role,
		content, tokens, msg_index, metadata
		FROM messages WHERE session_id=?
		AND turn_id IN (` + ph + `)
		ORDER BY id ASC`
	rows, err := a.mem.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/atomizer: query msgs: %w", err,
		)
	}
	defer rows.Close()

	var out []MessageRow
	for rows.Next() {
		var m MessageRow
		var ts string
		var content, meta sql.NullString
		var mi sql.NullInt64
		if err := rows.Scan(
			&m.ID, &m.SessionID, &m.TurnID,
			&ts, &m.Role, &content, &m.Tokens,
			&mi, &meta,
		); err != nil {
			return nil, fmt.Errorf(
				"pika/atomizer: scan msg: %w", err,
			)
		}
		m.Ts = parseSQLiteTime(ts)
		m.Content = content.String
		if mi.Valid {
			v := int(mi.Int64)
			m.MsgIndex = &v
		}
		if meta.Valid && meta.String != "" {
			m.Metadata = json.RawMessage(meta.String)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- Tag helpers (D-75) ---

// collectTagsByTurn builds turn_id -> unique tags from events.
func collectTagsByTurn(
	events []EventRow,
) map[int][]string {
	result := make(map[int][]string)
	for _, e := range events {
		if e.Tags == nil {
			continue
		}
		var tags []string
		if err := json.Unmarshal(
			e.Tags, &tags,
		); err != nil {
			continue
		}
		result[e.TurnID] = append(
			result[e.TurnID], tags...,
		)
	}
	for tid, tags := range result {
		result[tid] = deduplicateStrings(tags)
	}
	return result
}

// mergeTagsForTurns collects unique tags from specified turns.
func mergeTagsForTurns(
	turns []int, tagsByTurn map[int][]string,
) []string {
	seen := make(map[string]bool)
	var result []string
	for _, tid := range turns {
		for _, tag := range tagsByTurn[tid] {
			if !seen[tag] {
				seen[tag] = true
				result = append(result, tag)
			}
		}
	}
	return result
}

// deduplicateStrings removes duplicates preserving order.
func deduplicateStrings(s []string) []string {
	seen := make(map[string]bool, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// --- JSON extraction ---

// extractAtomizerJSON finds the outermost balanced JSON block.
// Picks whichever delimiter ({ or [) appears first in the text.
func extractAtomizerJSON(s string) string {
	objIdx := strings.IndexByte(s, '{')
	arrIdx := strings.IndexByte(s, '[')

	// Neither found
	if objIdx < 0 && arrIdx < 0 {
		return ""
	}
	// Only object found
	if arrIdx < 0 {
		return extractBalancedPair(s, '{', '}')
	}
	// Only array found
	if objIdx < 0 {
		return extractBalancedPair(s, '[', ']')
	}
	// Both found — use whichever comes first
	if arrIdx < objIdx {
		return extractBalancedPair(s, '[', ']')
	}
	return extractBalancedPair(s, '{', '}')
}

// extractBalancedPair finds first balanced open/closeCh pair.
func extractBalancedPair(
	s string, openCh, closeCh byte,
) string {
	start := strings.IndexByte(s, openCh)
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		if s[i] == openCh {
			depth++
		} else if s[i] == closeCh {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// --- Telemetry ---

func (a *Atomizer) reportFailure() {
	if a.telemetry != nil {
		a.telemetry.ReportComponentFailure(
			"atomizer", "degraded",
		)
	}
}

func (a *Atomizer) reportSuccess() {
	if a.telemetry != nil {
		a.telemetry.ReportComponentSuccess("atomizer")
	}
}

// --- Default prompt ---

const defaultAtomizerPrompt = `You are the Atomizer.
Analyze the provided chunk of messages and events.
Extract knowledge atoms with topic segmentation.

Rules:
- 1 chunk may contain multiple topics. Create a separate
  atom for each topic. Do NOT merge different topics.
- Categories: summary, tool_pref, decision, constraint.
- Polarity: positive (task succeeded), negative
  (fail/problem/user dissatisfied), neutral (informational).
- Confidence: 0.0 to 1.0 based on clarity of evidence.
- source_turns: list of turn_ids this atom covers.
- Keep summaries concise but include exact values
  (IPs, ports, paths, versions) verbatim.

Return a single JSON object (no surrounding text):
{
  "atoms": [
    {
      "category": "summary|tool_pref|decision|constraint",
      "summary": "concise description",
      "detail": "optional longer explanation",
      "polarity": "positive|negative|neutral",
      "confidence": 0.9,
      "source_turns": [1, 2, 3]
    }
  ]
}
`
