package pika

// PIKA-V3: tool_router_test.go — tests for D-TOOL-CLASS routing

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// --- mock types ---

type mockTool struct {
	name   string
	desc   string
	result *toolshared.ToolResult
	calls  int
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string  { return m.desc }
func (m *mockTool) Parameters() map[string]any {
	return nil
}
func (m *mockTool) Execute(
	_ context.Context, _ map[string]any,
) *toolshared.ToolResult {
	m.calls++
	return m.result
}

type mockMCPCaller struct {
	result *toolshared.ToolResult
	err    error
	called bool
}

func (m *mockMCPCaller) CallTool(
	_ context.Context,
	_, _ string,
	_ map[string]any,
) (*toolshared.ToolResult, error) {
	m.called = true
	return m.result, m.err
}

// dynamicMockTool allows custom Execute logic for retry tests.
type dynamicMockTool struct {
	name string
	desc string
	exec func(
		context.Context, map[string]any,
	) *toolshared.ToolResult
}

func (d *dynamicMockTool) Name() string        { return d.name }
func (d *dynamicMockTool) Description() string  { return d.desc }
func (d *dynamicMockTool) Parameters() map[string]any {
	return nil
}
func (d *dynamicMockTool) Execute(
	ctx context.Context, args map[string]any,
) *toolshared.ToolResult {
	return d.exec(ctx, args)
}

// --- helpers ---

func brainTools() []*mockTool {
	return []*mockTool{
		{
			name:   "search_memory",
			desc:   "Search memory",
			result: toolshared.NewToolResult("memory result"),
		},
		{
			name:   "registry_write",
			desc:   "Write registry",
			result: toolshared.NewToolResult("registry result"),
		},
		{
			name:   "clarify",
			desc:   "Clarify",
			result: toolshared.NewToolResult("clarify result"),
		},
	}
}

func testBaseTools() []*mockTool {
	return []*mockTool{
		{
			name:   "exec",
			desc:   "Execute",
			result: toolshared.NewToolResult("exec ok"),
		},
		{
			name:   "read_file",
			desc:   "Read file",
			result: toolshared.NewToolResult("read ok"),
		},
		{
			name:   "write_file",
			desc:   "Write file",
			result: toolshared.NewToolResult("write ok"),
		},
		{
			name:   "edit_file",
			desc:   "Edit file",
			result: toolshared.NewToolResult("edit ok"),
		},
		{
			name:   "append_file",
			desc:   "Append file",
			result: toolshared.NewToolResult("append ok"),
		},
		{
			name:   "list_dir",
			desc:   "List dir",
			result: toolshared.NewToolResult("listdir ok"),
		},
	}
}

func allEnabledCfg() *config.BaseToolsConfig {
	cfg := config.DefaultBaseToolsConfig()
	return &cfg
}

func allDisabledCfg() *config.BaseToolsConfig {
	return &config.BaseToolsConfig{Enabled: false}
}

func singleDisabledCfg(
	name string,
) *config.BaseToolsConfig {
	cfg := config.DefaultBaseToolsConfig()
	switch name {
	case "exec":
		cfg.Exec = false
	case "read_file":
		cfg.ReadFile = false
	case "write_file":
		cfg.WriteFile = false
	case "edit_file":
		cfg.EditFile = false
	case "append_file":
		cfg.AppendFile = false
	case "list_dir":
		cfg.ListDir = false
	}
	return &cfg
}

func setupRouter(
	baseCfg *config.BaseToolsConfig,
) *ToolRouter {
	r := NewToolRouter(baseCfg)
	for _, t := range brainTools() {
		r.RegisterBrain(t)
	}
	for _, t := range testBaseTools() {
		r.RegisterBase(t)
	}
	return r
}

func toolNamesSet(
	names map[ToolCategory][]string,
	cat ToolCategory,
) map[string]bool {
	s := make(map[string]bool)
	for _, n := range names[cat] {
		s[n] = true
	}
	return s
}

// --- tests ---

func TestToolRouter_AllDefaultsEnabled(t *testing.T) {
	r := setupRouter(allEnabledCfg())

	names := r.EnabledToolNames()
	brainSet := toolNamesSet(names, CategoryBrain)
	baseSet := toolNamesSet(names, CategoryBase)

	// All 3 BRAIN tools
	if len(brainSet) != 3 {
		t.Errorf(
			"expected 3 BRAIN tools, got %d",
			len(brainSet),
		)
	}
	for _, n := range []string{
		"search_memory", "registry_write", "clarify",
	} {
		if !brainSet[n] {
			t.Errorf("BRAIN tool %q missing", n)
		}
	}

	// All 6 BASE tools
	if len(baseSet) != 6 {
		t.Errorf(
			"expected 6 BASE tools, got %d",
			len(baseSet),
		)
	}
	for _, n := range []string{
		"exec", "read_file", "write_file",
		"edit_file", "append_file", "list_dir",
	} {
		if !baseSet[n] {
			t.Errorf("BASE tool %q missing", n)
		}
	}
}

func TestToolRouter_AllBaseDisabled(t *testing.T) {
	r := setupRouter(allDisabledCfg())
	// Add SKILL + MCP to show they survive
	r.RegisterSkill(&mockTool{
		name:   "web_fetch",
		desc:   "Fetch web",
		result: toolshared.NewToolResult("fetched"),
	})
	r.RegisterMCPTool("notion_search", "notion-server")

	names := r.EnabledToolNames()
	brainSet := toolNamesSet(names, CategoryBrain)
	baseSet := toolNamesSet(names, CategoryBase)
	skillSet := toolNamesSet(names, CategorySkill)
	mcpSet := toolNamesSet(names, CategoryMCP)

	// 🧠 BRAIN — always present (3)
	if len(brainSet) != 3 {
		t.Errorf(
			"expected 3 BRAIN tools, got %d",
			len(brainSet),
		)
	}
	// 🔧 BASE — all disabled (0)
	if len(baseSet) != 0 {
		t.Errorf(
			"expected 0 BASE tools, got %d: %v",
			len(baseSet), baseSet,
		)
	}
	// 🛠️ SKILL — present (1)
	if !skillSet["web_fetch"] {
		t.Errorf("SKILL web_fetch missing: %v", skillSet)
	}
	// 🔌 MCP — present (1)
	if !mcpSet["notion_search"] {
		t.Errorf(
			"MCP notion_search missing: %v", mcpSet,
		)
	}
}

func TestToolRouter_SingleBaseDisabled(t *testing.T) {
	r := setupRouter(singleDisabledCfg("exec"))

	names := r.EnabledToolNames()
	baseSet := toolNamesSet(names, CategoryBase)

	if baseSet["exec"] {
		t.Error(
			"exec should not be in enabled BASE tools",
		)
	}
	if len(baseSet) != 5 {
		t.Errorf(
			"expected 5 BASE tools, got %d",
			len(baseSet),
		)
	}
	for _, n := range []string{
		"read_file", "write_file",
		"edit_file", "append_file", "list_dir",
	} {
		if !baseSet[n] {
			t.Errorf(
				"BASE tool %q should be enabled", n,
			)
		}
	}
}

func TestToolRouter_CallDisabledBase(t *testing.T) {
	r := setupRouter(allDisabledCfg())

	result := r.Route(
		context.Background(), "exec", nil,
	)

	if result == nil {
		t.Fatal("expected error result, got nil")
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	expect := "tool disabled by config"
	if !strings.Contains(result.ForLLM, expect) {
		t.Errorf(
			"expected %q in error, got %q",
			expect, result.ForLLM,
		)
	}
}

func TestToolRouter_BrainAlwaysAvailable(t *testing.T) {
	// Even with all BASE disabled, BRAIN routing works
	r := setupRouter(allDisabledCfg())

	for _, name := range []string{
		"search_memory", "registry_write", "clarify",
	} {
		result := r.Route(
			context.Background(), name, nil,
		)
		if result == nil {
			t.Errorf(
				"BRAIN tool %q returned nil", name,
			)
			continue
		}
		if result.IsError {
			t.Errorf(
				"BRAIN tool %q should succeed, got: %s",
				name, result.ForLLM,
			)
		}
	}
}

func TestToolRouter_UnknownTool(t *testing.T) {
	r := setupRouter(allEnabledCfg())

	result := r.Route(
		context.Background(), "nonexistent_tool", nil,
	)
	if result == nil {
		t.Fatal("expected error result, got nil")
	}
	if !result.IsError {
		t.Error("expected IsError=true for unknown tool")
	}
	if !strings.Contains(result.ForLLM, "unknown tool") {
		t.Errorf(
			"expected 'unknown tool', got %q",
			result.ForLLM,
		)
	}
}

func TestToolRouter_MCPRouting(t *testing.T) {
	r := setupRouter(allEnabledCfg())

	mcpResult := toolshared.NewToolResult("mcp data")
	caller := &mockMCPCaller{result: mcpResult}

	r.SetMCPCaller(caller)
	r.RegisterMCPTool(
		"notion_search", "notion-server",
	)

	result := r.Route(
		context.Background(), "notion_search", nil,
	)
	if !caller.called {
		t.Error("MCPCaller.CallTool was not called")
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.ForLLM != "mcp data" {
		t.Errorf(
			"expected 'mcp data', got %q",
			result.ForLLM,
		)
	}
}

func TestToolRouter_MCPError(t *testing.T) {
	r := setupRouter(allEnabledCfg())

	caller := &mockMCPCaller{
		err: fmt.Errorf("connection refused"),
	}
	r.SetMCPCaller(caller)
	r.RegisterMCPTool("grafana_query", "grafana")

	result := r.Route(
		context.Background(), "grafana_query", nil,
	)
	if result == nil {
		t.Fatal("result is nil")
	}
	if !result.IsError {
		t.Error("expected IsError=true on MCP failure")
	}
	expect := "connection refused"
	if !strings.Contains(result.ForLLM, expect) {
		t.Errorf(
			"expected %q in error, got %q",
			expect, result.ForLLM,
		)
	}
}

func TestToolRouter_MCPNotConfigured(t *testing.T) {
	r := setupRouter(allEnabledCfg())
	// No MCPCaller set
	r.RegisterMCPTool("some_mcp_tool", "some-server")

	result := r.Route(
		context.Background(), "some_mcp_tool", nil,
	)
	if result == nil {
		t.Fatal("result is nil")
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	expect := "MCP caller not configured"
	if !strings.Contains(result.ForLLM, expect) {
		t.Errorf(
			"expected %q in error, got %q",
			expect, result.ForLLM,
		)
	}
}

func TestToolRouter_SkillRouting(t *testing.T) {
	r := setupRouter(allEnabledCfg())
	r.RegisterSkill(&mockTool{
		name:   "web_search",
		desc:   "Web search",
		result: toolshared.NewToolResult("search results"),
	})

	result := r.Route(
		context.Background(), "web_search", nil,
	)
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.ForLLM != "search results" {
		t.Errorf(
			"expected 'search results', got %q",
			result.ForLLM,
		)
	}
}

func TestToolRouter_ShellRetryOnTransient(t *testing.T) {
	callCount := 0
	transientErr := toolshared.ErrorResult(
		`{"ok":false,"data":null,` +
			`"error":"timeout: deadline exceeded"}`,
	)
	successResult := toolshared.NewToolResult("ok")

	r := NewToolRouter(allEnabledCfg())
	r.RegisterSkill(&dynamicMockTool{
		name: "flaky_skill",
		desc: "Flaky skill",
		exec: func(
			_ context.Context,
			_ map[string]any,
		) *toolshared.ToolResult {
			callCount++
			if callCount == 1 {
				return transientErr
			}
			return successResult
		},
	})

	result := r.Route(
		context.Background(), "flaky_skill", nil,
	)

	if callCount != 2 {
		t.Errorf(
			"expected 2 calls (1 retry), got %d",
			callCount,
		)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.IsError {
		t.Error("retried result should succeed")
	}
	if result.ForLLM != "ok" {
		t.Errorf(
			"expected 'ok', got %q", result.ForLLM,
		)
	}
}

func TestToolRouter_NoRetryOnPermanent(t *testing.T) {
	callCount := 0
	r := NewToolRouter(allEnabledCfg())

	r.RegisterSkill(&dynamicMockTool{
		name: "perm_skill",
		desc: "Permanent error skill",
		exec: func(
			_ context.Context,
			_ map[string]any,
		) *toolshared.ToolResult {
			callCount++
			return toolshared.ErrorResult(
				`{"ok":false,"data":null,` +
					`"error":"invalid_params: bad"}`,
			)
		},
	})

	result := r.Route(
		context.Background(), "perm_skill", nil,
	)

	if callCount != 1 {
		t.Errorf(
			"expected 1 call (no retry), got %d",
			callCount,
		)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if !result.IsError {
		t.Error("permanent error should remain error")
	}
}

func TestToolRouter_Classify(t *testing.T) {
	r := setupRouter(allEnabledCfg())
	r.RegisterSkill(&mockTool{name: "web_fetch"})
	r.RegisterMCPTool("notion_api", "notion")

	tests := []struct {
		name string
		want ToolCategory
	}{
		{"search_memory", CategoryBrain},
		{"exec", CategoryBase},
		{"web_fetch", CategorySkill},
		{"notion_api", CategoryMCP},
	}

	for _, tt := range tests {
		got := r.Classify(tt.name)
		if got != tt.want {
			t.Errorf(
				"Classify(%q) = %v, want %v",
				tt.name, got, tt.want,
			)
		}
	}
	if r.Classify("unknown") != -1 {
		t.Error("unknown tool should return -1")
	}
}

func TestToolRouter_ToolDefinitions(t *testing.T) {
	r := setupRouter(singleDisabledCfg("exec"))
	r.RegisterSkill(&mockTool{
		name: "cron",
		desc: "Cron scheduler",
	})

	defs := r.ToolDefinitions()

	nameSet := make(map[string]bool)
	for _, d := range defs {
		nameSet[d.Function.Name] = true
	}

	// 3 BRAIN + 5 BASE (exec disabled) + 1 SKILL = 9
	if len(defs) != 9 {
		t.Errorf(
			"expected 9 definitions, got %d",
			len(defs),
		)
	}
	if nameSet["exec"] {
		t.Error(
			"disabled exec should not appear in defs",
		)
	}
	if !nameSet["search_memory"] {
		t.Error("BRAIN search_memory missing from defs")
	}
	if !nameSet["cron"] {
		t.Error("SKILL cron missing from defs")
	}
}

func TestToolRouter_CategoryString(t *testing.T) {
	tests := []struct {
		cat  ToolCategory
		want string
	}{
		{CategoryBrain, "brain"},
		{CategoryBase, "base"},
		{CategorySkill, "skill"},
		{CategoryMCP, "mcp"},
		{ToolCategory(-1), "unknown"},
	}
	for _, tt := range tests {
		got := tt.cat.String()
		if got != tt.want {
			t.Errorf(
				"ToolCategory(%d).String() = %q, want %q",
				tt.cat, got, tt.want,
			)
		}
	}
}
