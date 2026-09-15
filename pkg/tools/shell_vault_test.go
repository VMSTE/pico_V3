package tools

import (
	"context"
	"strings"
	"testing"
)

// Волна 108 (D-AUDIT-131): exec deny для .vault — мутации закрытой зоны
// через терминал блокируются (чтение разрешено).

func TestShellTool_VaultDeny(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}
	ctx := context.Background()

	for _, cmd := range []string{
		"mv .vault /tmp/vault2",
		"echo hi > .vault/notes.txt",
		"cp -r .vault /tmp/copy",
	} {
		res := tool.Execute(ctx, map[string]any{
			"action": "run", "command": cmd,
		})
		if !res.IsError || !strings.Contains(res.ForLLM, "blocked") {
			t.Errorf("command %q must be blocked, got: %s", cmd, res.ForLLM)
		}
	}

	// чтение разрешено (диагностика)
	res := tool.Execute(ctx, map[string]any{
		"action": "run", "command": "ls .vault",
	})
	if res.IsError && strings.Contains(res.ForLLM, "blocked") {
		t.Errorf("ls .vault must NOT be blocked, got: %s", res.ForLLM)
	}
}
