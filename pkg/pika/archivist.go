// PIKA-V3: Archivist — agentic cheap LLM session for building
// FOCUS + MEMORY BRIEF. Single tool: search_context (read-only).
// Called from PikaContextManager.BuildSystemPrompt by threshold
// D-107. Decision: D-55, D-65, D-109.

package pika

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// ArchivistConfig holds Archivist-specific configuration.
type ArchivistConfig struct {
	PromptFile                string
	MaxToolCalls              int
	BuildPromptTimeoutMs      int
	MemoryBriefSoftLimit      int
	MemoryBriefHardLimit      int
	CompressProtectedSections []string
	MaxRetriesValidateBrief   int
	ReasoningGuidedRetrieval  bool
	ReasoningDriftOverlapMin  float64
	RotationLastN             int
	DefaultLastN              int
	// Волна 124: капы дельты входа в токенах (~4 символа/токен).
	DeltaMaxTokens         int
	RotationDeltaMaxTokens int
	Model                  string
}

// DefaultArchivistConfig returns sensible defaults (D-107).
func DefaultArchivistConfig() ArchivistConfig {
	return ArchivistConfig{
		PromptFile: "/workspace/prompts/archivist_build.md",
		// Волна 86: веер из 3-5 параллельных запросов (мульти-запрос).
		// Волна 87: 16 — два полных веера + запас; превышение мягкое.
		MaxToolCalls:         16,
		BuildPromptTimeoutMs: 30000,
		MemoryBriefSoftLimit: 5000,
		MemoryBriefHardLimit: 6000,
		CompressProtectedSections: []string{
			"AVOID", "CONSTRAINTS",
		},
		MaxRetriesValidateBrief:  3,
		ReasoningGuidedRetrieval: true,
		ReasoningDriftOverlapMin: 0.2,
		RotationLastN:            10,
		DefaultLastN:             5,
		DeltaMaxTokens:           4000,
		RotationDeltaMaxTokens:   12000,
		Model:                    "background",
	}
}

// searchContextToolDef is the tool definition exposed to the LLM.
var searchContextToolDef = providers.ToolDefinition{
	Type: "function",
	Function: providers.ToolFunctionDefinition{
		Name: "search_context",
		Description: "Search bot_memory.db for relevant context. " +
			"Use polarity='negative' first for AVOID items.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"aspects": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
					"description": "Sources: knowledge, " +
						"messages, reasoning, archive",
				},
				"polarity": map[string]any{
					"type": "string",
					"enum": []string{
						"negative", "positive", "all",
					},
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max results (default 20)",
				},
			},
			"required": []string{"query"},
		},
	},
}

// SearchContextParams for the search_context tool.
type SearchContextParams struct {
	Query    string   `json:"query"`
	Aspects  []string `json:"aspects,omitempty"`
	Polarity string   `json:"polarity,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// SearchContextResult is the Go fan-out result.
type SearchContextResult struct {
	Knowledge         []KnowledgeHit      `json:"knowledge,omitempty"`
	Messages          []MessageHit        `json:"messages,omitempty"`
	ReasoningKeywords []string            `json:"reasoning_keywords,omitempty"`
	CorrelatedTools   []CorrelatedToolRow `json:"correlated_tools,omitempty"`
	ToolPrefs         []KnowledgeHit      `json:"tool_prefs,omitempty"`
}

// KnowledgeHit is a single knowledge atom search result.
type KnowledgeHit struct {
	Category   string  `json:"category"`
	Summary    string  `json:"summary"`
	Polarity   string  `json:"polarity"`
	Confidence float64 `json:"confidence"`
}

// MessageHit is a single message search result.
type MessageHit struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Turn    int    `json:"turn"`
}

// archivistLLMOutput is the structured JSON from the LLM.
type archivistLLMOutput struct {
	Focus             Focus       `json:"focus"`
	MemoryBrief       MemoryBrief `json:"memory_brief"`
	RecommendedTools  []string    `json:"recommended_tools"`
	RecommendedSkills []string    `json:"recommended_skills"`
}

// Archivist implements ArchivistCaller via an agentic LLM session.
// Thread-safe: all public methods use mu for cache access.
type Archivist struct {
	mem      *BotMemory
	provider providers.LLMProvider
	trail    *Trail
	meta     *Meta
	cfg      ArchivistConfig
	diag     *DiagnosticsEngine

	mu          sync.RWMutex
	lastResult  *ArchivistResult
	cachedFocus *Focus
	// Волна 97 (бой 21 авг 11:17): штампы кэша — бриф валиден только
	// для той же сессии и того же scope памяти. Иначе /memory all
	// молча получал протухший бриф от другого чата (0 LLM-вызовов).
	builtForSessionKey string
	builtForScope      string
	// Волна 124: id последнего сообщения, попавшего во вход прошлой
	// сборки — дельта следующего входа считается от него.
	builtAfterMsgID int64

	// PIKA-V3: transient tracking for atom_usage (TZ-v2-9a F-2)
	currentSessionKey string
	currentSpanID     string
	currentTraceID    string // Волна 92: дочерние спаны search_context
}

// NewArchivist creates a new Archivist. All dependencies injected.
func NewArchivist(
	mem *BotMemory,
	provider providers.LLMProvider,
	trail *Trail,
	meta *Meta,
	cfg ArchivistConfig,
) *Archivist {
	if cfg.MaxToolCalls <= 0 {
		cfg.MaxToolCalls = 4
	}
	if cfg.BuildPromptTimeoutMs <= 0 {
		cfg.BuildPromptTimeoutMs = 30000
	}
	if cfg.MemoryBriefSoftLimit <= 0 {
		cfg.MemoryBriefSoftLimit = 5000
	}
	if cfg.MemoryBriefHardLimit <= 0 {
		cfg.MemoryBriefHardLimit = 6000
	}
	if cfg.MaxRetriesValidateBrief <= 0 {
		cfg.MaxRetriesValidateBrief = 3
	}
	if cfg.DefaultLastN <= 0 {
		cfg.DefaultLastN = 5
	}
	if cfg.RotationLastN <= 0 {
		cfg.RotationLastN = 10
	}
	return &Archivist{
		mem:      mem,
		provider: provider,
		trail:    trail,
		meta:     meta,
		cfg:      cfg,
	}
}

// InvalidateBrief clears cached brief and focus.
func (a *Archivist) InvalidateBrief() {
	a.mu.Lock()
	a.lastResult = nil
	a.mu.Unlock()
}

// GetCachedBrief returns the cached brief text.
func (a *Archivist) GetCachedBrief() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.lastResult != nil {
		return a.lastResult.BriefText
	}
	return ""
}

// SetDiagnostics injects the diagnostics engine (post-construction wiring).
func (a *Archivist) SetDiagnostics(d *DiagnosticsEngine) {
	a.diag = d
}

// GetCachedFocus returns the cached Focus (nil if none).
func (a *Archivist) GetCachedFocus() *Focus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cachedFocus
}

// BuildPrompt implements ArchivistCaller. Runs an agentic LLM
// session to produce FOCUS + MEMORY BRIEF. Returns cached result
// if available.
func (a *Archivist) BuildPrompt(
	ctx context.Context,
	input ArchivistInput,
) (_ *ArchivistResult, retErr error) {
	// Fast path: return cached result (~80% of calls)
	// Волна 97 (бой 21 авг 11:17): кэш валиден только для ТОЙ ЖЕ сессии
	// и ТОГО ЖЕ scope памяти — смена любого роняет кэш на этой сборке.
	a.mu.RLock()
	cached := a.lastResult
	builtForSession := a.builtForSessionKey
	builtForScope := a.builtForScope
	a.mu.RUnlock()
	if cached != nil &&
		builtForSession == input.SessionKey &&
		builtForScope == a.mem.GetMemoryScope(ctx, input.SessionKey) {
		return cached, nil
	}

	// PIKA-V3: Trace span (TZ-v2-9a block 3)
	spanIDarchivist := fmt.Sprintf("span_archivist_%d", time.Now().UnixNano())
	traceIDarchivist := fmt.Sprintf("trace_archivist_%d", time.Now().UnixNano())
	a.currentSessionKey = input.SessionKey
	a.currentSpanID = spanIDarchivist
	a.currentTraceID = traceIDarchivist
	a.mu.Lock()
	a.builtForSessionKey = input.SessionKey
	a.builtForScope = a.mem.GetMemoryScope(ctx, input.SessionKey)
	a.mu.Unlock()
	_ = a.mem.InsertSpan(ctx, TraceSpanRow{
		SpanID: spanIDarchivist, TraceID: traceIDarchivist, Component: "archivist", Operation: "build_prompt",
		// D-AUDIT-63: DDL CHECK не знает "running" — пишем разрешённый статус.
		StartedAt: time.Now(), Status: "ok",
	})
	// Волна 92: наполняется после сериализации брифа, читается в defer.
	var briefPreview string
	defer func() {
		// D-AUDIT-67: error-статус спана + петля диагностики.
		// WithoutCancel: у archivist ctx уже с таймаутом, а defer идёт после cancel.
		dctx := context.WithoutCancel(ctx)
		st, errType, errMsg := "ok", "", ""
		if retErr != nil {
			st, errType, errMsg = "error", "agentic", retErr.Error()
		}
		_ = a.mem.CompleteSpan(dctx, spanIDarchivist, st, nil, errType, errMsg)
		// Волна 92: превью входа/выхода в спан — цепочку читаем данными.
		_ = a.mem.SetSpanPreviews(
			dctx, spanIDarchivist,
			truncateStr(input.Message, 300), truncateStr(briefPreview, 4000),
		)
		if a.diag != nil {
			if retErr != nil {
				if res := a.diag.Diagnose(dctx, traceIDarchivist); res.SuggestedCR != nil {
					_ = a.diag.CreateCR(dctx, "archivist", *res.SuggestedCR)
				}
			} else {
				_ = a.diag.IncrementVerified(dctx, "archivist")
			}
		}
	}()
	// Apply timeout
	tMs := a.cfg.BuildPromptTimeoutMs
	ctx, cancel := context.WithTimeout(
		ctx, time.Duration(tMs)*time.Millisecond,
	)
	defer cancel()

	// Load prompt file (hot-reload, 0 restart)
	promptText, err := a.loadPromptFile()
	if err != nil {
		return nil, fmt.Errorf(
			"pika/archivist: load prompt: %w", err,
		)
	}

	// Build user message with all input context
	// Волна 124: тёплый вход — предыдущий бриф + дельта сообщений чата
	// с его сборки. Ротация чат не меняет: хвост умершей сессии доезжает
	// без поиска (handoff из живого контекста, а не из отставших атомов).
	a.mu.RLock()
	prevBrief := ""
	if a.lastResult != nil {
		prevBrief = a.lastResult.BriefText
	}
	watermark := a.builtAfterMsgID
	a.mu.RUnlock()
	deltaCap := a.cfg.DeltaMaxTokens
	if input.IsRotation && a.cfg.RotationDeltaMaxTokens > deltaCap {
		deltaCap = a.cfg.RotationDeltaMaxTokens
	}
	input.PreviousBrief = prevBrief
	input.WorkSinceBrief = a.mem.GetWorkSince(
		ctx, input.SessionKey, watermark, deltaCap,
	)
	userMsg := a.buildUserMessage(ctx, input)

	// Run agentic loop
	output, err := a.runAgenticLoop(
		ctx, promptText, userMsg, input.IsRotation,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/archivist: agentic loop: %w", err,
		)
	}

	// Serialize brief
	// Волна 124 (бой 28 сен): промптное «без дублей» не сработало
	// (×3 подряд в AVOID) — дедупит Go, детерминированно.
	dedupeMemoryBrief(&output.MemoryBrief)
	briefText := SerializeMemoryBrief(output.MemoryBrief)
	briefPreview = briefText

	// Size control (F10-5): rough ~4 chars/token
	if estimateTokens(briefText) > a.cfg.MemoryBriefSoftLimit {
		for i := 0; i < a.cfg.MaxRetriesValidateBrief; i++ {
			compressed, cErr := a.compressBrief(
				ctx, promptText, output, briefText,
			)
			if cErr != nil {
				break
			}
			output.MemoryBrief = compressed
			dedupeMemoryBrief(&output.MemoryBrief)
			briefText = SerializeMemoryBrief(compressed)
			if estimateTokens(briefText) <=
				a.cfg.MemoryBriefSoftLimit {
				break
			}
		}
	}
	// hard_limit is a metric only — insert as-is (D-107)

	// Cache result
	result := &ArchivistResult{
		Focus:             output.Focus,
		Brief:             output.MemoryBrief,
		BriefText:         briefText,
		RecommendedTools:  output.RecommendedTools,
		RecommendedSkills: output.RecommendedSkills,
	}
	a.mu.Lock()
	a.lastResult = result
	a.builtAfterMsgID = a.mem.GetMaxMessageID(ctx, input.SessionKey)
	a.mu.Unlock()

	return result, nil
}

// LastResult returns the last cached ArchivistResult, or nil.
func (a *Archivist) LastResult() *ArchivistResult {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastResult
}

// loadPromptFile reads the archivist prompt from disk.
func (a *Archivist) loadPromptFile() (string, error) {
	if a.diag != nil {
		prompt, err := a.diag.BuildSubagentPrompt(context.Background(), "archivist")
		if err == nil {
			return prompt, nil
		}
		// fallback to default prompt
	}
	path := a.cfg.PromptFile
	if path == "" {
		return defaultArchivistPrompt, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- config path, D-90
	if err != nil {
		if os.IsNotExist(err) {
			return defaultArchivistPrompt, nil
		}
		return "", fmt.Errorf(
			"pika/archivist: read %s: %w", path, err,
		)
	}
	return string(data), nil
}

// buildUserMessage constructs the user message for the LLM.
func (a *Archivist) buildUserMessage(
	_ context.Context,
	input ArchivistInput,
) string {
	var sb strings.Builder

	sb.WriteString("## Current message\n")
	if input.Message != "" {
		sb.WriteString(input.Message)
	} else {
		sb.WriteString("(no message)")
	}
	sb.WriteString("\n\n")

	if a.trail != nil {
		trailText := a.trail.Serialize()
		if trailText != "" {
			sb.WriteString("## TRAIL\n")
			sb.WriteString(trailText)
			sb.WriteString("\n\n")
		}
	}

	if a.meta != nil {
		metaText := a.meta.Serialize()
		if metaText != "" {
			sb.WriteString("## META\n")
			sb.WriteString(metaText)
			sb.WriteString("\n\n")
		}
	}

	if input.PreviousBrief != "" {
		sb.WriteString("## PREVIOUS_BRIEF\n")
		sb.WriteString(input.PreviousBrief)
		sb.WriteString("\n\n")
	}
	if input.WorkSinceBrief != "" {
		sb.WriteString("## WORK_SINCE_BRIEF\n")
		sb.WriteString(input.WorkSinceBrief)
		sb.WriteString("\n\n")
	}
	sb.WriteString("## Config\n")
	fmt.Fprintf(&sb,
		"reasoning_guided_retrieval: %v\n",
		a.cfg.ReasoningGuidedRetrieval)
	fmt.Fprintf(&sb,
		"memory_brief_soft_limit: %d\n",
		a.cfg.MemoryBriefSoftLimit)
	fmt.Fprintf(&sb,
		"max_tool_calls: %d\n", a.cfg.MaxToolCalls)
	fmt.Fprintf(&sb,
		"is_rotation: %v\n", input.IsRotation)
	// D-AUDIT-60: честный вход — сессия, план, лимиты рекомендаций
	fmt.Fprintf(&sb, "session_id: %s\n", input.SessionKey)
	if input.ActivePlan != "" {
		fmt.Fprintf(&sb, "active_plan: %s\n", input.ActivePlan)
	}
	fmt.Fprintf(&sb,
		"max_recommended_tools: %d\n", input.MaxRecommendedTools)
	fmt.Fprintf(&sb,
		"max_recommended_skills: %d\n", input.MaxRecommendedSkills)
	// PIKA-V3: inject tool/skill catalogs for LLM selection
	if len(input.ToolCatalog) > 0 {
		fmt.Fprintf(&sb,
			"available_tools: %v\n",
			input.ToolCatalog)
	}
	if len(input.SkillCatalog) > 0 {
		fmt.Fprintf(&sb,
			"available_skills: %v\n",
			input.SkillCatalog)
	}

	return sb.String()
}

// sessionKeyForTelemetry — снапшот текущей сессии для телеметрии (волна 82).
func (a *Archivist) sessionKeyForTelemetry() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentSessionKey
}

// runAgenticLoop: prompt -> LLM -> tool_calls -> execute -> LLM -> JSON.
func (a *Archivist) runAgenticLoop(
	ctx context.Context,
	systemPrompt, userMsg string,
	isRotation bool,
) (*archivistLLMOutput, error) {
	msgs := []providers.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	}
	tools := []providers.ToolDefinition{searchContextToolDef}
	model := a.cfg.Model
	toolCallCount := 0
	// Волна 90 (бой 20 авг): промт ссылается на response_schema API,
	// но схема в Chat не передаётся — финал может прийти прозой
	// («no JSON in response»). Даём до 2 nudge-попыток, прежде чем
	// ронять бриф.
	noJSONRetries := 0

	for {
		// D-AUDIT-82/волна 82: телеметрия спутника в request_log
		// (успех и ошибка — раньше Архивариус не писался вообще).
		llmStart := time.Now()
		resp, err := a.provider.Chat(
			ctx, msgs, tools, model, nil,
		)
		if err != nil {
			RecordSatelliteLLMFailure(
				ctx, a.mem, "archivarius", "build_prompt",
				a.sessionKeyForTelemetry(), model, err, llmStart,
			)
			return nil, fmt.Errorf(
				"pika/archivist: LLM call: %w", err,
			)
		}
		RecordSatelliteLLM(
			ctx, a.mem, "archivarius", "build_prompt",
			a.sessionKeyForTelemetry(), model, resp, llmStart,
		)

		// No tool calls -> parse final JSON response
		if len(resp.ToolCalls) == 0 {
			out, pErr := parseArchivistOutput(resp.Content)
			if pErr == nil {
				return out, nil
			}
			noJSONRetries++
			if noJSONRetries > 2 {
				return nil, fmt.Errorf(
					"pika/archivist: no JSON after %d retries: %w",
					noJSONRetries-1, pErr,
				)
			}
			msgs = append(msgs,
				providers.Message{Role: "assistant", Content: resp.Content},
				providers.Message{
					Role: "user",
					Content: "Ответь ТОЛЬКО одним JSON-объектом по схеме " +
						"из системного промта (focus, memory_brief, " +
						"recommended_tools, recommended_skills). " +
						"Без текста до и после.",
				},
			)
			continue
		}
		// Волна 87: модель проигнорировала tools=nil после мягкого
		// потолка — не крутим цикл бесконечно.
		if tools == nil {
			return nil, fmt.Errorf(
				"pika/archivist: soft cap: model kept calling tools",
			)
		}

		// Волна 83 (бой 20 авг): нормализация как в main/toolloop.
		// Провайдер парсит tool calls в internal top-level поля
		// (json:"-"), Function остаётся nil → на провод уходило
		// assistant-сообщение без function payload и Gemini отклонял
		// второй заход: 400 «no valid function calls». NormalizeToolCall
		// восстанавливает Function и ThoughtSignature (обязательна для
		// gemini-2.5 thinking).
		normalizedCalls := make([]providers.ToolCall, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			normalizedCalls = append(normalizedCalls, providers.NormalizeToolCall(tc))
		}

		// Add assistant message with tool calls
		msgs = append(msgs, providers.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: normalizedCalls,
		})

		// Волна 87 (бой 20 авг): мягкий потолок — превышение лимита
		// больше не роняет весь BuildPrompt (бриф исчезал из системного
		// промта, main-модель отвечала вслепую). Недопущенным вызовам
		// отвечаем notice, финальная итерация идёт без инструментов.
		capExceeded := false
		for _, tc := range normalizedCalls {
			toolCallCount++
			if toolCallCount > a.cfg.MaxToolCalls {
				capExceeded = true
			}

			// Волна 83: имя из нормализованного вызова (Function мог
			// быть nil в сыром ответе — fnName терялся).
			fnName := tc.Name
			fnArgs := ""
			if tc.Function != nil {
				fnArgs = tc.Function.Arguments
			}

			var toolResult string
			if capExceeded {
				toolResult = `{"notice":"tool call limit reached; use collected results and produce final JSON"}`
			} else if fnName == "search_context" {
				toolResult = a.handleSearchContext(
					ctx, fnArgs, isRotation,
				)
			} else {
				toolResult = fmt.Sprintf(
					`{"error":"unknown tool: %s"}`,
					fnName,
				)
			}

			msgs = append(msgs, providers.Message{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID,
			})
		}
		if capExceeded {
			// Следующая (финальная) итерация — без инструментов:
			// модель обязана выдать JSON из собранного.
			tools = nil
		}
	}
}

// handleSearchContext parses args and executes Go fan-out.
func (a *Archivist) handleSearchContext(
	ctx context.Context,
	argsJSON string,
	isRotation bool,
) string {
	var params SearchContextParams
	if err := json.Unmarshal(
		[]byte(argsJSON), &params,
	); err != nil {
		return `{"error":"invalid params"}`
	}

	// Волна 92: дочерний спан на каждый search_context — запрос, хиты
	// и первые сниппеты видны в trace_spans.
	childID := fmt.Sprintf("span_search_%d", time.Now().UnixNano())
	_ = a.mem.InsertSpan(ctx, TraceSpanRow{
		SpanID: childID, ParentSpanID: a.currentSpanID,
		TraceID: a.currentTraceID, Component: "archivist",
		Operation: "search_context", StartedAt: time.Now(), Status: "ok",
	})

	result, err := a.executeSearchContext(
		ctx, params, isRotation,
	)
	if err != nil {
		_ = a.mem.CompleteSpan(ctx, childID, "error", nil, "search", err.Error())
		return fmt.Sprintf(
			`{"error":"%s"}`, err.Error(),
		)
	}

	snips := ""
	for i, m := range result.Messages {
		if i >= 2 {
			break
		}
		snips += " | " + truncateStr(m.Content, 80)
	}
	_ = a.mem.SetSpanPreviews(ctx, childID,
		truncateStr(params.Query, 200),
		fmt.Sprintf("knowledge=%d messages=%d tools=%d prefs=%d%s",
			len(result.Knowledge), len(result.Messages),
			len(result.CorrelatedTools), len(result.ToolPrefs), snips),
	)
	_ = a.mem.CompleteSpan(ctx, childID, "ok", nil, "", "")

	// Волна 88 (бой 20 авг): телеметрия фан-аута — сколько хитов дал
	// каждый аспект. Видно в /logs; без этого было неотличимо
	// «модель не искала» от «база не нашла».
	log.Printf(
		"INFO pika/archivist: search_context q=%q knowledge=%d messages=%d tools=%d prefs=%d",
		params.Query, len(result.Knowledge), len(result.Messages),
		len(result.CorrelatedTools), len(result.ToolPrefs),
	)
	data, _ := json.Marshal(result)
	return string(data)
}

// executeSearchContext — wave 122 (slice D): convergence on the
// search_memory engine (SSOT retrieval). Aspects knowledge/messages/
// archive are served by ONE engine call (same layers, RRF, clusters,
// cold archive, artifacts); the SearchContextResult JSON contract is
// unchanged. Archivist specifics (polarity, lastN tail, correlated
// tools, reasoning boost) stay on top.
func (a *Archivist) executeSearchContext(
	ctx context.Context,
	params SearchContextParams,
	isRotation bool,
) (*SearchContextResult, error) {
	result := &SearchContextResult{}

	aspects := params.Aspects
	if len(aspects) == 0 {
		aspects = []string{
			"knowledge", "messages", "reasoning", "correlated_tools", "tool_prefs",
		}
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	want := make(map[string]bool, len(aspects))
	for _, aspect := range aspects {
		want[aspect] = true
	}

	// One engine call serves knowledge/messages/archive/tool_prefs.
	var engine []SearchResult
	if want["knowledge"] || want["messages"] || want["archive"] ||
		want["tool_prefs"] {
		ms := NewMemorySearch(a.mem)
		engine = ms.Search(ctx, params.Query, limit, a.currentSessionKey)
	}

	seenMsg := make(map[string]bool)
	mapMessage := func(r SearchResult) {
		content := stripRolePrefix(r.Summary, r.Role)
		ck := normalizeContentKey(content)
		if ck == "" || seenMsg[ck] {
			return
		}
		seenMsg[ck] = true
		// Turn is the archivist-side pika-session index; the engine
		// works in message ids (r.MsgID). Tail hits below carry Turn.
		result.Messages = append(result.Messages, MessageHit{
			Role:    r.Role,
			Content: truncateStr(content, 500),
		})
	}
	keepKnowledge := func(r SearchResult) bool {
		// Same filter semantics as the retired searchKnowledge.
		return params.Polarity == "" || params.Polarity == "all" ||
			r.Polarity == params.Polarity
	}
	mapKnowledge := func(r SearchResult) {
		result.Knowledge = append(result.Knowledge, KnowledgeHit{
			Category:   r.Category,
			Summary:    stripCatPrefix(r.Summary, r.Category),
			Polarity:   r.Polarity,
			Confidence: r.Confidence,
		})
		// PIKA-V3: atom_usage telemetry (TZ-v2-9a F-2) — preserved.
		if r.AtomID != "" {
			tid, _ := a.mem.GetMaxPikaSessionID(ctx, a.currentSessionKey)
			_ = a.mem.InsertAtomUsage(
				ctx, r.AtomID, a.currentSpanID, tid, "BRIEF",
				nil, nil, "", "", a.currentSpanID,
			)
		}
	}

	for _, r := range engine {
		switch r.Type {
		case "session", "archive":
			if want["messages"] || want["archive"] {
				mapMessage(r)
			}
		case "knowledge":
			if r.Category == "tool_pref" && want["tool_prefs"] {
				// tool_prefs ignores polarity (as the old code did).
				result.ToolPrefs = append(result.ToolPrefs, KnowledgeHit{
					Category:   r.Category,
					Summary:    stripCatPrefix(r.Summary, r.Category),
					Polarity:   r.Polarity,
					Confidence: r.Confidence,
				})
			}
			if want["knowledge"] && keepKnowledge(r) {
				mapKnowledge(r)
			}
		default:
			// event / reasoning / artifact / registry / snapshot —
			// surfaced like the old "archive" (events) aspect did.
			if want["knowledge"] || want["archive"] {
				result.Knowledge = append(result.Knowledge, KnowledgeHit{
					Category: r.Type,
					Summary:  r.Summary,
					Polarity: "neutral",
				})
			}
		}
	}

	// Recent tail (recency feed, not search) — on top of engine hits.
	if want["messages"] {
		lastN := a.cfg.DefaultLastN
		if isRotation {
			lastN = a.cfg.RotationLastN
		}
		tail, err := a.recentMessages(ctx, lastN)
		if err != nil {
			log.Printf("WARN pika/archivist: recent msgs: %v", err)
		}
		for _, h := range tail {
			ck := normalizeContentKey(h.Content)
			if ck == "" || seenMsg[ck] {
				continue
			}
			seenMsg[ck] = true
			result.Messages = append(result.Messages, h)
		}
	}

	if want["reasoning"] {
		if kw, err := a.extractReasoningKeywords(ctx); err == nil {
			result.ReasoningKeywords = kw
		}
	}
	if want["correlated_tools"] {
		if ct, err := a.mem.QueryCorrelatedTools(
			ctx, params.Query, limit,
		); err == nil {
			result.CorrelatedTools = ct
		}
	}

	// Reasoning-guided retrieval boost (D-62, D-98).
	if a.cfg.ReasoningGuidedRetrieval &&
		len(result.ReasoningKeywords) > 0 {
		if !a.hasDrift(params.Query, result.ReasoningKeywords) {
			boosted, err := a.boostWithReasoning(
				ctx, result.ReasoningKeywords, params.Polarity, limit,
			)
			if err == nil && len(boosted) > 0 {
				result.Knowledge = deduplicateKnowledge(
					result.Knowledge, boosted,
				)
			}
		}
	}

	return result, nil
}

// recentMessages — guaranteed chat tail (lastN): a recency feed, NOT
// search (the engine has no "last N without query" mode). Scope
// semantics unchanged (D-AUDIT-104). Wave 122 (slice D): FTS search
// moved to the engine; only the tail stays here.
func (a *Archivist) recentMessages(
	ctx context.Context,
	lastN int,
) ([]MessageHit, error) {
	scopeWhere := ""
	var scopeArgs []any
	if a.mem.GetMemoryScope(ctx, a.currentSessionKey) == "session" &&
		a.currentSessionKey != "" {
		scopeWhere = " WHERE chat_id = ?"
		scopeArgs = append(scopeArgs, a.currentSessionKey)
	}
	// #nosec G202 -- scopeWhere is a static string; values parameterized
	rows, err := a.mem.db.QueryContext(ctx,
		"SELECT role, content, pika_session_id FROM messages"+
			scopeWhere+" ORDER BY id DESC LIMIT ?",
		append(append([]any{}, scopeArgs...), lastN)...)
	if err != nil {
		return nil, fmt.Errorf("pika/archivist: recent msgs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hits []MessageHit
	for rows.Next() {
		var role string
		var content sql.NullString
		var turnRaw sql.NullString
		if err := rows.Scan(&role, &content, &turnRaw); err != nil {
			continue
		}
		hits = append(hits, MessageHit{
			Role:    role,
			Content: truncateStr(content.String, 500),
			Turn:    parseTurnID(turnRaw),
		})
	}
	return hits, rows.Err()
}

// stripRolePrefix / stripCatPrefix remove the engine's "[role] " and
// "[cat] " Summary prefixes so MessageHit/KnowledgeHit stay clean.
func stripRolePrefix(summary, role string) string {
	if role == "" {
		return summary
	}
	return strings.TrimPrefix(summary, "["+role+"] ")
}

func stripCatPrefix(summary, cat string) string {
	if cat == "" {
		return summary
	}
	return strings.TrimPrefix(summary, "["+cat+"] ")
}

func (a *Archivist) extractReasoningKeywords(
	ctx context.Context,
) ([]string, error) {
	rows, err := a.mem.db.QueryContext(ctx,
		`SELECT reasoning_keywords FROM reasoning_log
		WHERE reasoning_keywords IS NOT NULL
		AND reasoning_keywords != ''
		ORDER BY id DESC LIMIT 10`)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/archivist: reasoning kw: %w", err,
		)
	}
	defer rows.Close()

	unique := make(map[string]bool)
	for rows.Next() {
		var raw sql.NullString
		if err := rows.Scan(&raw); err != nil || !raw.Valid {
			continue
		}
		var kws []string
		if err := json.Unmarshal(
			[]byte(raw.String), &kws,
		); err != nil {
			continue
		}
		for _, kw := range kws {
			unique[kw] = true
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pika/archivist: reasoning kw rows: %w", err)
	}

	result := make([]string, 0, len(unique))
	for kw := range unique {
		result = append(result, kw)
	}
	return result, nil
}

// hasDrift checks keyword overlap below drift threshold.
func (a *Archivist) hasDrift(
	query string, reasoningKW []string,
) bool {
	qWords := strings.Fields(strings.ToLower(query))
	if len(qWords) == 0 || len(reasoningKW) == 0 {
		return true
	}

	rkSet := make(map[string]bool, len(reasoningKW))
	for _, kw := range reasoningKW {
		rkSet[strings.ToLower(kw)] = true
	}

	overlap := 0
	for _, w := range qWords {
		if rkSet[w] {
			overlap++
		}
	}

	ratio := float64(overlap) / float64(len(qWords))
	return ratio < a.cfg.ReasoningDriftOverlapMin
}

// boostWithReasoning runs additional FTS5 search using
// reasoning keywords as OR-composed query boost.
func (a *Archivist) boostWithReasoning(
	ctx context.Context,
	keywords []string,
	polarity string,
	limit int,
) ([]KnowledgeHit, error) {
	if len(keywords) == 0 {
		return nil, nil
	}
	maxKW := 10
	if len(keywords) > maxKW {
		keywords = keywords[:maxKW]
	}
	q := strings.Join(keywords, " ")
	ms := NewMemorySearch(a.mem)
	var hits []KnowledgeHit
	for _, r := range ms.Search(ctx, q, limit, a.currentSessionKey) {
		if r.Type != "knowledge" {
			continue
		}
		if polarity != "" && polarity != "all" && r.Polarity != polarity {
			continue
		}
		hits = append(hits, KnowledgeHit{
			Category:   r.Category,
			Summary:    stripCatPrefix(r.Summary, r.Category),
			Polarity:   r.Polarity,
			Confidence: r.Confidence,
		})
	}
	return hits, nil
}

// compressBrief asks the LLM to compress the brief.
func (a *Archivist) compressBrief(
	ctx context.Context,
	systemPrompt string,
	output *archivistLLMOutput,
	currentBrief string,
) (MemoryBrief, error) {
	protected := strings.Join(
		a.cfg.CompressProtectedSections, ", ",
	)
	compressMsg := fmt.Sprintf(
		"The MEMORY BRIEF exceeds the soft limit. "+
			"Compress it.\n"+
			"Protected sections (DO NOT modify): %s\n"+
			"Shorten PREFER and CONTEXT sections.\n"+
			"Current brief:\n%s\n\n"+
			"Return the same JSON format with shorter "+
			"prefer and context arrays.",
		protected, currentBrief,
	)

	msgs := []providers.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: compressMsg},
	}

	resp, err := a.provider.Chat(
		ctx, msgs, nil, a.cfg.Model, nil,
	)
	if err != nil {
		return MemoryBrief{}, fmt.Errorf(
			"pika/archivist: compress LLM: %w", err,
		)
	}

	var compressed archivistLLMOutput
	if err := json.Unmarshal(
		[]byte(extractJSON(resp.Content)), &compressed,
	); err != nil {
		// Fallback to original on parse failure
		return output.MemoryBrief, nil //nolint:nilerr // fallback to original on parse failure
	}

	// Protect AVOID and CONSTRAINTS
	compressed.MemoryBrief.Avoid = output.MemoryBrief.Avoid
	compressed.MemoryBrief.Constraints = output.MemoryBrief.Constraints

	return compressed.MemoryBrief, nil
}

// parseArchivistOutput parses the LLM's final JSON response.
func parseArchivistOutput(
	content string,
) (*archivistLLMOutput, error) {
	jsonStr := extractJSON(content)
	if jsonStr == "" {
		return nil, fmt.Errorf(
			"pika/archivist: no JSON in response",
		)
	}
	var out archivistLLMOutput
	if err := json.Unmarshal(
		[]byte(jsonStr), &out,
	); err != nil {
		return nil, fmt.Errorf(
			"pika/archivist: parse JSON: %w", err,
		)
	}
	return &out, nil
}

// extractJSON finds the first balanced { ... } block.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// SerializeMemoryBrief converts MemoryBrief to text.
func SerializeMemoryBrief(mb MemoryBrief) string {
	var sb strings.Builder
	writeSec := func(icon, name string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&sb, "%s %s:\n", icon, name)
		for _, item := range items {
			fmt.Fprintf(&sb, "- %s\n", item)
		}
	}
	writeSec("\u26d4", "AVOID", mb.Avoid)
	writeSec("\U0001f4cb", "CONSTRAINTS", mb.Constraints)
	writeSec("\u2705", "PREFER", mb.Prefer)
	writeSec("\U0001f4dd", "CONTEXT", mb.Context)
	return strings.TrimRight(sb.String(), "\n")
}

// SerializeFocus (волна 124-fix, бой 29 сен): FOCUS в текст промпта.
// Бой показал: Архивариус собирал handoff в Focus (задача/решения/
// следующий шаг), но в системный промпт попадал только BriefText —
// handoff выбрасывался на последней миле («не нашла репо и критерии»).
// Пустой Focus → пустая строка (секция не создаётся).
func SerializeFocus(f Focus) string {
	if f.Task == "" && f.Step == "" {
		return ""
	}
	var sb strings.Builder
	if f.Task != "" {
		fmt.Fprintf(&sb, "TASK: %s\n", f.Task)
	}
	if f.Step != "" {
		fmt.Fprintf(&sb, "STEP: %s\n", f.Step)
	}
	if f.Mode != "" {
		fmt.Fprintf(&sb, "MODE: %s\n", f.Mode)
	}
	if f.Blocked != nil && *f.Blocked != "" {
		fmt.Fprintf(&sb, "BLOCKED: %s\n", *f.Blocked)
	}
	for _, c := range f.Constraints {
		fmt.Fprintf(&sb, "CONSTRAINT: %s\n", c)
	}
	for _, d := range f.Decisions {
		fmt.Fprintf(&sb, "DECISION: %s\n", d)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// estimateTokens gives a rough token count (~4 chars/token).
func estimateTokens(s string) int {
	return len(s) / 4
}

// truncateStr shortens a string to maxLen chars.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// deduplicateKnowledge merges two hit slices by summary.
func deduplicateKnowledge(
	a, b []KnowledgeHit,
) []KnowledgeHit {
	seen := make(map[string]bool, len(a))
	for _, h := range a {
		seen[h.Summary] = true
	}
	result := append([]KnowledgeHit{}, a...)
	for _, h := range b {
		if !seen[h.Summary] {
			result = append(result, h)
		}
	}
	return result
}

// defaultArchivistPrompt used when the prompt file is missing.
const defaultArchivistPrompt = `You are an Archivist.
Analyze the message, determine FOCUS, search for relevant context,
and compose a MEMORY BRIEF.

You have one tool: search_context(query, aspects, polarity, limit).
- First call: polarity="negative" to find AVOID items.
- Second call (if needed): polarity="all" for general context.

Return a JSON object:
{
  "focus": {
    "task": "...", "step": "...", "mode": "...",
    "blocked": null, "constraints": [...], "decisions": [...]
  },
  "memory_brief": {
    "avoid": [...], "constraints": [...],
    "prefer": [...], "context": [...]
  },
  "recommended_tools": [...],
"recommended_skills": [...]
}

Rules:
- Exact values (IPs, ports, paths) verbatim, do not paraphrase.
- Negative-first: AVOID section has highest priority.
- Keep brief concise: soft limit ~5000 tokens.
`

// parseTurnID (волна 93, бой 20 авг 21:26): pika_session_id в проде —
// TEXT вида "sk_v1_...:<unix>" / "session-1:<unix>", не число. Scan
// в int молча убивал КАЖДУЮ строку выдачи (бой: 4 поиска x 0 при живом
// факте в базе; Архивариус не вернул ни одного сообщения за всю жизнь).
// Берём хвост после последнего ':'; не распарсилось — 0.
func parseTurnID(raw sql.NullString) int {
	if !raw.Valid {
		return 0
	}
	s := raw.String
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
