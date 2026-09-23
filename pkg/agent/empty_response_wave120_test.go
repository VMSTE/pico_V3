package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// Волна 120 (срез 4): контракт классификатора пустого ответа.
// Пусто = нет content, tool calls и reasoning одновременно.
func TestIsEmptyLLMResponse(t *testing.T) {
	if !isEmptyLLMResponse(nil) {
		t.Error("nil response must be empty")
	}
	if !isEmptyLLMResponse(&providers.LLMResponse{}) {
		t.Error("zero response must be empty")
	}
	if isEmptyLLMResponse(&providers.LLMResponse{Content: "x"}) {
		t.Error("content present — not empty")
	}
	if isEmptyLLMResponse(&providers.LLMResponse{Reasoning: "thinking"}) {
		t.Error("reasoning present — not empty")
	}
	if isEmptyLLMResponse(&providers.LLMResponse{ReasoningContent: "thought"}) {
		t.Error("reasoning content present — not empty")
	}
	if isEmptyLLMResponse(&providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{ID: "call_1"}},
	}) {
		t.Error("tool calls present — not empty")
	}
}
