package pika

import "testing"

func TestExtractActivePlan_NoPlan(t *testing.T) {
	result := ExtractActivePlan("no plan tags here")
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestExtractActivePlan_EmptyInput(t *testing.T) {
	result := ExtractActivePlan("")
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestExtractActivePlan_SinglePlan(t *testing.T) {
	input := "reasoning text <plan>Step 1\nStep 2</plan> more text"
	result := ExtractActivePlan(input)
	expected := "Step 1\nStep 2"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestExtractActivePlan_MultiplePlans_LastWins(t *testing.T) {
	input := "<plan>old plan</plan> thinking... <plan>new plan</plan>"
	result := ExtractActivePlan(input)
	if result != "new plan" {
		t.Errorf("expected %q, got %q", "new plan", result)
	}
}

func TestExtractActivePlan_TrimWhitespace(t *testing.T) {
	input := "<plan>  trimmed  </plan>"
	result := ExtractActivePlan(input)
	if result != "trimmed" {
		t.Errorf("expected %q, got %q", "trimmed", result)
	}
}

func TestExtractActivePlan_MultiLine(t *testing.T) {
	input := `<plan>
1. Do X
2. Do Y
3. Verify
</plan>`
	result := ExtractActivePlan(input)
	if result == "" {
		t.Error("expected non-empty plan")
	}
	if result != "1. Do X\n2. Do Y\n3. Verify" {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestActivePlanStore_SetGet(t *testing.T) {
	store := NewActivePlanStore()
	if got := store.GetActivePlan(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	store.SetActivePlan("my plan")
	if got := store.GetActivePlan(); got != "my plan" {
		t.Errorf("expected %q, got %q", "my plan", got)
	}
}

func TestActivePlanStore_Overwrite(t *testing.T) {
	store := NewActivePlanStore()
	store.SetActivePlan("plan A")
	store.SetActivePlan("plan B")
	if got := store.GetActivePlan(); got != "plan B" {
		t.Errorf("expected %q, got %q", "plan B", got)
	}
}
