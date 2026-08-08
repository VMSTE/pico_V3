package pika

import (
	"context"
	"testing"
)

// Р-5 шаг 3: укрепление критических путей — нормализация (точки в пути)
// и рекурсивные шаблоны **.

func pathTestGate(
	sender *effectMockSender, criticalPaths []string,
) *ConfirmGate {
	cfg := testDangerousOpsConfig()
	cfg.Security.DangerousOps.CriticalPaths = criticalPaths
	return ConfirmGateFactory(
		cfg, sender, &mockHealthState{state: StateHealthy},
	)
}

func writeReq(path string) *ConfirmApprovalRequest {
	return &ConfirmApprovalRequest{
		Tool:      "write_file",
		Arguments: map[string]any{"path": path, "content": "x"},
	}
}

// Критерий 2 задачи Р-5: запись в папку с промтами через путь с точками
// перехватывается.
func TestCriticalPath_DottedPath(t *testing.T) {
	cases := []string{
		"/workspace/./prompt/CORE.md",
		"/workspace/x/../prompt/CORE.md",
		"/workspace//prompt/CORE.md",
	}
	for _, p := range cases {
		sender := &effectMockSender{}
		sender.approved = true
		cg := pathTestGate(sender, []string{"/workspace/prompt/*"})
		_, err := cg.ApproveTool(context.Background(), writeReq(p))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !sender.called {
			t.Errorf("dotted path %q slipped past critical path check", p)
		}
	}
}

// Рекурсивный шаблон ** — любое число сегментов, включая глубокое.
func TestCriticalPath_DoubleStar(t *testing.T) {
	sender := &effectMockSender{}
	sender.approved = true
	cg := pathTestGate(sender, []string{"**/workspace/prompts/**"})

	_, err := cg.ApproveTool(
		context.Background(),
		writeReq("/home/user/workspace/prompts/sub/dir/x.md"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sender.called {
		t.Error("** pattern must match deep nesting")
	}

	sender2 := &effectMockSender{}
	cg2 := pathTestGate(sender2, []string{"**/workspace/prompts/**"})
	d, err := cg2.ApproveTool(
		context.Background(), writeReq("/home/user/workspace/other/x.md"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Approved || sender2.called {
		t.Error("non-matching path must be silent")
	}
}

// Одиночная * по-прежнему не пересекает разделители (семантика
// filepath.Match сохранена для старых шаблонов).
func TestCriticalPath_SingleStarNoCrossing(t *testing.T) {
	sender := &effectMockSender{}
	cg := pathTestGate(sender, []string{"/workspace/prompt/*"})
	d, err := cg.ApproveTool(
		context.Background(), writeReq("/workspace/prompt/sub/x.md"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.Approved || sender.called {
		t.Error("single * must not cross path separators")
	}
}

// Относительный путь тоже проверяется (пробуем и с ведущим /).
func TestCriticalPath_RelativePath(t *testing.T) {
	sender := &effectMockSender{}
	sender.approved = true
	cg := pathTestGate(sender, []string{"**/config.json"})
	_, err := cg.ApproveTool(
		context.Background(), writeReq("data/config.json"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sender.called {
		t.Error("relative path to config.json must be caught by **")
	}
}
