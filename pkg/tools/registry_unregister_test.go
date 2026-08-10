package tools

import "testing"

// D-AUDIT-72: Unregister удаляет тул и честно возвращает true/false.
func TestToolRegistry_Unregister(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockRegistryTool{
		name: "x", desc: "x", params: map[string]any{}, result: SilentResult("ok"),
	})
	if !r.Unregister("x") {
		t.Fatal("expected true for existing tool")
	}
	if r.Unregister("x") {
		t.Fatal("expected false for missing tool")
	}
	if _, ok := r.Get("x"); ok {
		t.Fatal("unregistered tool should not be gettable")
	}
}
