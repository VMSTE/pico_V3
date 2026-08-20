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
	Model                     string
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
	a.mu.RLock()
	cached := a.lastResult
	a.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	// PIKA-V3: Trace span (TZ-v2-9a block 3)
	spanIDarchivist := fmt.Sprintf("span_archivist_%d", time.Now().UnixNano())
	traceIDarchivist := fmt.Sprintf("trace_archivist_%d", time.Now().UnixNano())
	a.currentSessionKey = input.SessionKey
	a.currentSpanID = spanIDarchivist
	a.currentTraceID = traceIDarchivist
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
			truncateStr(input.Message, 300), truncateStr(briefPreview, 500),
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

// executeSearchContext performs the Go fan-out across 4 aspects.
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

	for _, aspect := range aspects {
		switch aspect {
		case "knowledge":
			hits, err := a.searchKnowledge(
				ctx, params.Query, params.Polarity, limit,
			)
			if err == nil {
				result.Knowledge = hits
			}
		case "messages":
			lastN := a.cfg.DefaultLastN
			if isRotation {
				lastN = a.cfg.RotationLastN
			}
			hits, err := a.searchMessages(
				ctx, params.Query, limit, lastN,
			)
			if err == nil {
				result.Messages = hits
			} else {
				log.Printf("WARN pika/archivist: messages search: %v", err)
			}
		case "reasoning":
			kw, err := a.extractReasoningKeywords(ctx)
			if err == nil {
				result.ReasoningKeywords = kw
			}
		case "archive":
			aHits, err := a.mem.SearchEventsArchiveFTS(
				ctx, params.Query, limit,
			)
			if err == nil {
				for _, h := range aHits {
					result.Knowledge = append(
						result.Knowledge,
						KnowledgeHit{
							Category: h.Type,
							Summary:  h.Summary,
							Polarity: "neutral",
						},
					)
				}
			}
		case "correlated_tools":
			ct, err := a.mem.QueryCorrelatedTools(ctx, params.Query, limit)
			if err == nil {
				result.CorrelatedTools = ct
			}
		case "tool_prefs":
			hits, err := a.searchKnowledge(
				ctx, params.Query, "", limit,
			)
			if err == nil {
				var prefs []KnowledgeHit
				for _, h := range hits {
					if h.Category == "tool_pref" {
						prefs = append(prefs, h)
					}
				}
				result.ToolPrefs = prefs
			}
		}
	}

	// Reasoning-guided retrieval boost (D-62, D-98)
	if a.cfg.ReasoningGuidedRetrieval &&
		len(result.ReasoningKeywords) > 0 {
		if !a.hasDrift(
			params.Query, result.ReasoningKeywords,
		) {
			boosted, err := a.boostWithReasoning(
				ctx, result.ReasoningKeywords,
				params.Polarity, limit,
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

// searchKnowledge queries knowledge_atoms via FTS5.
func (a *Archivist) searchKnowledge(
	ctx context.Context,
	query, polarity string,
	limit int,
) ([]KnowledgeHit, error) {
	// Волна 86: сырой multiword-запрос в FTS MATCH работал как AND
	// (слишком строго). buildFTSQuery — OR по словам, как везде.
	fq := buildFTSQuery(query)
	if fq == "" {
		return nil, nil
	}
	atoms, err := a.mem.QueryKnowledgeFTS(
		ctx, fq, limit*2,
	)
	if err != nil {
		return nil, err
	}
	var hits []KnowledgeHit
	for _, atom := range atoms {
		if polarity != "" && polarity != "all" &&
			atom.Polarity != polarity {
			continue
		}
		// PIKA-V3: record atom_usage (TZ-v2-9a F-2)
		tid, _ := a.mem.GetMaxPikaSessionID(ctx, a.currentSessionKey)
		_ = a.mem.InsertAtomUsage(ctx, atom.AtomID, a.currentSpanID, tid, "BRIEF", nil, nil, "", "", a.currentSpanID)
		hits = append(hits, KnowledgeHit{
			Category:   atom.Category,
			Summary:    atom.Summary,
			Polarity:   atom.Polarity,
			Confidence: atom.Confidence,
		})
		if len(hits) >= limit {
			break
		}
	}
	return hits, nil
}

// searchMessages searches messages across all sessions.
func (a *Archivist) searchMessages(
	ctx context.Context,
	query string,
	limit, lastN int,
) ([]MessageHit, error) {
	var hits []MessageHit

	// D-AUDIT-104: per-chat memory scope — session mode restricts to the
	// current chat (chat_id); all mode searches the whole base.
	scopeWhere := ""
	var scopeArgs []any
	if a.mem.GetMemoryScope(ctx, a.currentSessionKey) == "session" &&
		a.currentSessionKey != "" {
		scopeWhere = " WHERE chat_id = ?"
		scopeArgs = append(scopeArgs, a.currentSessionKey)
	}

	// Guaranteed last N messages (most recent, any session)
	// #nosec G202 -- scopeWhere is a static string; value parameterized
	rows, err := a.mem.db.QueryContext(ctx,
		`SELECT role, content, pika_session_id
		FROM messages`+scopeWhere+` ORDER BY id DESC LIMIT ?`,
		append(append([]any{}, scopeArgs...), lastN)...)
	if err != nil {
		return nil, fmt.Errorf(
			"pika/archivist: recent msgs: %w", err,
		)
	}
	defer rows.Close()

	seen := make(map[string]bool)
	for rows.Next() {
		var role string
		var content sql.NullString
		var turnRaw sql.NullString
		if err := rows.Scan(
			&role, &content, &turnRaw,
		); err != nil {
			continue
		}
		turn := parseTurnID(turnRaw)
		c := content.String
		key := fmt.Sprintf("%s:%d", role, turn)
		seen[key] = true
		hits = append(hits, MessageHit{
			Role:    role,
			Content: truncateStr(c, 500),
			Turn:    turn,
		})
	}
	if err := rows.Err(); err != nil {
		return hits, nil
	}

	// Волна 86 (бой 20 авг): FTS5 вместо однофразного LIKE.
	// LIKE '%весь запрос%' не находил многословное, а регистр кириллицы
	// («СИНИЙ» vs «синий») SQLite не складывает. messages_fts (unicode61)
	// решает оба; buildFTSQuery — тот же, что в search_memory.
	if query != "" {
		fq := buildFTSQuery(query)
		if fq != "" {
			ftsQ := `SELECT role, content, pika_session_id
				FROM messages_fts f
				JOIN messages m ON m.id = f.rowid
				WHERE messages_fts MATCH ?`
			ftsArgs := []any{fq}
			if scopeWhere != "" {
				ftsQ += ` AND m.chat_id = ?`
				ftsArgs = append(ftsArgs, a.currentSessionKey)
			}
			// Волна 90: over-fetch x4 + tie-break, как в search_memory.
			ftsQ += ` ORDER BY bm25(messages_fts), m.id LIMIT ?`
			ftsArgs = append(ftsArgs, limit*4)
			// #nosec G202 -- статические фрагменты; значения параметризованы
			rows2, err2 := a.mem.db.QueryContext(ctx, ftsQ, ftsArgs...)
			if err2 != nil {
				log.Printf("WARN pika/archivist: messages FTS: %v (q=%q)", err2, fq)
			}
			if err2 == nil {
				defer rows2.Close()
				// Волна 90: дедуп эха по содержимому — N копий вопроса
				// схлопываются в одну строку, слоты достаются фактам.
				seenContent := make(map[string]bool)
				for rows2.Next() {
					var role string
					var content sql.NullString
					var turnRaw sql.NullString
					if err := rows2.Scan(&role, &content, &turnRaw); err != nil {
						continue
					}
					turn := parseTurnID(turnRaw)
					key := fmt.Sprintf("%s:%d", role, turn)
					if seen[key] {
						continue
					}
					seen[key] = true
					ck := normalizeContentKey(content.String)
					if seenContent[ck] {
						continue
					}
					seenContent[ck] = true
					hits = append(hits, MessageHit{
						Role:    role,
						Content: truncateStr(content.String, 500),
						Turn:    turn,
					})
				}
				_ = rows2.Err()
			}
		}
	}

	return hits, nil
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
	q := strings.Join(keywords, " OR ")
	return a.searchKnowledge(ctx, q, polarity, limit)
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
