package pika

// PIKA-V3: tool_router.go — unified tool routing (D-TOOL-CLASS)
//
// ToolRouter dispatches tool calls based on classification markers:
//   🧠 BRAIN — Go-native, always-on (search_memory, registry_write, clarify)
//   🔧 BASE  — conditional, config on/off (exec, read_file, etc.)
//   🛠️ SKILL — drop-in shell scripts (upstream skills loader)
//   🔌 MCP   — upstream Manager + security wrapper

import (
	"context"
	"fmt"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// ToolCategory classifies tools by routing behavior (D-TOOL-CLASS).
type ToolCategory int

const (
	// CategoryBrain — 🧠 always-on cognitive tools (Go-native).
	CategoryBrain ToolCategory = iota
	// CategoryBase — 🔧 conditional tools, toggled via config.
	CategoryBase
	// CategorySkill — 🛠️ drop-in shell-based tools.
	CategorySkill
	// CategoryMCP — 🔌 upstream MCP protocol tools.
	CategoryMCP
)

// String returns category label.
func (c ToolCategory) String() string {
	switch c {
	case CategoryBrain:
		return "brain"
	case CategoryBase:
		return "base"
	case CategorySkill:
		return "skill"
	case CategoryMCP:
		return "mcp"
	default:
		return "unknown"
	}
}

// MCPCaller abstracts upstream MCP Manager for testability.
// Real implementation wraps pkg/mcp.Manager + mcp_security.go (ТЗ-6b).
type MCPCaller interface {
	// CallTool invokes an MCP tool on the named server and returns result.
	CallTool(
		ctx context.Context,
		serverName, toolName string,
		arguments map[string]any,
	) (*toolshared.ToolResult, error)
}

// ToolRouter is the single dispatch point for all tool calls.
// Priority: 🧠 BRAIN → 🔧 BASE → 🛠️ SKILL → 🔌 MCP → error.
type ToolRouter struct {
	mu sync.RWMutex

	brainHandlers map[string]toolshared.Tool
	baseHandlers  map[string]toolshared.Tool
	skillHandlers map[string]toolshared.Tool

	mcpCaller    MCPCaller
	mcpToolNames map[string]string // toolName → serverName

	baseCfg *config.BaseToolsConfig
}

// NewToolRouter creates a ToolRouter with BASE tools config.
func NewToolRouter(
	baseCfg *config.BaseToolsConfig,
) *ToolRouter {
	return &ToolRouter{
		brainHandlers: make(map[string]toolshared.Tool),
		baseHandlers:  make(map[string]toolshared.Tool),
		skillHandlers: make(map[string]toolshared.Tool),
		mcpToolNames:  make(map[string]string),
		baseCfg:       baseCfg,
	}
}

// RegisterBrain registers a 🧠 BRAIN tool (always-on, unconditional).
func (r *ToolRouter) RegisterBrain(tool toolshared.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.brainHandlers[tool.Name()] = tool
}

// RegisterBase registers a 🔧 BASE tool (config-gated).
// Registration is unconditional — config is checked at routing
// and tool-list generation time.
func (r *ToolRouter) RegisterBase(tool toolshared.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.baseHandlers[tool.Name()] = tool
}

// RegisterSkill registers a 🛠️ SKILL tool (shell-based, drop-in).
func (r *ToolRouter) RegisterSkill(tool toolshared.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skillHandlers[tool.Name()] = tool
}

// SetMCPCaller sets the 🔌 MCP caller for MCP tool routing.
func (r *ToolRouter) SetMCPCaller(caller MCPCaller) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mcpCaller = caller
}

// RegisterMCPTool maps an MCP tool name to its server.
func (r *ToolRouter) RegisterMCPTool(
	toolName, serverName string,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mcpToolNames[toolName] = serverName
}

// Route dispatches a tool call to the appropriate handler.
// Priority: 🧠 BRAIN → 🔧 BASE → 🛠️ SKILL → 🔌 MCP → error.
func (r *ToolRouter) Route(
	ctx context.Context,
	toolName string,
	args map[string]any,
) *toolshared.ToolResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 1. BRAIN — always available, Go-native
	if handler, ok := r.brainHandlers[toolName]; ok {
		return handler.Execute(ctx, args)
	}

	// 2. BASE — conditional, check config
	if handler, ok := r.baseHandlers[toolName]; ok {
		if !r.baseCfg.IsBaseToolEnabled(toolName) {
			return toolshared.ErrorResult(fmt.Sprintf(
				"tool disabled by config: %s", toolName,
			))
		}
		return handler.Execute(ctx, args)
	}

	// 3. SKILL — upstream shell tools + envelope retry
	if handler, ok := r.skillHandlers[toolName]; ok {
		result := handler.Execute(ctx, args)
		return r.maybeRetryShell(ctx, handler, args, result)
	}

	// 4. MCP — upstream Manager + security wrapper
	if serverName, ok := r.mcpToolNames[toolName]; ok {
		return r.routeMCP(ctx, serverName, toolName, args)
	}

	return toolshared.ErrorResult(
		fmt.Sprintf("unknown tool: %s", toolName),
	)
}

// routeMCP delegates a call to the MCP caller.
func (r *ToolRouter) routeMCP(
	ctx context.Context,
	serverName, toolName string,
	args map[string]any,
) *toolshared.ToolResult {
	if r.mcpCaller == nil {
		return toolshared.ErrorResult(fmt.Sprintf(
			"pika/tool_router: MCP caller not configured "+
				"for tool %s", toolName,
		))
	}

	result, err := r.mcpCaller.CallTool(
		ctx, serverName, toolName, args,
	)
	if err != nil {
		return toolshared.ErrorResult(fmt.Sprintf(
			"pika/tool_router: MCP call failed: %s",
			err.Error(),
		))
	}
	return result
}

// maybeRetryShell retries a shell tool once if the envelope
// error is transient (timeout, exec_error). D-8 retry policy.
func (r *ToolRouter) maybeRetryShell(
	ctx context.Context,
	handler toolshared.Tool,
	args map[string]any,
	result *toolshared.ToolResult,
) *toolshared.ToolResult {
	if result == nil || !result.IsError {
		return result
	}
	// Parse the result content as envelope to check retryability.
	env := ParseEnvelope([]byte(result.ForLLM))
	if !env.IsRetryable() {
		return result
	}
	// Retry once
	return handler.Execute(ctx, args)
}

// EnabledToolNames returns names of currently available tools,
// grouped by category. Disabled BASE tools are excluded.
func (r *ToolRouter) EnabledToolNames() map[ToolCategory][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[ToolCategory][]string)

	for name := range r.brainHandlers {
		result[CategoryBrain] = append(
			result[CategoryBrain], name,
		)
	}
	for name := range r.baseHandlers {
		if r.baseCfg.IsBaseToolEnabled(name) {
			result[CategoryBase] = append(
				result[CategoryBase], name,
			)
		}
	}
	for name := range r.skillHandlers {
		result[CategorySkill] = append(
			result[CategorySkill], name,
		)
	}
	for name := range r.mcpToolNames {
		result[CategoryMCP] = append(
			result[CategoryMCP], name,
		)
	}

	return result
}

// ToolDefinitions returns tool schemas for LLM tools[] array.
// Only includes currently enabled tools. MCP tool definitions
// are managed externally by upstream Discovery — not included here.
func (r *ToolRouter) ToolDefinitions() []toolshared.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var defs []toolshared.ToolDefinition

	// 🧠 BRAIN — always included
	for _, tool := range r.brainHandlers {
		defs = append(defs, toDefinition(tool))
	}

	// 🔧 BASE — only if enabled in config
	for _, tool := range r.baseHandlers {
		if r.baseCfg.IsBaseToolEnabled(tool.Name()) {
			defs = append(defs, toDefinition(tool))
		}
	}

	// 🛠️ SKILL — always included
	for _, tool := range r.skillHandlers {
		defs = append(defs, toDefinition(tool))
	}

	return defs
}

// Classify returns the category of a tool by name.
// Returns -1 if tool is unknown.
func (r *ToolRouter) Classify(toolName string) ToolCategory {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if _, ok := r.brainHandlers[toolName]; ok {
		return CategoryBrain
	}
	if _, ok := r.baseHandlers[toolName]; ok {
		return CategoryBase
	}
	if _, ok := r.skillHandlers[toolName]; ok {
		return CategorySkill
	}
	if _, ok := r.mcpToolNames[toolName]; ok {
		return CategoryMCP
	}
	return -1
}

// toDefinition converts a Tool to a ToolDefinition.
func toDefinition(
	tool toolshared.Tool,
) toolshared.ToolDefinition {
	return toolshared.ToolDefinition{
		Type: "function",
		Function: toolshared.ToolFunctionDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Parameters(),
		},
	}
}
