//
// Single point for subagent error diagnosis, correction rule (CR)
// management, and subagent prompt assembly with active CR injection.
//
// Invariant: diagnostics NEVER blocks main loop.
// Error in diagnostics → log warning, continue.
package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// D-148: hardcoded Go constants (not config.json).
// Rebuild container required for any change to diagnostics.go anyway →
// hot-reload is pointless. 5-min refactor to struct+config if needed later.
// ---------------------------------------------------------------------------
const (
	defaultMaxActiveCRs           = 10
	defaultMaxCRTokens            = 500
	defaultVerifyThreshold        = 5
	defaultPromotionMinAgeDays    = 7
	defaultDeactivationMaxAgeDays = 30
)

// validCRComponents — allowed component names for correction rules.
var validCRComponents = map[string]bool{
	"archivist": true,
	"atomizer":  true,
	"reflexor":  true,
	"mcp_guard": true,
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// CorrectionRule is the JSON payload stored in registry.data
// for kind='correction_rule'.
type CorrectionRule struct {
	Component     string  `json:"component"`
	RuleText      string  `json:"rule_text"`
	CreatedAt     string  `json:"created_at"`
	VerifiedCount int     `json:"verified_count"`
	Status        string  `json:"status"` // active | verified | promoted | deactivated
	PromotedAt    *string `json:"promoted_at,omitempty"`
}

// DiagnosisResult is returned by Diagnose.
type DiagnosisResult struct {
	TraceID        string
	Component      string        // which subagent errored
	ErrorKind      string        // "error" | "timeout"
	RootSpan       *TraceSpanRow // the span with error/timeout
	RelatedAtomIDs []string      // atom_ids used in this trace's prompts
	SuggestedCR    *string       // non-nil if pattern ≥2 similar errors in 7d
}

// CRReviewAction is returned by ReviewCRs for the Reflector weekly pipeline.
type CRReviewAction struct {
	RegistryID int64
	Key        string
	Action     string // "promote" | "deactivate"
	Rule       CorrectionRule
}

// ---------------------------------------------------------------------------
// DiagnosticsEngine
// ---------------------------------------------------------------------------

// DiagnosticsEngine manages subagent error diagnosis, correction rules,
// and subagent prompt assembly with CR injection.
type DiagnosticsEngine struct {
	mem         *BotMemory
	tgSender    TelegramSender    // optional (nil → skip notifications)
	promptPaths map[string]string // component → prompt file path on disk
}

// NewDiagnosticsEngine creates a DiagnosticsEngine.
// tgSender may be nil (notifications will be skipped).
// promptPaths maps component name ("archivist", etc.) to its prompt file path.
func NewDiagnosticsEngine(
	mem *BotMemory,
	tgSender TelegramSender,
	promptPaths map[string]string,
) *DiagnosticsEngine {
	return &DiagnosticsEngine{
		mem:         mem,
		tgSender:    tgSender,
		promptPaths: promptPaths,
	}
}

// ---------------------------------------------------------------------------
// Diagnose — error attribution (read-only)
// ---------------------------------------------------------------------------

// Diagnose attributes a subagent error by trace_id.
// Read-only with respect to trace_spans and atom_usage.
// Returns empty result on any internal error (invariant: never blocks).
func (d *DiagnosticsEngine) Diagnose(ctx context.Context, traceID string) DiagnosisResult {
	result := DiagnosisResult{TraceID: traceID}

	// 1. Find first error/timeout span for this trace.
	row := d.mem.db.QueryRowContext(ctx,
		`SELECT span_id, COALESCE(parent_span_id,''), trace_id,
		        COALESCE(session_id,''), COALESCE(turn_id,0),
		        component, operation, started_at, status
		 FROM trace_spans
		 WHERE trace_id = ? AND status IN ('error','timeout')
		 ORDER BY started_at ASC
		 LIMIT 1`, traceID)

	var span TraceSpanRow
	var parentSpanID, sessionID string
	var turnID int
	err := row.Scan(
		&span.SpanID, &parentSpanID, &span.TraceID,
		&sessionID, &turnID,
		&span.Component, &span.Operation, &span.StartedAt, &span.Status,
	)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("pika/diagnostics: diagnose scan: %v", err)
		}
		return result // no error spans or query failed
	}
	span.ParentSpanID = parentSpanID
	span.SessionID = sessionID
	if turnID != 0 {
		span.TurnID = &turnID
	}

	result.Component = span.Component
	result.ErrorKind = span.Status
	result.RootSpan = &span

	// 2. Collect atom_ids used in this trace's prompts.
	atomRows, err := d.mem.db.QueryContext(ctx,
		`SELECT DISTINCT atom_id FROM atom_usage WHERE trace_id = ?`, traceID)
	if err == nil {
		defer atomRows.Close()
		for atomRows.Next() {
			var atomID string
			if atomRows.Scan(&atomID) == nil {
				result.RelatedAtomIDs = append(result.RelatedAtomIDs, atomID)
			}
		}
		if err := atomRows.Err(); err != nil {
			log.Printf("pika/diagnostics: atom rows: %v", err)
		}
	}

	// 3. Pattern check: ≥2 similar errors in last 7 days → suggest CR.
	var similarCount int
	err = d.mem.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM trace_spans
		 WHERE component = ? AND status IN ('error','timeout')
		   AND started_at > datetime('now','-7 days')
		   AND trace_id != ?`,
		result.Component, traceID).Scan(&similarCount)
	if err != nil {
		log.Printf("pika/diagnostics: similar count: %v", err)
		return result
	}

	if similarCount >= 2 {
		suggestion := fmt.Sprintf(
			"При [%s] для [%s]: проверить входные данные и добавить retry с hint",
			result.ErrorKind, result.Component)
		result.SuggestedCR = &suggestion
	}

	return result
}

// ---------------------------------------------------------------------------
// CreateCR — create correction rule
// ---------------------------------------------------------------------------

// CreateCR inserts a new correction rule into registry.
// Validates component, notifies via Telegram (D-149), alerts on threshold.
func (d *DiagnosticsEngine) CreateCR(ctx context.Context, component, ruleText string) error {
	if !validCRComponents[component] {
		return fmt.Errorf("pika/diagnostics: invalid component %q", component)
	}

	cr := CorrectionRule{
		Component:     component,
		RuleText:      ruleText,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		VerifiedCount: 0,
		Status:        "active",
	}
	data, err := json.Marshal(cr)
	if err != nil {
		return fmt.Errorf("pika/diagnostics: marshal CR: %w", err)
	}

	key := fmt.Sprintf("cr-%s-%d", component, time.Now().UnixNano())

	_, err = d.mem.db.ExecContext(ctx,
		`INSERT INTO registry (kind, key, summary, data, verified, tags)
		 VALUES ('correction_rule', ?, ?, ?, 0, ?)`,
		key, ruleText, string(data),
		fmt.Sprintf(`["%s","correction_rule"]`, component))
	if err != nil {
		return fmt.Errorf("pika/diagnostics: insert CR: %w", err)
	}

	log.Printf("pika/diagnostics: CR created for %s: %s", component, ruleText)

	// D-149: Notify user via Telegram.
	d.notifyCRCreated(ctx, component, ruleText)

	// D-149: Threshold alert (≥3 active CRs → prompt needs review).
	d.checkCRThreshold(ctx, component)

	return nil
}

func (d *DiagnosticsEngine) notifyCRCreated(ctx context.Context, component, ruleText string) {
	if d.tgSender == nil {
		return
	}
	msg := fmt.Sprintf("⚠️ [%s] — создано правило: %s", component, ruleText)
	if _, err := d.tgSender.SendMessage(ctx, msg); err != nil {
		log.Printf("pika/diagnostics: notify CR: %v", err)
	}
}

func (d *DiagnosticsEngine) checkCRThreshold(ctx context.Context, component string) {
	if d.tgSender == nil {
		return
	}
	var count int
	err := d.mem.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM registry
		 WHERE kind = 'correction_rule'
		   AND json_extract(data, '$.component') = ?
		   AND json_extract(data, '$.status') IN ('active','verified')`,
		component).Scan(&count)
	if err != nil {
		log.Printf("pika/diagnostics: threshold query: %v", err)
		return
	}
	if count >= 3 {
		msg := fmt.Sprintf(
			"🔴 [%s]: %d активных правил. Промт нуждается в ревью.",
			component, count)
		if _, err := d.tgSender.SendMessage(ctx, msg); err != nil {
			log.Printf("pika/diagnostics: threshold alert: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// BuildSubagentPrompt — hot-reload base prompt + append active CRs
// ---------------------------------------------------------------------------

// BuildSubagentPrompt loads the base prompt file for a component and
// appends active correction rules. Returns base prompt only if 0 CRs.
// Error if prompt file missing (not panic — caller handles fallback).
func (d *DiagnosticsEngine) BuildSubagentPrompt(ctx context.Context, component string) (string, error) {
	// 1. Resolve prompt path.
	path, ok := d.promptPaths[component]
	if !ok || path == "" {
		return "", fmt.Errorf("pika/diagnostics: no prompt path for %q", component)
	}

	// 2. Hot-reload: read file on every call (D-90 pattern).
	baseData, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("pika/diagnostics: read prompt %s: %w", path, err)
	}
	basePrompt := string(baseData)

	// 3. Query active/verified CRs for this component.
	rows, err := d.mem.db.QueryContext(ctx,
		`SELECT data FROM registry
		 WHERE kind = 'correction_rule'
		   AND json_extract(data, '$.component') = ?
		   AND json_extract(data, '$.status') IN ('active','verified')
		 ORDER BY id DESC
		 LIMIT ?`,
		component, defaultMaxActiveCRs)
	if err != nil {
		log.Printf("pika/diagnostics: query CRs for %s: %v", component, err)
		return basePrompt, nil // graceful: base prompt only
	}
	defer rows.Close()

	// 4. Collect rules within token budget.
	var rules []string
	totalTokens := 0
	for rows.Next() {
		var dataStr string
		if err := rows.Scan(&dataStr); err != nil {
			continue
		}
		var cr CorrectionRule
		if err := json.Unmarshal([]byte(dataStr), &cr); err != nil {
			continue
		}
		ruleTokens := estimateCRTokens(cr.RuleText)
		if totalTokens+ruleTokens > defaultMaxCRTokens {
			break // oldest trimmed (ordered DESC → newest first)
		}
		rules = append(rules, cr.RuleText)
		totalTokens += ruleTokens
	}

	// 5. No CRs → base prompt only (regression-safe: 0 diff with current).
	if len(rules) == 0 {
		return basePrompt, nil
	}

	// 6. Append CR block.
	var sb strings.Builder
	sb.WriteString(basePrompt)
	sb.WriteString("\n\n## CORRECTION RULES (active)\n")
	for _, r := range rules {
		sb.WriteString("- ")
		sb.WriteString(r)
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// IncrementVerified — called after successful subagent call
// ---------------------------------------------------------------------------

// IncrementVerified increments verified_count for all active CRs of a
// component. When count reaches threshold → status becomes "verified".
func (d *DiagnosticsEngine) IncrementVerified(ctx context.Context, component string) error {
	rows, err := d.mem.db.QueryContext(ctx,
		`SELECT id, data FROM registry
		 WHERE kind = 'correction_rule'
		   AND json_extract(data, '$.component') = ?
		   AND json_extract(data, '$.status') = 'active'`,
		component)
	if err != nil {
		return fmt.Errorf("pika/diagnostics: query active CRs: %w", err)
	}
	defer rows.Close()

	type pendingUpdate struct {
		id   int64
		data string
	}
	var updates []pendingUpdate

	for rows.Next() {
		var id int64
		var dataStr string
		if err := rows.Scan(&id, &dataStr); err != nil {
			continue
		}
		var cr CorrectionRule
		if err := json.Unmarshal([]byte(dataStr), &cr); err != nil {
			continue
		}
		cr.VerifiedCount++
		if cr.VerifiedCount >= defaultVerifyThreshold {
			cr.Status = "verified"
		}
		newData, err := json.Marshal(cr)
		if err != nil {
			continue
		}
		updates = append(updates, pendingUpdate{id: id, data: string(newData)})
	}

	for _, u := range updates {
		if _, err := d.mem.db.ExecContext(ctx,
			`UPDATE registry SET data = ? WHERE id = ?`,
			u.data, u.id); err != nil {
			log.Printf("pika/diagnostics: update CR %d: %v", u.id, err)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// ReviewCRs — called by Reflector weekly pipeline (6th task)
// ---------------------------------------------------------------------------

// ReviewCRs reviews all active/verified CRs and returns actions taken:
//   - promote: verified + age ≥ 7d → status "promoted"
//   - deactivate: active + age ≥ 30d + count < threshold → "deactivated"
//
// Go (reflector.go) calls this; updates are applied inside.
func (d *DiagnosticsEngine) ReviewCRs(ctx context.Context) []CRReviewAction {
	var actions []CRReviewAction

	rows, err := d.mem.db.QueryContext(ctx,
		`SELECT id, key, data FROM registry
		 WHERE kind = 'correction_rule'
		   AND json_extract(data, '$.status') IN ('active','verified')`)
	if err != nil {
		log.Printf("pika/diagnostics: review query: %v", err)
		return actions
	}
	defer rows.Close()

	type candidate struct {
		id  int64
		key string
		cr  CorrectionRule
	}
	var candidates []candidate

	for rows.Next() {
		var id int64
		var key, dataStr string
		if err := rows.Scan(&id, &key, &dataStr); err != nil {
			continue
		}
		var cr CorrectionRule
		if err := json.Unmarshal([]byte(dataStr), &cr); err != nil {
			continue
		}
		candidates = append(candidates, candidate{id: id, key: key, cr: cr})
	}
	if err := rows.Err(); err != nil {
		log.Printf("pika/diagnostics: review rows: %v", err)
		return actions
	}

	now := time.Now().UTC()
	for _, c := range candidates {
		createdAt, err := time.Parse(time.RFC3339, c.cr.CreatedAt)
		if err != nil {
			log.Printf("pika/diagnostics: parse created_at %s: %v", c.key, err)
			continue
		}
		ageDays := int(now.Sub(createdAt).Hours() / 24)

		switch {
		// Promote: verified + old enough → promoted.
		case c.cr.Status == "verified" && ageDays >= defaultPromotionMinAgeDays:
			c.cr.Status = "promoted"
			ts := now.Format(time.RFC3339)
			c.cr.PromotedAt = &ts
			newData, _ := json.Marshal(c.cr)
			if _, err := d.mem.db.ExecContext(ctx,
				`UPDATE registry SET data = ? WHERE id = ?`,
				string(newData), c.id); err != nil {
				log.Printf("pika/diagnostics: promote %s: %v", c.key, err)
				continue
			}
			actions = append(actions, CRReviewAction{
				RegistryID: c.id, Key: c.key, Action: "promote", Rule: c.cr,
			})

		// Deactivate: active + stale + insufficient verification.
		case c.cr.Status == "active" &&
			ageDays >= defaultDeactivationMaxAgeDays &&
			c.cr.VerifiedCount < defaultVerifyThreshold:
			c.cr.Status = "deactivated"
			newData, _ := json.Marshal(c.cr)
			if _, err := d.mem.db.ExecContext(ctx,
				`UPDATE registry SET data = ? WHERE id = ?`,
				string(newData), c.id); err != nil {
				log.Printf("pika/diagnostics: deactivate %s: %v", c.key, err)
				continue
			}
			actions = append(actions, CRReviewAction{
				RegistryID: c.id, Key: c.key, Action: "deactivate", Rule: c.cr,
			})
		}
	}

	return actions
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// estimateCRTokens approximates token count (~4 chars/token).
// Sufficient precision for the 500-token CR budget guard.
func estimateCRTokens(text string) int {
	return (len(text) + 3) / 4
}
