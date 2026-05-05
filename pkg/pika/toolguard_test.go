package pika

import (
	"testing"
)

// mockPlanGetter implements ActivePlanGetter for tests.
type mockPlanGetter struct {
	plan string
}

func (m *mockPlanGetter) GetActivePlan() string {
	return m.plan
}

// --- Test: activePlan + text without tools → modify ---
func TestToolGuard_ActivePlanTextNoTools_Modify(t *testing.T) {
	pg := &mockPlanGetter{plan: "Step 1: run diagnostics"}
	tg := ToolGuardFactory(nil, pg)

	resp := &ToolGuardLLMResponse{
		Content:      "I'll help you with that.",
		HasToolCalls: false,
	}

	decision := tg.AfterLLM(resp)
	if decision.Action != HookActionModify {
		t.Errorf(
			"action = %q, want %q",
			decision.Action, HookActionModify,
		)
	}
	if decision.Reason == "" {
		t.Error("reason should not be empty on modify")
	}
}

// --- Test: activePlan + tool calls → continue ---
func TestToolGuard_ActivePlanWithToolCalls_Continue(
	t *testing.T,
) {
	pg := &mockPlanGetter{plan: "Step 1: run diagnostics"}
	tg := ToolGuardFactory(nil, pg)

	resp := &ToolGuardLLMResponse{
		Content:      "",
		HasToolCalls: true,
	}

	decision := tg.AfterLLM(resp)
	if decision.Action != HookActionContinue {
		t.Errorf(
			"action = %q, want continue",
			decision.Action,
		)
	}
}

// --- Test: no plan + text without tools → continue ---
func TestToolGuard_NoPlan_Continue(t *testing.T) {
	pg := &mockPlanGetter{plan: ""}
	tg := ToolGuardFactory(nil, pg)

	resp := &ToolGuardLLMResponse{
		Content:      "I'll help you with that.",
		HasToolCalls: false,
	}

	decision := tg.AfterLLM(resp)
	if decision.Action != HookActionContinue {
		t.Errorf(
			"action = %q, want continue",
			decision.Action,
		)
	}
}

// --- Test: retry exhausted → continue ---
func TestToolGuard_RetryExhausted_Continue(t *testing.T) {
	pg := &mockPlanGetter{plan: "Step 1: run diagnostics"}
	tg := ToolGuardFactory(nil, pg)

	resp := &ToolGuardLLMResponse{
		Content:      "Here is the result.",
		HasToolCalls: false,
	}

	// First call — should modify (retry #1)
	d1 := tg.AfterLLM(resp)
	if d1.Action != HookActionModify {
		t.Fatalf(
			"first call: action = %q, want modify",
			d1.Action,
		)
	}

	// Second call — retry exhausted, should continue
	d2 := tg.AfterLLM(resp)
	if d2.Action != HookActionContinue {
		t.Errorf(
			"second call: action = %q, want continue",
			d2.Action,
		)
	}
}

// --- Test: empty response content → continue ---
func TestToolGuard_EmptyResponse_Continue(t *testing.T) {
	pg := &mockPlanGetter{plan: "Step 1: run diagnostics"}
	tg := ToolGuardFactory(nil, pg)

	resp := &ToolGuardLLMResponse{
		Content:      "",
		HasToolCalls: false,
	}

	decision := tg.AfterLLM(resp)
	if decision.Action != HookActionContinue {
		t.Errorf(
			"action = %q, want continue",
			decision.Action,
		)
	}
}

// --- Test: nil planGetter → continue (guard) ---
func TestToolGuard_NilPlanGetter_Continue(t *testing.T) {
	tg := ToolGuardFactory(nil, nil)

	resp := &ToolGuardLLMResponse{
		Content:      "Some text without tools.",
		HasToolCalls: false,
	}

	decision := tg.AfterLLM(resp)
	if decision.Action != HookActionContinue {
		t.Errorf(
			"action = %q, want continue",
			decision.Action,
		)
	}
}

// --- Test: nil response → continue ---
func TestToolGuard_NilResponse_Continue(t *testing.T) {
	pg := &mockPlanGetter{plan: "Step 1: do something"}
	tg := ToolGuardFactory(nil, pg)

	decision := tg.AfterLLM(nil)
	if decision.Action != HookActionContinue {
		t.Errorf(
			"action = %q, want continue",
			decision.Action,
		)
	}
}

// --- Test: ResetTurn re-enables retry ---
func TestToolGuard_ResetTurn(t *testing.T) {
	pg := &mockPlanGetter{plan: "Step 1: do something"}
	tg := ToolGuardFactory(nil, pg)

	resp := &ToolGuardLLMResponse{
		Content:      "text without tools",
		HasToolCalls: false,
	}

	// First call — modify
	d1 := tg.AfterLLM(resp)
	if d1.Action != HookActionModify {
		t.Fatalf(
			"first: action = %q, want modify", d1.Action,
		)
	}

	// Second call — exhausted → continue
	d2 := tg.AfterLLM(resp)
	if d2.Action != HookActionContinue {
		t.Fatalf(
			"exhausted: action = %q, want continue",
			d2.Action,
		)
	}

	// Reset turn
	tg.ResetTurn()

	// After reset — should modify again
	d3 := tg.AfterLLM(resp)
	if d3.Action != HookActionModify {
		t.Errorf(
			"after reset: action = %q, want modify",
			d3.Action,
		)
	}
}
