package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Р-1: флаги hooks.builtins.pika.<имя>.enabled реально включают
// и выключают монтирование хуков Пики (D-AUDIT-49).

func newPikaHookTestLoop(cfg *config.Config) *AgentLoop {
	return &AgentLoop{
		cfg:   cfg,
		hooks: NewHookManager(nil),
	}
}

func pikaMountedHookNames(al *AgentLoop) map[string]bool {
	mounted := map[string]bool{}
	for _, reg := range al.hooks.snapshotHooks() {
		mounted[reg.Name] = true
	}
	return mounted
}

func TestPikaBuiltinHooks_EnabledFlagsMount(t *testing.T) {
	cfg := &config.Config{}
	cfg.Hooks.Enabled = true
	cfg.Hooks.Builtins = map[string]config.BuiltinHookConfig{
		"pika.output_gate":  {Enabled: true},
		"pika.toolguard":    {Enabled: true},
		"pika.confirm_gate": {Enabled: true},
		"pika.progress":     {Enabled: true},
	}
	al := newPikaHookTestLoop(cfg)
	registerPikaBuiltinHooks(al, cfg)
	if err := al.loadConfiguredHooks(context.Background()); err != nil {
		t.Fatalf("loadConfiguredHooks: %v", err)
	}
	mounted := pikaMountedHookNames(al)
	for _, name := range []string{
		"pika.output_gate", "pika.toolguard",
		"pika.confirm_gate", "pika.progress",
	} {
		if !mounted[name] {
			t.Errorf("expected hook %q mounted", name)
		}
	}
}

func TestPikaBuiltinHooks_DisabledFlagsSkip(t *testing.T) {
	cfg := &config.Config{}
	cfg.Hooks.Enabled = true
	cfg.Hooks.Builtins = map[string]config.BuiltinHookConfig{
		"pika.output_gate": {Enabled: false},
		"pika.toolguard":   {Enabled: true},
	}
	al := newPikaHookTestLoop(cfg)
	registerPikaBuiltinHooks(al, cfg)
	if err := al.loadConfiguredHooks(context.Background()); err != nil {
		t.Fatalf("loadConfiguredHooks: %v", err)
	}
	mounted := pikaMountedHookNames(al)
	if mounted["pika.output_gate"] {
		t.Error("pika.output_gate must NOT be mounted when disabled")
	}
	if !mounted["pika.toolguard"] {
		t.Error("pika.toolguard must be mounted when enabled")
	}
}

func TestPikaBuiltinHooks_GlobalDisabledMountsNothing(t *testing.T) {
	cfg := &config.Config{}
	cfg.Hooks.Enabled = false
	cfg.Hooks.Builtins = map[string]config.BuiltinHookConfig{
		"pika.output_gate": {Enabled: true},
	}
	al := newPikaHookTestLoop(cfg)
	registerPikaBuiltinHooks(al, cfg)
	if err := al.loadConfiguredHooks(context.Background()); err != nil {
		t.Fatalf("loadConfiguredHooks: %v", err)
	}
	if got := len(al.hooks.snapshotHooks()); got != 0 {
		t.Errorf("expected 0 mounted hooks, got %d", got)
	}
}

func TestPikaBuiltinHooks_DoubleRegistrationSafe(t *testing.T) {
	cfg := &config.Config{}
	al := newPikaHookTestLoop(cfg)
	registerPikaBuiltinHooks(al, cfg)
	registerPikaBuiltinHooks(al, cfg) // не должно паниковать или дублировать
}
