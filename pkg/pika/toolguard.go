package pika

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// PIKA-V3: ToolGuard — builtin AfterLLM hook (D-136a).
// Detects missing tool calls when ACTIVE_PLAN is active.
// Injects reminder and retries once. Disableable via config
// (hooks.builtins.toolguard.enabled).

// ActivePlanGetter provides access to the current ACTIVE_PLAN.
// Implemented by PikaContextManager (context_manager.go).
type ActivePlanGetter interface {
	GetActivePlan() string
}

// ToolGuard is an LLMInterceptor (AfterLLM) that detects when
// the model returns text instead of a tool call while an
// ACTIVE_PLAN is present. It returns HookActionModify so the
// upstream pipeline injects a system-message reminder and
// retries the LLM call. Max 1 retry per turn.
type ToolGuard struct {
	maxRetries int              // default 1
	retryCount int              // per-turn, reset via ResetTurn()
	planGetter ActivePlanGetter // source of current ACTIVE_PLAN
}

// compile-time interface check
var _ agent.LLMInterceptor = (*ToolGuard)(nil)

// ToolGuardFactory creates a new ToolGuard hook.
// cfg is accepted for future config-driven tuning.
// Registration: hookManager.Mount(agent.NamedHook(
//
//	"pika.toolguard", tg)) in instance.go (ТЗ-4a).
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

// BeforeLLM is a no-op — ToolGuard only acts after LLM.
func (tg *ToolGuard) BeforeLLM(
	_ context.Context,
	req *agent.LLMHookRequest,
) (*agent.LLMHookRequest, agent.HookDecision, error) {
	return req, agent.HookDecision{
		Action: agent.HookActionContinue,
	}, nil
}

// AfterLLM detects a missing tool call when ACTIVE_PLAN is
// present. Decision matrix:
//
//	planGetter == nil        → continue
//	plan == ""               → continue (no active plan)
//	resp nil / empty content → continue
//	ToolCalls present        → continue (model is executing)
//	retryCount >= max        → continue (exhausted)
//	Otherwise                → modify (inject reminder, retry)
func (tg *ToolGuard) AfterLLM(
	_ context.Context,
	resp *agent.LLMHookResponse,
) (*agent.LLMHookResponse, agent.HookDecision, error) {
	continueDecision := agent.HookDecision{
		Action: agent.HookActionContinue,
	}

	// Guard: nil planGetter
	if tg.planGetter == nil {
		return resp, continueDecision, nil
	}

	plan := tg.planGetter.GetActivePlan()
	if plan == "" {
		return resp, continueDecision, nil
	}

	// Plan is active — check response
	if resp == nil || resp.Response == nil {
		return resp, continueDecision, nil
	}

	if len(resp.Response.ToolCalls) > 0 {
		return resp, continueDecision, nil
	}

	if resp.Response.Content == "" {
		return resp, continueDecision, nil
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
		return resp, continueDecision, nil
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

	return resp, agent.HookDecision{
		Action: agent.HookActionModify,
		Reason: "PLAN active. Execute pending tool call, " +
			"don't respond with text.",
	}, nil
}
