// PIKA-V3: clarify.go — Go-native HITL clarify tool (D-NEW-2)
// FTS5 pre-check → escalation to manager via Telegram.
// 0 LLM tokens — pure Go fast-path.

package pika

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// ClarifyConfig holds config for the clarify tool.
type ClarifyConfig struct {
	Enabled              bool `json:"enabled"`
	TimeoutMin           int  `json:"timeout_min"`
	MaxStreakBeforeBypass int  `json:"max_streak_before_bypass"`
	PrecheckTimeoutMs    int  `json:"precheck_timeout_ms"`
}

// ClarifySender is the minimal interface for sending
// messages and waiting for replies.
// Implementation lives in main().
type ClarifySender interface {
	SendMessage(
		chatID string, text string,
	) (messageID string, err error)
	WaitForReply(
		ctx context.Context,
		chatID string,
		timeout time.Duration,
	) (string, error)
}

// ClarifyInput holds the parsed tool arguments.
type ClarifyInput struct {
	Question string `json:"question"`
	Context  string `json:"context"`
}

// ClarifyResult holds the tool response.
type ClarifyResult struct {
	Source string `json:"source"` // "memory"|"manager"|"timeout"
	Answer string `json:"answer"`
}

// clarifyState holds per-session state.
type clarifyState struct {
	streak        int
	awaiting      bool
	lastQuestions []string
}

// ClarifyHandler is the HITL clarify tool.
// NOT stateless: holds per-session state via sync.Map.
type ClarifyHandler struct {
	cfg      *ClarifyConfig
	bm       *BotMemory
	sender   ClarifySender
	chatID   string
	sessions sync.Map         // sessionID → *clarifyState
	patterns []*regexp.Regexp // decision/confirmation
}

// NewClarifyHandler creates a new ClarifyHandler.
func NewClarifyHandler(
	cfg *ClarifyConfig,
	bm *BotMemory,
	sender ClarifySender,
	chatID string,
) *ClarifyHandler {
	patternStrings := []string{
		`(?i)делать\s*\?`,
		`(?i)подтверди`,
		`(?i)ок\s*\?`,
		`(?i)продолжа`,
		`(?i)запус(к|тить)`,
		`(?i)\bproceed\b`,
		`(?i)\bconfirm\b`,
		`(?i)\bapprove\b`,
	}
	patterns := make(
		[]*regexp.Regexp, 0, len(patternStrings),
	)
	for _, p := range patternStrings {
		compiled, err := regexp.Compile(p)
		if err != nil {
			log.Printf(
				"WARN pika/clarify: bad pattern %q: %v",
				p, err,
			)
			continue
		}
		patterns = append(patterns, compiled)
	}
	return &ClarifyHandler{
		cfg:      cfg,
		bm:       bm,
		sender:   sender,
		chatID:   chatID,
		patterns: patterns,
	}
}

// Name returns the tool name.
func (ch *ClarifyHandler) Name() string {
	return "clarify"
}

// Description returns the tool description.
func (ch *ClarifyHandler) Description() string {
	return "Ask the user/manager a clarifying question. " +
		"First checks memory for existing answers, " +
		"then escalates to human if needed. " +
		"0 LLM tokens — Go fast-path."
}

// Parameters returns JSON schema for tool arguments.
func (ch *ClarifyHandler) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{
				"type":        "string",
				"description": "The clarifying question",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "Context for the question",
			},
		},
		"required": []string{"question"},
	}
}

// Execute runs the clarify tool.
func (ch *ClarifyHandler) Execute(
	ctx context.Context, args map[string]any,
) *toolshared.ToolResult {
	input, err := parseClarifyArgs(args)
	if err != nil {
		return toolshared.ErrorResult(
			fmt.Sprintf(
				"pika/clarify: invalid args: %s", err,
			),
		)
	}

	sessionID, _ := ctx.Value(
		SessionIDKey{},
	).(string)
	state := ch.getOrCreateState(sessionID)

	// Step 2: Streak check — bypass FTS5
	if state.streak >= ch.cfg.MaxStreakBeforeBypass {
		return ch.escalateToManager(
			ctx, input, state, true,
		)
	}

	// Step 3: Decision/confirmation pattern → escalate
	if ch.isDecisionQuestion(input.Question) {
		return ch.escalateToManager(
			ctx, input, state, false,
		)
	}

	// Step 4: FTS5 pre-check
	precheckTimeout := time.Duration(
		ch.cfg.PrecheckTimeoutMs,
	) * time.Millisecond
	precheckCtx, cancel := context.WithTimeout(
		ctx, precheckTimeout,
	)
	defer cancel()

	hits, ftsErr := ch.queryKnowledgeFTS(
		precheckCtx, input.Question, 3,
	)
	if ftsErr == nil && len(hits) > 0 {
		// Memory hit — return without escalation
		answer := formatFTSResults(hits)
		state.streak++
		state.lastQuestions = append(
			state.lastQuestions, input.Question,
		)
		result := ClarifyResult{
			Source: "memory",
			Answer: answer,
		}
		out, _ := json.Marshal(result)
		return toolshared.SilentResult(string(out))
	}

	// Step 5: FTS miss or timeout → escalate
	return ch.escalateToManager(
		ctx, input, state, false,
	)
}

// escalateToManager sends question via ClarifySender.
func (ch *ClarifyHandler) escalateToManager(
	ctx context.Context,
	input ClarifyInput,
	state *clarifyState,
	includeHistory bool,
) *toolshared.ToolResult {
	msg := formatQuestionForManager(
		input, state, includeHistory,
	)

	_, sendErr := ch.sender.SendMessage(
		ch.chatID, msg,
	)
	if sendErr != nil {
		return toolshared.ErrorResult(
			fmt.Sprintf(
				"pika/clarify: send failed: %s",
				sendErr,
			),
		)
	}

	state.awaiting = true
	timeout := time.Duration(
		ch.cfg.TimeoutMin,
	) * time.Minute

	reply, waitErr := ch.sender.WaitForReply(
		ctx, ch.chatID, timeout,
	)
	state.awaiting = false

	state.streak++
	state.lastQuestions = append(
		state.lastQuestions, input.Question,
	)

	if waitErr != nil {
		result := ClarifyResult{
			Source: "timeout",
			Answer: fmt.Sprintf(
				"Таймаут ожидания ответа (%d мин)",
				ch.cfg.TimeoutMin,
			),
		}
		out, _ := json.Marshal(result)
		return toolshared.SilentResult(string(out))
	}

	result := ClarifyResult{
		Source: "manager",
		Answer: reply,
	}
	out, _ := json.Marshal(result)
	return toolshared.SilentResult(string(out))
}

// ResetStreak resets the clarify streak for a session.
// Called by Router on any non-clarify tool call.
func (ch *ClarifyHandler) ResetStreak(
	sessionID string,
) {
	if val, ok := ch.sessions.Load(sessionID); ok {
		state := val.(*clarifyState)
		state.streak = 0
		state.lastQuestions = nil
	}
}

// IsAwaiting returns true if clarify is waiting for
// a reply from the manager.
func (ch *ClarifyHandler) IsAwaiting(
	sessionID string,
) bool {
	if val, ok := ch.sessions.Load(sessionID); ok {
		state := val.(*clarifyState)
		return state.awaiting
	}
	return false
}

// CleanupSession removes session state from sync.Map.
// Called on session rotation.
func (ch *ClarifyHandler) CleanupSession(
	sessionID string,
) {
	ch.sessions.Delete(sessionID)
}

// getOrCreateState returns or creates per-session state.
func (ch *ClarifyHandler) getOrCreateState(
	sessionID string,
) *clarifyState {
	val, _ := ch.sessions.LoadOrStore(
		sessionID,
		&clarifyState{},
	)
	return val.(*clarifyState)
}

// isDecisionQuestion checks if the question matches
// decision/confirmation patterns.
func (ch *ClarifyHandler) isDecisionQuestion(
	question string,
) bool {
	for _, p := range ch.patterns {
		if p.MatchString(question) {
			return true
		}
	}
	return false
}

// knowledgeHit is a single FTS5 result from knowledge.
type knowledgeHit struct {
	Summary  string
	Category string
}

// queryKnowledgeFTS searches knowledge_atoms via FTS5.
// Read-only. Uses bm.db directly (same pattern as
// MemorySearch in memory_tools.go).
func (ch *ClarifyHandler) queryKnowledgeFTS(
	ctx context.Context,
	query string,
	limit int,
) ([]knowledgeHit, error) {
	fq := buildFTSQuery(escapeFTSSpecial(query))
	if fq == "" {
		return nil, nil
	}
	rows, err := ch.bm.db.QueryContext(ctx,
		`SELECT ka.summary, ka.category
		FROM knowledge_atoms ka
		JOIN knowledge_fts kf ON ka.id = kf.rowid
		WHERE knowledge_fts MATCH ?
		ORDER BY bm25(knowledge_fts) LIMIT ?`,
		fq, limit)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/clarify: fts query: %w", err,
		)
	}
	defer rows.Close()

	var results []knowledgeHit
	for rows.Next() {
		var h knowledgeHit
		if scanErr := rows.Scan(
			&h.Summary, &h.Category,
		); scanErr != nil {
			return nil, fmt.Errorf(
				"pika/clarify: fts scan: %w", scanErr,
			)
		}
		results = append(results, h)
	}
	return results, rows.Err()
}

// escapeFTSSpecial removes FTS5 special characters
// that could break MATCH queries.
func escapeFTSSpecial(s string) string {
	replacer := strings.NewReplacer(
		"*", "",
		"(", "",
		")", "",
		":", "",
		"^", "",
		"+", "",
		"-", "",
		"~", "",
		"\"", "",
		"'", "",
	)
	return replacer.Replace(s)
}

// formatFTSResults formats knowledge hits for the model.
func formatFTSResults(hits []knowledgeHit) string {
	var sb strings.Builder
	sb.WriteString("Найдено в памяти:\n")
	for i, h := range hits {
		sb.WriteString(fmt.Sprintf(
			"%d. [%s] %s\n",
			i+1, h.Category, h.Summary,
		))
	}
	return sb.String()
}

// formatQuestionForManager formats the escalation
// message for the manager.
func formatQuestionForManager(
	input ClarifyInput,
	state *clarifyState,
	includeHistory bool,
) string {
	var sb strings.Builder
	sb.WriteString("\U0001F914 Пика спрашивает:\n\n")
	sb.WriteString(input.Question)
	if input.Context != "" {
		sb.WriteString(
			"\n\nКонтекст: " + input.Context,
		)
	}
	if includeHistory && len(state.lastQuestions) > 0 {
		sb.WriteString(
			"\n\n\U0001F4CB Предыдущие вопросы " +
				"без ответа:\n",
		)
		for _, q := range state.lastQuestions {
			sb.WriteString("• " + q + "\n")
		}
	}
	return sb.String()
}

func parseClarifyArgs(
	args map[string]any,
) (ClarifyInput, error) {
	var input ClarifyInput
	q, ok := args["question"]
	if !ok {
		return input, fmt.Errorf(
			"missing required field: question",
		)
	}
	input.Question, ok = q.(string)
	if !ok {
		return input, fmt.Errorf(
			"question must be a string",
		)
	}
	if input.Question == "" {
		return input, fmt.Errorf(
			"question must not be empty",
		)
	}
	if c, exists := args["context"]; exists {
		if cs, cOK := c.(string); cOK {
			input.Context = cs
		}
	}
	return input, nil
}
