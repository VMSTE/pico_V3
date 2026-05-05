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
		}, nil
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

var (
	_ LLMInterceptor = (*outputGateAdapter)(nil)
	_ LLMInterceptor = (*toolGuardAdapter)(nil)
	_ ToolApprover   = (*confirmGateAdapter)(nil)
	_ EventObserver  = (*progressAdapter)(nil)
)
