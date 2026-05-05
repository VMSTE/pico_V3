// PIKA-V3: output_gate.go — Output Gate builtin hook
// (LLMInterceptor, D-136a). Enforces message length limits.
// Returns HookActionModify to trigger retry with compression.
//
// HookAction, HookDecision defined in toolguard.go (same pkg).
// Wiring adapter in pkg/agent/hook_pika.go converts to
// agent.LLMInterceptor.

package pika

import (
	"fmt"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// OutputGateLLMResponse is a minimal view of the LLM response.
// Populated by the adapter in pkg/agent/hook_pika.go.
type OutputGateLLMResponse struct {
	Content string
}

// OutputGate enforces message length limits on LLM responses.
// If response exceeds maxChars, requests retry with compression
// instruction. After maxRetries exhausted, passes through as-is.
type OutputGate struct {
	maxChars   int
	maxRetries int
	retryCount int
}

// OutputGateFactory creates an OutputGate from config.
// Config path: agents.<id>.output_gate (OutputGateConfig).
func OutputGateFactory(
	cfg *config.Config,
) *OutputGate {
	maxChars := 3500
	maxRetries := 3

	rc := cfg.ResolveAgentConfig("main")
	if rc.OutputGate.MaxChars > 0 {
		maxChars = rc.OutputGate.MaxChars
	}
	if rc.OutputGate.MaxRetries > 0 {
		maxRetries = rc.OutputGate.MaxRetries
	}

	return &OutputGate{
		maxChars:   maxChars,
		maxRetries: maxRetries,
	}
}

// ResetTurn resets per-turn retry counter.
// Must be called at the beginning of each new user turn.
func (og *OutputGate) ResetTurn() {
	og.retryCount = 0
}

// AfterLLM checks response length and decides whether to
// request a retry. Decision matrix:
//
//	maxChars <= 0            → continue (disabled)
//	resp nil                 → continue
//	Content <= maxChars      → continue (within limit)
//	retryCount >= maxRetries → continue (exhausted)
//	Otherwise                → modify (inject compression msg)
func (og *OutputGate) AfterLLM(
	resp *OutputGateLLMResponse,
) HookDecision {
	cont := HookDecision{Action: HookActionContinue}
	if og.maxChars <= 0 || resp == nil {
		return cont
	}
	cLen := len(resp.Content)
	if cLen <= og.maxChars {
		return cont
	}
	if og.retryCount >= og.maxRetries {
		logger.WarnCF(
			"output_gate",
			"Response exceeds limit after max retries",
			map[string]any{
				"chars":   cLen,
				"limit":   og.maxChars,
				"retries": og.retryCount,
			},
		)
		return cont
	}
	og.retryCount++
	return HookDecision{
		Action: HookActionModify,
		Reason: fmt.Sprintf(
			"Output %d chars > limit %d. "+
				"Compress, keep key info. "+
				"Retry %d/%d.",
			cLen, og.maxChars,
			og.retryCount, og.maxRetries,
		),
	}
}
