// PIKA-V3: hook_pika.go — Adapters bridging pkg/pika hooks
// to pkg/agent hook interfaces. Avoids circular import.
// D-136a: upstream pipeline + builtin hooks.
//
// Each adapter wraps a pika component and implements an agent
// hook interface (LLMInterceptor, ToolApprover, EventObserver).
// Local pika types are converted to agent types at the boundary.

package agent

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
)

// --- Output Gate Adapter (LLMInterceptor) ---

// outputGateAdapter wraps pika.OutputGate as agent.LLMInterceptor.
type outputGateAdapter struct {
	gate *pika.OutputGate
}

func (a *outputGateAdapter) BeforeLLM(
	_ context.Context,
	req *LLMHookRequest,
) (*LLMHookRequest, HookDecision, error) {
	return req, HookDecision{
		Action: HookActionContinue,
	}, nil
}

func (a *outputGateAdapter) AfterLLM(
	_ context.Context,
	resp *LLMHookResponse,
) (*LLMHookResponse, HookDecision, error) {
	if resp == nil || resp.Response == nil {
		return resp, HookDecision{
			Action: HookActionContinue,
		}, nil
	}
	pikaResp := &pika.OutputGateLLMResponse{
		Content: resp.Response.Content,
	}
	d := a.gate.AfterLLM(pikaResp)
	return resp, HookDecision{
		Action: HookAction(d.Action),
		Reason: d.Reason,
	}, nil
}

// --- ToolGuard Adapter (LLMInterceptor) ---

// toolGuardAdapter wraps pika.ToolGuard as agent.LLMInterceptor.
type toolGuardAdapter struct {
	guard *pika.ToolGuard
}

func (a *toolGuardAdapter) BeforeLLM(
	_ context.Context,
	req *LLMHookRequest,
) (*LLMHookRequest, HookDecision, error) {
	return req, HookDecision{
		Action: HookActionContinue,
	}, nil
}

func (a *toolGuardAdapter) AfterLLM(
	_ context.Context,
	resp *LLMHookResponse,
) (*LLMHookResponse, HookDecision, error) {
	if resp == nil || resp.Response == nil {
		return resp, HookDecision{
			Action: HookActionContinue,
		}, nil
	}
	pikaResp := &pika.ToolGuardLLMResponse{
		Content:      resp.Response.Content,
		HasToolCalls: len(resp.Response.ToolCalls) > 0,
	}
	d := a.guard.AfterLLM(pikaResp)
	return resp, HookDecision{
		Action: HookAction(d.Action),
		Reason: d.Reason,
	}, nil
}

// --- ConfirmGate Adapter (ToolApprover) ---

// confirmGateAdapter wraps pika.ConfirmGate as agent.ToolApprover.
type confirmGateAdapter struct {
	gate *pika.ConfirmGate
}

func (a *confirmGateAdapter) ApproveTool(
	ctx context.Context,
	req *ToolApprovalRequest,
) (ApprovalDecision, error) {
	pikaReq := &pika.ConfirmApprovalRequest{
		Tool:      req.Tool,
		Arguments: req.Arguments,
	}
	d, err := a.gate.ApproveTool(ctx, pikaReq)
	if err != nil {
		return ApprovalDecision{
			Approved: false,
			Reason:   err.Error(),
		}, err
	}
	return ApprovalDecision{
		Approved: d.Approved,
		Reason:   d.Reason,
	}, nil
}

// --- Progress Adapter (EventObserver) ---

// progressAdapter wraps pika.ProgressObserver as agent.EventObserver.
type progressAdapter struct {
	observer *pika.ProgressObserver
}

func (a *progressAdapter) OnEvent(
	ctx context.Context,
	evt Event,
) error {
	// PIKA-V3 (Р-1): без отправителя адаптер монтируется как no-op.
	// Nil-guard обязателен: наблюдатель вызывается из горутины
	// HookManager.dispatchEvents, паника там уронит процесс.
	if a.observer == nil {
		return nil
	}
	var pikaEvt pika.ProgressEvent
	switch evt.Kind {
	case EventKindToolExecStart:
		p, ok := evt.Payload.(ToolExecStartPayload)
		if !ok {
			return nil
		}
		pikaEvt = pika.ProgressEvent{
			Kind: pika.ProgressEventToolExecStart,
			Payload: pika.ToolExecStartPayload{
				Tool: p.Tool,
			},
		}
	case EventKindToolExecEnd:
		p, ok := evt.Payload.(ToolExecEndPayload)
		if !ok {
			return nil
		}
		pikaEvt = pika.ProgressEvent{
			Kind: pika.ProgressEventToolExecEnd,
			Payload: pika.ToolExecEndPayload{
				Tool:     p.Tool,
				Duration: p.Duration,
			},
		}
	case EventKindTurnEnd:
		pikaEvt = pika.ProgressEvent{
			Kind:    pika.ProgressEventTurnEnd,
			Payload: pika.TurnEndPayload{},
		}
	default:
		return nil
	}
	return a.observer.OnEvent(ctx, pikaEvt)
}

// --- Compile-time interface checks ---

// PIKA-V3: autoEventAdapter wraps pika.AutoEventHandler as agent.EventObserver.
// Translates EventKindToolExecEnd -> HandleToolResult (D-136a, TZ-v2-8i).
type autoEventAdapter struct {
	handler *pika.AutoEventHandler
}

func (a *autoEventAdapter) OnEvent(ctx context.Context, evt Event) error {
	if evt.Kind != EventKindToolExecEnd {
		return nil
	}
	p, ok := evt.Payload.(ToolExecEndPayload)
	if !ok {
		return nil
	}
	return a.handler.HandleToolResult(ctx, p.Tool, "", p.IsError, "", "")
}

var (
	_ LLMInterceptor = (*outputGateAdapter)(nil)
	_ LLMInterceptor = (*toolGuardAdapter)(nil)
	_ ToolApprover   = (*confirmGateAdapter)(nil)
	_ EventObserver  = (*progressAdapter)(nil)
	_ EventObserver  = (*autoEventAdapter)(nil)
)

// --- Builtin hook registration (Р-1, D-AUDIT-49) ---

// Имена builtin-хуков Пики — ключи в hooks.builtins конфига.
const (
	hookNameOutputGate  = "pika.output_gate"
	hookNameToolGuard   = "pika.toolguard"
	hookNameConfirmGate = "pika.confirm_gate"
	hookNameProgress    = "pika.progress"
)

// registerPikaBuiltinHooks регистрирует фабрики хуков Пики в глобальном
// реестре builtin-хуков. Вызывается из NewAgentLoop после
// resolveContextManager. Монтирование — лениво, в loadConfiguredHooks
// при старте Run(), по флагам hooks.builtins.<имя>.enabled.
//
// Почему реестр, а не прямой Mount (D-AUDIT-49): loadConfiguredHooks
// падает на включённом, но незарегистрированном builtin и обрывает
// Run() — а hooks.enabled: true уже стоит в config.example.json.
func registerPikaBuiltinHooks(al *AgentLoop, cfg *config.Config) {
	if al == nil || cfg == nil {
		return
	}

	registerBuiltinOnce(hookNameOutputGate, func(
		_ context.Context, _ config.BuiltinHookConfig,
	) (any, error) {
		return &outputGateAdapter{gate: pika.OutputGateFactory(cfg)}, nil
	})

	registerBuiltinOnce(hookNameToolGuard, func(
		_ context.Context, _ config.BuiltinHookConfig,
	) (any, error) {
		return &toolGuardAdapter{
			guard: pika.ToolGuardFactory(cfg, pikaPlanGetter(al)),
		}, nil
	})

	registerBuiltinOnce(hookNameConfirmGate, func(
		_ context.Context, _ config.BuiltinHookConfig,
	) (any, error) {
		var health pika.SystemStateProvider
		if al.telemetry != nil {
			health = al.telemetry
		}
		// Р-5: живой отправитель менеджера (health.reporting.manager_*).
		// Без адреса sender=nil: гейт разоружён при пустой таблице
		// dangerous_ops (дефолт) и fail-closed после её заполнения.
		return &confirmGateAdapter{
			gate: pika.ConfirmGateFactory(
				cfg, pika.NewManagerSender(al.bus, cfg), health,
			),
		}, nil
	})

	registerBuiltinOnce(hookNameProgress, func(
		_ context.Context, _ config.BuiltinHookConfig,
	) (any, error) {
		// Р-3: живой отправитель, если настроен адрес менеджера
		// (health.reporting.manager_*). Без адреса — no-op адаптер:
		// nil-guard в OnEvent защищает горутину наблюдателя от паники.
		sender := pika.NewManagerSender(al.bus, cfg)
		if sender == nil {
			logger.WarnCF(
				"hooks",
				"pika.progress: no sender configured, mounting as no-op",
				nil,
			)
			return &progressAdapter{}, nil
		}
		return &progressAdapter{
			observer: pika.ProgressObserverFactory(cfg, sender),
		}, nil
	})
}

// registerBuiltinOnce регистрирует фабрику, если имя ещё не занято.
// Реестр глобальный, а NewAgentLoop вызывается повторно (тесты).
func registerBuiltinOnce(name string, factory BuiltinHookFactory) {
	if _, ok := lookupBuiltinHook(name); ok {
		return
	}
	if err := RegisterBuiltinHook(name, factory); err != nil {
		logger.ErrorCF(
			"hooks",
			"failed to register builtin hook",
			map[string]any{"hook": name, "error": err.Error()},
		)
	}
}

// pikaPlanGetter возвращает ActivePlanGetter из PikaContextManager,
// если активен именно он; иначе nil (ToolGuard тогда всегда continue).
func pikaPlanGetter(al *AgentLoop) pika.ActivePlanGetter {
	if al == nil {
		return nil
	}
	adapter, ok := al.contextManager.(*pikaContextManagerAdapter)
	if !ok || adapter == nil || adapter.cm == nil {
		return nil
	}
	return adapter.cm.GetPlanStore()
}
