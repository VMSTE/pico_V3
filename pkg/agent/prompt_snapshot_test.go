package agent

import "testing"

// Волна 124 (срез В): расклад по маркерам; без маркеров — всё core.
func TestPromptTokenBreakdown(t *testing.T) {
	core := "core part here            " // 25 chars
	brief := "--- MEMORY BRIEF ---\nbrief text      "
	trail := "--- TRAIL ---\ntrail entries "
	plan := "--- ACTIVE_PLAN ---\nplan    "
	unknown := "--- DEGRADATION ---\nx   "
	sp := core + brief + trail + plan + unknown

	tokens := promptTokenBreakdown(sp)
	if tokens["core"] != len(core)/4 {
		t.Errorf("core = %d, want %d", tokens["core"], len(core)/4)
	}
	if tokens["brief"] != len(brief)/4 {
		t.Errorf("brief = %d, want %d", tokens["brief"], len(brief)/4)
	}
	if tokens["trail"] != len(trail)/4 {
		t.Errorf("trail = %d, want %d", tokens["trail"], len(trail)/4)
	}
	if tokens["plan"] != len(plan)/4 {
		t.Errorf("plan = %d, want %d", tokens["plan"], len(plan)/4)
	}
	if tokens["context"] != len(unknown)/4 {
		t.Errorf("context = %d, want %d", tokens["context"], len(unknown)/4)
	}

	// Без маркеров — всё core (как волна 92).
	plain := "no markers here at all"
	got := promptTokenBreakdown(plain)
	if got["core"] != len(plain)/4 || got["brief"] != 0 {
		t.Errorf("plain prompt: %+v", got)
	}
}
