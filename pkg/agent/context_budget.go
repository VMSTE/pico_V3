// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tokenizer"
)

// EstimateMessageTokens estimates the token count for a single message.
// Delegates to the shared tokenizer package for consistency across agent and seahorse.
func EstimateMessageTokens(msg providers.Message) int {
	return tokenizer.EstimateMessageTokens(msg)
}

// EstimateToolDefsTokens estimates the total token cost of tool definitions
// as they appear in the LLM request. Delegates to the shared tokenizer package.
func EstimateToolDefsTokens(defs []providers.ToolDefinition) int {
	return tokenizer.EstimateToolDefsTokens(defs)
}

// isOverContextBudget checks whether the assembled messages plus tool definitions
// and output reserve would exceed the model's context window. This enables
// proactive compression before calling the LLM, rather than reacting to 400 errors.
func isOverContextBudget(
	contextWindow int,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	maxTokens int,
) bool {
	msgTokens := 0
	for _, m := range messages {
		msgTokens += EstimateMessageTokens(m)
	}

	toolTokens := EstimateToolDefsTokens(toolDefs)
	total := msgTokens + toolTokens + maxTokens

	return total > contextWindow
}
