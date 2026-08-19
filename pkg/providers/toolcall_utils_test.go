package providers

import "testing"

func TestNormalizeToolCall_PreservesExtraContentGoogleThoughtSignature(t *testing.T) {
	tc := NormalizeToolCall(ToolCall{
		ID:        "call_1",
		Name:      "search",
		Arguments: map[string]any{"q": "pico"},
		ExtraContent: &ExtraContent{
			Google: &GoogleExtra{ThoughtSignature: "sig-1"},
		},
	})

	if tc.ThoughtSignature != "sig-1" {
		t.Fatalf("ThoughtSignature = %q, want sig-1", tc.ThoughtSignature)
	}
	if tc.Function == nil {
		t.Fatal("Function is nil")
	}
	if tc.Function.ThoughtSignature != "sig-1" {
		t.Fatalf("Function.ThoughtSignature = %q, want sig-1", tc.Function.ThoughtSignature)
	}
}

// Волна 84 (бой 20 авг): Type="function" обязателен для эха tool_calls.
func TestNormalizeToolCall_BackfillsTypeFunction(t *testing.T) {
	tc := NormalizeToolCall(ToolCall{
		ID:        "call_1",
		Name:      "search",
		Arguments: map[string]any{"q": "x"},
	})
	if tc.Type != "function" {
		t.Fatalf("Type = %q, want function", tc.Type)
	}
	// явно заданный type не затирается
	tc2 := NormalizeToolCall(ToolCall{ID: "c2", Name: "s", Type: "function"})
	if tc2.Type != "function" {
		t.Fatalf("explicit Type overwritten: %q", tc2.Type)
	}
}
