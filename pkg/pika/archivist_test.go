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
			"tool_set": ["compose", "files"]
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
	if len(result.ToolSet) != 2 {
		t.Errorf("ToolSet = %d, want 2", len(result.ToolSet))
	}

	// 2 LLM calls: tool call + final
	if prov.callCount() != 2 {
		t.Errorf("LLM calls = %d, want 2", prov.callCount())
	}
}

func TestArchivist_MaxToolCallsExceeded(t *testing.T) {
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

	_, err := a.BuildPrompt(
		context.Background(),
		ArchivistInput{SessionKey: "s1", Message: "test"},
	)
	if err == nil {
		t.Fatal("expected error for max tool calls")
	}
	if !strings.Contains(err.Error(), "max tool calls") {
		t.Errorf("error = %q", err)
	}
}

func TestArchivist_CachedBrief(t *testing.T) {
	finalJSON := `{
		"focus": {"task":"t","step":"s","mode":"m",
		"blocked":null,"constraints":[],"decisions":[]},
		"memory_brief": {"avoid":[],"constraints":[],
		"prefer":[],"context":["cached"]},
		"tool_set": []
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
