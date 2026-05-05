package pika

import (
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// PIKA-V3: ToolGuard — builtin AfterLLM hook (D-136a).
// Detects missing tool calls when ACTIVE_PLAN is active.
// Injects reminder and retries once. Disableable via config
// (hooks.builtins.toolguard.enabled).
//
// NOTE: pkg/pika cannot import pkg/agent (import cycle via
// context_pika.go). Hook types are defined locally here.
// Wiring adapter in instance.go (ТЗ-4a) converts between
// these types and agent.LLMInterceptor / agent.LLMHookResponse.

// HookAction represents the action a hook wants the pipeline
// to take. Mirrors agent.HookAction values.
type HookAction string

const (
	// HookActionContinue lets the pipeline proceed as-is.
	HookActionContinue HookAction = "continue"
	// HookActionModify asks the pipeline to inject Reason
	// as a system message and retry the LLM call.
	HookActionModify HookAction = "modify"
)

// HookDecision is the hook's verdict after inspecting a
// response. Mirrors agent.HookDecision.
type HookDecision struct {
	Action HookAction
	Reason string
}

// ToolGuardLLMResponse is a minimal view of the LLM response
// that ToolGuard needs. Wiring code in instance.go populates
// it from agent.LLMHookResponse.
type ToolGuardLLMResponse struct {
	Content      string
	HasToolCalls bool
}

// ActivePlanGetter provides access to the current ACTIVE_PLAN.
// Implemented by PikaContextManager (context_manager.go).
type ActivePlanGetter interface {
	GetActivePlan() string
}

// ToolGuard is a builtin AfterLLM hook that detects when the
// model returns text instead of a tool call while an
// ACTIVE_PLAN is present. It returns HookActionModify so the
// upstream pipeline injects a system-message reminder and
// retries the LLM call. Max 1 retry per turn.
type ToolGuard struct {
	maxRetries int              // default 1
	retryCount int              // per-turn, reset via ResetTurn()
	planGetter ActivePlanGetter // source of current ACTIVE_PLAN
}

// ToolGuardFactory creates a new ToolGuard hook.
// cfg is accepted for future config-driven tuning.
// Registration: hookManager.Mount(agent.NamedHook(
//
//	"pika.toolguard", adapter)) in instance.go (ТЗ-4a).
func ToolGuardFactory(
	_ *config.Config,
	planGetter ActivePlanGetter,
) *ToolGuard {
	return &ToolGuard{
		maxRetries: 1,
		planGetter: planGetter,
	}
}

// ResetTurn resets the per-turn retry counter.
// Must be called at the beginning of each new user turn.
func (tg *ToolGuard) ResetTurn() {
	tg.retryCount = 0
}

// AfterLLM inspects the LLM response and decides whether to
// request a retry. Decision matrix:
//
//	planGetter == nil        → continue
//	plan == ""               → continue (no active plan)
//	resp nil                 → continue
//	HasToolCalls             → continue (model is executing)
//	Content == ""            → continue (empty response)
//	retryCount >= max        → continue (exhausted)
//	Otherwise                → modify (inject reminder, retry)
func (tg *ToolGuard) AfterLLM(
	resp *ToolGuardLLMResponse,
) HookDecision {
	cont := HookDecision{Action: HookActionContinue}

	// Guard: nil planGetter
	if tg.planGetter == nil {
		return cont
	}

	plan := tg.planGetter.GetActivePlan()
	if plan == "" {
		return cont
	}

	// Plan is active — check response
	if resp == nil {
		return cont
	}

	if resp.HasToolCalls {
		return cont
	}

	if resp.Content == "" {
		return cont
	}

	if tg.retryCount >= tg.maxRetries {
		logger.WarnCF(
			"toolguard",
			"PLAN active but model returned text "+
				"after max retries — passing through",
			map[string]any{
				"retries": tg.retryCount,
			},
		)
		return cont
	}

	// Model returned text instead of tool call with active plan
	tg.retryCount++
	logger.WarnCF(
		"toolguard",
		"PLAN active, no tool calls — requesting retry",
		map[string]any{
			"retry": tg.retryCount,
		},
	)

	return HookDecision{
		Action: HookActionModify,
		Reason: "PLAN active. Execute pending tool call, " +
			"don't respond with text.",
	}
}
