package pika

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// mockLLMProvider implements providers.LLMProvider for testing.
type mockLLMProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	errors    []error
	callIdx   int
	calls     [][]providers.Message
}

func newMockProvider(
	resps ...*providers.LLMResponse,
) *mockLLMProvider {
	errs := make([]error, len(resps))
	return &mockLLMProvider{
		responses: resps,
		errors:    errs,
	}
}

func (m *mockLLMProvider) Chat(
	_ context.Context,
	msgs []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, msgs)
	if m.callIdx >= len(m.responses) {
		return nil, fmt.Errorf("mock: no more responses")
	}
	resp := m.responses[m.callIdx]
	err := m.errors[m.callIdx]
	m.callIdx++
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (m *mockLLMProvider) GetDefaultModel() string {
	return "mock-model"
}

func (m *mockLLMProvider) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func newTestArchivist(
	t *testing.T,
	prov providers.LLMProvider,
) (*Archivist, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Migrate(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mem, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("botmemory: %v", err)
	}
	trail := NewTrail()
	meta := NewMeta()

	promptPath := filepath.Join(dir, "archivist.md")
	//nolint:errcheck
	os.WriteFile(
		promptPath,
		[]byte(defaultArchivistPrompt),
		0o644,
	)

	cfg := DefaultArchivistConfig()
	cfg.PromptFile = promptPath

	a := NewArchivist(mem, prov, trail, meta, cfg)
	cleanup := func() {
		mem.Close()
		db.Close()
	}
	return a, cleanup
}

func TestArchivist_BuildPrompt_SingleToolCall(t *testing.T) {
	// LLM response 1: tool call to search_context
	toolCallResp := &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{
			{
				ID: "call_1",
				Function: &providers.FunctionCall{
					Name:      "search_context",
					Arguments: `{"query":"test","polarity":"negative"}`,
				},
			},
		},
	}
	// LLM response 2: final JSON
	finalResp := &providers.LLMResponse{
		Content: `{
			"focus": {
				"task": "test task",
				"step": "1/3 ACT",
				"mode": "routine",
				"blocked": null,
				"constraints": ["port 8080"],
				"decisions": ["use docker"]
			},
			"memory_brief": {
				"avoid": ["do not restart nginx"],
				"constraints": ["port 8080"],
				"prefer": ["use backup first"],
				"context": ["running nginx v1.25"]
			},
			"recommended_tools": ["compose", "files"]
		}`,
	}

	prov := newMockProvider(toolCallResp, finalResp)
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()

	result, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{
			SessionKey: "test-session",
			Message:    "deploy nginx",
		},
	)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	// FOCUS — 6 fields
	if result.Focus.Task != "test task" {
		t.Errorf("Task = %q", result.Focus.Task)
	}
	if result.Focus.Step != "1/3 ACT" {
		t.Errorf("Step = %q", result.Focus.Step)
	}
	if result.Focus.Mode != "routine" {
		t.Errorf("Mode = %q", result.Focus.Mode)
	}
	if result.Focus.Blocked != nil {
		t.Errorf("Blocked = %v, want nil", result.Focus.Blocked)
	}
	if len(result.Focus.Constraints) != 1 {
		t.Errorf("Constraints = %d", len(result.Focus.Constraints))
	}
	if len(result.Focus.Decisions) != 1 {
		t.Errorf("Decisions = %d", len(result.Focus.Decisions))
	}

	// MEMORY BRIEF — 4 sections
	if len(result.Brief.Avoid) != 1 {
		t.Errorf("Avoid = %d", len(result.Brief.Avoid))
	}
	if len(result.Brief.Constraints) != 1 {
		t.Errorf("Constraints = %d", len(result.Brief.Constraints))
	}
	if len(result.Brief.Prefer) != 1 {
		t.Errorf("Prefer = %d", len(result.Brief.Prefer))
	}
	if len(result.Brief.Context) != 1 {
		t.Errorf("Context = %d", len(result.Brief.Context))
	}

	if result.BriefText == "" {
		t.Error("BriefText is empty")
	}
	if len(result.RecommendedTools) != 2 {
		t.Errorf("ToolSet = %d, want 2", len(result.RecommendedTools))
	}

	// 2 LLM calls: tool call + final
	if prov.callCount() != 2 {
		t.Errorf("LLM calls = %d, want 2", prov.callCount())
	}
}

// Волна 87 (бой 20 авг): мягкий потолок — при превышении лимита
// BuildPrompt НЕ падает: недопущенный вызов получает notice, финальная
// итерация без инструментов, бриф собирается из уже найденного.
func TestArchivist_SoftCap_ProducesBrief(t *testing.T) {
	tcResp := &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{
			{
				ID: "call_1",
				Function: &providers.FunctionCall{
					Name:      "search_context",
					Arguments: `{"query":"test"}`,
				},
			},
		},
	}
	finalResp := &providers.LLMResponse{
		Content: `{"focus":{"task":"t","step":null,"mode":"routine","blocked":null,"constraints":[],"decisions":[]},"memory_brief":{"avoid":[],"constraints":[],"prefer":[],"context":[]},"recommended_tools":[],"recommended_skills":[]}`,
	}
	prov := newMockProvider(tcResp, tcResp, finalResp)
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()
	a.cfg.MaxToolCalls = 1

	result, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{SessionKey: "s1", Message: "test"},
	)
	if err != nil {
		t.Fatalf("soft cap must not fail BuildPrompt: %v", err)
	}
	if result == nil || result.Focus.Task != "t" {
		t.Fatalf("result = %+v", result)
	}
	if prov.callCount() != 3 {
		t.Errorf("LLM calls = %d, want 3", prov.callCount())
	}
}

// Волна 87: модель, игнорирующая tools=nil после мягкого потолка,
// получает явную ошибку вместо бесконечного цикла.
func TestArchivist_SoftCap_ModelIgnoresNilTools(t *testing.T) {
	tcResp := &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{
			{
				ID: "call_1",
				Function: &providers.FunctionCall{
					Name:      "search_context",
					Arguments: `{"query":"test"}`,
				},
			},
		},
	}
	resps := make([]*providers.LLMResponse, 10)
	for i := range resps {
		resps[i] = tcResp
	}
	prov := newMockProvider(resps...)
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()
	a.cfg.MaxToolCalls = 1

	_, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{SessionKey: "s1", Message: "test"},
	)
	if err == nil {
		t.Fatal("expected error when model ignores nil tools")
	}
	if !strings.Contains(err.Error(), "soft cap") {
		t.Errorf("error = %q", err)
	}
}

func TestArchivist_CachedBrief(t *testing.T) {
	finalJSON := `{
		"focus": {"task":"t","step":"s","mode":"m",
		"blocked":null,"constraints":[],"decisions":[]},
		"memory_brief": {"avoid":[],"constraints":[],
		"prefer":[],"context":["cached"]},
		"recommended_tools": []
	}`
	prov := newMockProvider(
		&providers.LLMResponse{Content: finalJSON},
	)
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()

	// First call -> LLM
	r1, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{SessionKey: "s1", Message: "hi"},
	)
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	// Second call -> cached (0 LLM calls)
	r2, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{SessionKey: "s1", Message: "hi2"},
	)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if r2.BriefText != r1.BriefText {
		t.Error("cached brief should match")
	}
	if prov.callCount() != 1 {
		t.Errorf("calls = %d, want 1", prov.callCount())
	}

	// Invalidate -> next call triggers LLM
	a.InvalidateBrief()
	if a.GetCachedBrief() != "" {
		t.Error("brief not cleared")
	}
	if a.GetCachedFocus() != nil {
		t.Error("focus not cleared")
	}
}

func TestArchivist_DegradedMode_LLMError(t *testing.T) {
	prov := &mockLLMProvider{
		responses: []*providers.LLMResponse{nil},
		errors:    []error{fmt.Errorf("LLM unavailable")},
	}
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()

	_, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{SessionKey: "s1", Message: "test"},
	)
	if err == nil {
		t.Fatal("expected error on LLM failure")
	}
}

func TestArchivist_InvalidJSON(t *testing.T) {
	prov := newMockProvider(
		&providers.LLMResponse{Content: "not json"},
	)
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()

	_, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{SessionKey: "s1", Message: "test"},
	)
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestSerializeMemoryBrief(t *testing.T) {
	mb := MemoryBrief{
		Avoid:       []string{"don't stop nginx"},
		Constraints: []string{"port 8080"},
		Prefer:      []string{"use backup"},
		Context:     []string{"running v1.25"},
	}
	text := SerializeMemoryBrief(mb)
	if !strings.Contains(text, "AVOID") {
		t.Error("missing AVOID")
	}
	if !strings.Contains(text, "CONSTRAINTS") {
		t.Error("missing CONSTRAINTS")
	}
	if !strings.Contains(text, "PREFER") {
		t.Error("missing PREFER")
	}
	if !strings.Contains(text, "CONTEXT") {
		t.Error("missing CONTEXT")
	}
	if !strings.Contains(text, "don't stop nginx") {
		t.Error("missing avoid item")
	}
}

func TestSearchContext_EmptyDB(t *testing.T) {
	prov := newMockProvider()
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()

	result, err := a.executeSearchContext(
		context.Background(),
		SearchContextParams{
			Query:    "test",
			Polarity: "negative",
		},
		false,
	)
	if err != nil {
		t.Fatalf("searchContext: %v", err)
	}
	if result == nil {
		t.Fatal("result nil")
	}
	if len(result.Knowledge) != 0 {
		t.Errorf("knowledge = %d", len(result.Knowledge))
	}
	if len(result.Messages) != 0 {
		t.Errorf("messages = %d", len(result.Messages))
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`prefix {"a":1} suffix`, `{"a":1}`},
		{`{"a":{"b":2}}`, `{"a":{"b":2}}`},
		{`no json`, ""},
		{``, ""},
	}
	for _, tt := range tests {
		got := extractJSON(tt.in)
		if got != tt.want {
			t.Errorf(
				"extractJSON(%q) = %q, want %q",
				tt.in, got, tt.want,
			)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if estimateTokens("1234") != 1 {
		t.Error("4 chars != 1 token")
	}
	if estimateTokens("12345678") != 2 {
		t.Error("8 chars != 2 tokens")
	}
}

// Волна 83 (бой 20 авг): tool call от провайдера может прийти без
// Function-структуры (только internal top-level Name/Arguments,
// ThoughtSignature в ExtraContent). Цикл обязан нормализовать — иначе
// Gemini отклоняет второй запрос: 400 «assistant message produced no
// valid function calls but is followed by tool result messages».
func TestArchivist_BuildPrompt_NormalizesToolCalls(t *testing.T) {
	toolCallResp := &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{
			{
				ID:               "call_1",
				Name:             "search_context",
				Arguments:        map[string]any{"query": "test", "polarity": "negative"},
				ThoughtSignature: "sig-1",
				ExtraContent: &providers.ExtraContent{
					Google: &providers.GoogleExtra{ThoughtSignature: "sig-1"},
				},
			},
		},
	}
	finalResp := &providers.LLMResponse{
		Content: `{
			"focus": {"task": "test task", "step": null, "mode": "routine", "blocked": null, "constraints": [], "decisions": []},
			"memory_brief": {"avoid": [], "constraints": [], "prefer": [], "context": []},
			"recommended_tools": [], "recommended_skills": []
		}`,
	}
	prov := newMockProvider(toolCallResp, finalResp)
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()

	result, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{SessionKey: "test-session", Message: "deploy nginx"},
	)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if result == nil || result.Focus.Task != "test task" {
		t.Fatalf("result = %+v", result)
	}

	prov.mu.Lock()
	defer prov.mu.Unlock()
	if len(prov.calls) != 2 {
		t.Fatalf("LLM calls = %d, want 2 (tool loop)", len(prov.calls))
	}
	var assistant, toolResult *providers.Message
	msgs := prov.calls[1]
	for i := range msgs {
		switch msgs[i].Role {
		case "assistant":
			assistant = &msgs[i]
		case "tool":
			toolResult = &msgs[i]
		}
	}
	if assistant == nil || len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant message with tool call not echoed: %+v", msgs)
	}
	tc := assistant.ToolCalls[0]
	if tc.Function == nil || tc.Function.Name != "search_context" {
		t.Errorf("Function not backfilled: %+v", tc)
	}
	if tc.Function == nil || tc.Function.ThoughtSignature != "sig-1" {
		t.Errorf("ThoughtSignature lost in echo: %+v", tc)
	}
	// Волна 84: type:"function" обязателен на проводе.
	if tc.Type != "function" {
		t.Errorf("Type not backfilled on echo: %+v", tc)
	}
	if toolResult == nil || strings.Contains(toolResult.Content, "unknown tool") {
		t.Errorf("tool result wrong: %+v", toolResult)
	}
}

// Волна 86 (бой 20 авг): капс-факт («СИНИЙ») находится по «синий»,
// многословный запрос с лишними словами находит факт.
func TestArchivist_SearchMessages_FTS(t *testing.T) {
	a, cleanup := newTestArchivist(t, newMockProvider())
	defer cleanup()
	ctx := context.Background()

	if _, err := a.mem.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "привет, это тест. мой любимый цвет СИНИЙ", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.mem.SaveMessage(ctx, MessageRow{
		ChatID: "s1", PikaSessionID: "1", Role: "user",
		Content: "погода завтра дождь", Tokens: 5,
	}); err != nil {
		t.Fatal(err)
	}

	hasFact := func(hits []MessageHit) bool {
		for _, h := range hits {
			if strings.Contains(h.Content, "СИНИЙ") {
				return true
			}
		}
		return false
	}

	hits, err := a.searchMessages(ctx, "любимый цвет пользователя Gar", 10, 5)
	if err != nil {
		t.Fatalf("searchMessages: %v", err)
	}
	if !hasFact(hits) {
		t.Fatalf("verbose query: fact not found in %d hits", len(hits))
	}

	hits2, _ := a.searchMessages(ctx, "синий", 10, 5)
	if !hasFact(hits2) {
		t.Fatal("caps fact not found by lowercase query")
	}
}

// Волна 90 (бой 20 авг): финал прозой вместо JSON — цикл вталкивает
// nudge и получает JSON со второй попытки, бриф не падает.
func TestArchivist_NoJSONRetry(t *testing.T) {
	prose := &providers.LLMResponse{Content: "Нашёл несколько записей в памяти, сейчас оформлю."}
	final := &providers.LLMResponse{
		Content: `{"focus":{"task":"t","step":null,"mode":"routine","blocked":null,"constraints":[],"decisions":[]},"memory_brief":{"avoid":[],"constraints":[],"prefer":[],"context":["любимый цвет — синий"]},"recommended_tools":[],"recommended_skills":[]}`,
	}
	prov := newMockProvider(prose, final)
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()

	result, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{SessionKey: "s1", Message: "какой мой любимый цвет?"},
	)
	if err != nil {
		t.Fatalf("no-JSON retry must recover: %v", err)
	}
	if len(result.Brief.Context) != 1 {
		t.Fatalf("brief context = %v", result.Brief.Context)
	}
	if prov.callCount() != 2 {
		t.Errorf("LLM calls = %d, want 2 (prose + nudge retry)", prov.callCount())
	}
}

// Волна 92: прогон оставляет читаемый след — превью на родительском
// спане и дочерний спан search_context с запросом.
func TestArchivist_SpanPreviews(t *testing.T) {
	toolCallResp := &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{
			{
				ID: "call_1",
				Function: &providers.FunctionCall{
					Name:      "search_context",
					Arguments: `{"query":"цвет"}`,
				},
			},
		},
	}
	finalResp := &providers.LLMResponse{
		Content: `{"focus":{"task":"t","step":null,"mode":"routine","blocked":null,"constraints":[],"decisions":[]},"memory_brief":{"avoid":[],"constraints":[],"prefer":[],"context":["цвет — синий"]},"recommended_tools":[],"recommended_skills":[]}`,
	}
	prov := newMockProvider(toolCallResp, finalResp)
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()

	_, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{SessionKey: "s1", Message: "какой мой любимый цвет?"},
	)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}

	var childIn string
	err = a.mem.db.QueryRow(
		`SELECT coalesce(input_preview,'') FROM trace_spans
		WHERE operation='search_context'`,
	).Scan(&childIn)
	if err != nil {
		t.Fatalf("child span missing: %v", err)
	}
	if childIn != "цвет" {
		t.Errorf("child input_preview = %q, want query", childIn)
	}

	var parentOut string
	err = a.mem.db.QueryRow(
		`SELECT coalesce(output_preview,'') FROM trace_spans
		WHERE component='archivist' AND operation='build_prompt'`,
	).Scan(&parentOut)
	if err != nil {
		t.Fatalf("parent span: %v", err)
	}
	if !strings.Contains(parentOut, "синий") {
		t.Errorf("parent output_preview = %q, want brief text", parentOut)
	}
}

// Волна 93 (бой 20 авг 21:26): в проде pika_session_id — TEXT вида
// "sk_v1_...:<ts>". Scan в int молча выбрасывал каждую строку →
// messages=0 при живом факте в базе. На старом коде этот тест падает.
func TestArchivist_SearchMessages_NonNumericSessionID(t *testing.T) {
	a, cleanup := newTestArchivist(t, newMockProvider())
	defer cleanup()
	ctx := context.Background()

	if _, err := a.mem.SaveMessage(ctx, MessageRow{
		ChatID:        "sk_v1_old",
		PikaSessionID: "sk_v1_old:1787244471",
		Role:          "user",
		Content:       "привет, это тест. мой любимый цвет СИНИЙ",
		Tokens:        5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.mem.SetMemoryScope(ctx, "sk_v1_old", "all"); err != nil {
		t.Fatal(err)
	}
	a.currentSessionKey = "sk_v1_old"

	hits, err := a.searchMessages(ctx, "любимый цвет", 10, 5)
	if err != nil {
		t.Fatalf("searchMessages: %v", err)
	}
	found := false
	for _, h := range hits {
		if strings.Contains(h.Content, "СИНИЙ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("non-numeric pika_session_id emptied results: %v", hits)
	}
}

// Волна 97 (бой 21 авг 11:17): после /memory all ход получил кэш брифа,
// построенный для ДРУГОГО чата при session-scope — Архивариус не
// запустился (0 LLM-вызовов). Смена scope или сессии обязана ронять кэш.
func TestArchivist_CacheInvalidatedOnScopeChange(t *testing.T) {
	finalJSON := `{
		"focus": {"task":"t","step":"s","mode":"m",
		"blocked":null,"constraints":[],"decisions":[]},
		"memory_brief": {"avoid":[],"constraints":[],
		"prefer":[],"context":["cached"]},
		"recommended_tools": []
	}`
	prov := newMockProvider(
		&providers.LLMResponse{Content: finalJSON},
		&providers.LLMResponse{Content: finalJSON},
		&providers.LLMResponse{Content: finalJSON},
		&providers.LLMResponse{Content: finalJSON},
	)
	a, cleanup := newTestArchivist(t, prov)
	defer cleanup()
	ctx := context.Background()

	// Первая сборка для s1 (scope=session по умолчанию)
	if _, err := a.BuildPrompt(ctx, ArchivistInput{
		SessionKey: "s1", Message: "hi",
	}); err != nil {
		t.Fatal(err)
	}
	// Та же сессия, тот же scope — кэш
	if _, err := a.BuildPrompt(ctx, ArchivistInput{
		SessionKey: "s1", Message: "hi again",
	}); err != nil {
		t.Fatal(err)
	}
	if prov.callCount() != 1 {
		t.Fatalf("calls = %d, want 1 (warm cache)", prov.callCount())
	}

	// /memory all → scope сменился → кэш обязан упасть
	if err := a.mem.SetMemoryScope(ctx, "s1", "all"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.BuildPrompt(ctx, ArchivistInput{
		SessionKey: "s1", Message: "after scope change",
	}); err != nil {
		t.Fatal(err)
	}
	if prov.callCount() != 2 {
		t.Fatalf("calls = %d, want 2 (scope change must rebuild)",
			prov.callCount())
	}

	// Другая сессия → тоже пересборка (бой: чат Б получил кэш чата А)
	if _, err := a.BuildPrompt(ctx, ArchivistInput{
		SessionKey: "s2", Message: "other chat",
	}); err != nil {
		t.Fatal(err)
	}
	if prov.callCount() != 3 {
		t.Fatalf("calls = %d, want 3 (session change must rebuild)",
			prov.callCount())
	}
}
