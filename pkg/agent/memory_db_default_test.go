package agent

// Волна 122 (срез М0): регресс на флак «no such table: messages» в CI
// (1–3 окт, pkg/agent). Корень: ручной конфиг без MemoryDBPath →
// Migrate("") → приватная temp-БД на коннект пула. Теперь пустой путь
// резолвится в workspace/memory/bot_memory.db — файл реальный, мигрирован.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestNewAgentInstance_EmptyMemoryDBPathFallsBackToWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxToolIterations = 1
	// MemoryDBPath намеренно пустой — ручной конфиг без DefaultConfig().

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	key := "empty-db-path-guard"
	agent.Sessions.AddMessage(key, "user", "раз")
	agent.Sessions.AddMessage(key, "assistant", "два")
	if got := len(agent.Sessions.GetHistory(key)); got != 2 {
		t.Fatalf("history len = %d, want 2 (DB must be a migrated workspace file)", got)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "memory", "bot_memory.db")); err != nil {
		t.Fatalf("workspace DB file missing: %v", err)
	}
}
