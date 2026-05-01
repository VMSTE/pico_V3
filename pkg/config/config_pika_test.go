package config

import (
	"encoding/json"
	"testing"
)

// ========================================================================
// Standalone type tests (compile independently of config.go changes)
// ========================================================================

func TestConfirmMode_UnmarshalString(t *testing.T) {
	tests := []struct {
		input    string
		expected ConfirmMode
	}{
		{`"always"`, ConfirmAlways},
		{`"never"`, ConfirmNever},
		{`"if_healthy"`, ConfirmIfHealthy},
		{`"if_critical_path"`, ConfirmIfCritical},
	}
	for _, tt := range tests {
		var m ConfirmMode
		if err := json.Unmarshal([]byte(tt.input), &m); err != nil {
			t.Fatalf("unmarshal %s: %v", tt.input, err)
		}
		if m != tt.expected {
			t.Errorf("unmarshal %s: got %q, want %q", tt.input, m, tt.expected)
		}
	}
}

func TestConfirmMode_UnmarshalBool(t *testing.T) {
	var m ConfirmMode

	if err := json.Unmarshal([]byte(`true`), &m); err != nil {
		t.Fatalf("unmarshal true: %v", err)
	}
	if m != ConfirmAlways {
		t.Errorf("unmarshal true: got %q, want %q", m, ConfirmAlways)
	}

	if err := json.Unmarshal([]byte(`false`), &m); err != nil {
		t.Fatalf("unmarshal false: %v", err)
	}
	if m != ConfirmNever {
		t.Errorf("unmarshal false: got %q, want %q", m, ConfirmNever)
	}
}

func TestConfirmMode_UnmarshalInvalid(t *testing.T) {
	var m ConfirmMode
	if err := json.Unmarshal([]byte(`123`), &m); err == nil {
		t.Fatal("expected error for numeric input, got nil")
	}
}

func TestSecurityConfig_JSON(t *testing.T) {
	jsonStr := `{
		"dangerous_ops": {
			"ops": {
				"deploy.request": {"level": "critical", "confirm": "always"},
				"files.write": {"level": "medium", "confirm": true}
			},
			"confirm_timeout_min": 30,
			"critical_paths": ["/workspace/prompt/*"]
		},
		"rad": {"enabled": true, "drift_threshold": 0.2, "block_score": 3, "warn_score": 2},
		"mcp": {"taint_reset_policy": "explicit_only", "per_server_rpm": 60}
	}`
	var cfg SecurityConfig
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.DangerousOps.Ops["deploy.request"].Confirm != ConfirmAlways {
		t.Errorf("deploy.request confirm: got %q, want %q",
			cfg.DangerousOps.Ops["deploy.request"].Confirm, ConfirmAlways)
	}
	if cfg.DangerousOps.Ops["files.write"].Confirm != ConfirmAlways {
		t.Errorf("files.write confirm (bool true): got %q, want %q",
			cfg.DangerousOps.Ops["files.write"].Confirm, ConfirmAlways)
	}
	if cfg.RAD.DriftThreshold != 0.2 {
		t.Errorf("rad drift: got %f, want 0.2", cfg.RAD.DriftThreshold)
	}
	if cfg.MCP.PerServerRPM != 60 {
		t.Errorf("mcp rpm: got %d, want 60", cfg.MCP.PerServerRPM)
	}
}

func TestHealthConfig_JSON(t *testing.T) {
	jsonStr := `{
		"window_size": 5,
		"tool_fail_threshold_pct": 30,
		"latency_threshold_ms": 30000,
		"fallback_provider": {
			"provider": "stepfun",
			"api_key_env": "STEPFUN_API_KEY",
			"model": "step-3.5-flash"
		},
		"reporting": {"typing_indicator_enabled": true},
		"progress": {"enabled": true, "throttle_sec": 2}
	}`
	var cfg HealthConfig
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.WindowSize != 5 {
		t.Errorf("window_size: got %d, want 5", cfg.WindowSize)
	}
	if cfg.FallbackProvider.Provider != "stepfun" {
		t.Errorf("fallback provider: got %q, want stepfun", cfg.FallbackProvider.Provider)
	}
	if cfg.Progress.ThrottleSec != 2 {
		t.Errorf("progress throttle: got %d, want 2", cfg.Progress.ThrottleSec)
	}
}

func TestClarifyConfig_JSON(t *testing.T) {
	jsonStr := `{"enabled": true, "timeout_min": 30, "max_streak_before_bypass": 2, "precheck_timeout_ms": 3000}`
	var cfg ClarifyConfig
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled=true")
	}
	if cfg.TimeoutMin != 30 {
		t.Errorf("timeout: got %d, want 30", cfg.TimeoutMin)
	}
}

func TestBaseToolsConfig_JSON(t *testing.T) {
	jsonStr := `{"enabled": true, "exec": true, "read_file": true, "write_file": false, "edit_file": true, "append_file": true, "list_dir": true}`
	var cfg BaseToolsConfig
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled=true")
	}
	if cfg.WriteFile {
		t.Error("expected write_file=false")
	}
	if !cfg.Exec {
		t.Error("expected exec=true")
	}
}

func TestNestedConfigs_JSON(t *testing.T) {
	// ReasoningConfig
	var r ReasoningConfig
	if err := json.Unmarshal([]byte(`{"effort":"medium","exclude":false,"log_max_chars":2000}`), &r); err != nil {
		t.Fatalf("reasoning unmarshal: %v", err)
	}
	if r.Effort != "medium" {
		t.Errorf("reasoning effort: got %q", r.Effort)
	}

	// BudgetConfig
	var b BudgetConfig
	if err := json.Unmarshal([]byte(`{"daily_usd":2.0,"session_usd":0.5,"warn_pct":80}`), &b); err != nil {
		t.Fatalf("budget unmarshal: %v", err)
	}
	if b.DailyUSD != 2.0 {
		t.Errorf("budget daily: got %f", b.DailyUSD)
	}

	// LoopConfig
	var l LoopConfig
	if err := json.Unmarshal([]byte(`{"chain_max_calls":8,"task_overflow_enabled":true}`), &l); err != nil {
		t.Fatalf("loop unmarshal: %v", err)
	}
	if l.ChainMaxCalls != 8 {
		t.Errorf("loop chain_max: got %d", l.ChainMaxCalls)
	}
}

// ========================================================================
// Tests requiring config.go struct changes (won't compile until applied)
// ========================================================================

func TestResolveAgentConfig_AtomizerTemperatureZero(t *testing.T) {
	temp1 := 1.0
	temp0 := 0.0
	topP1 := 1.0
	enabled := true

	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Temperature:    &temp1,
				ContextManager: "pika",
			},
			List: []AgentConfig{
				{
					ID:          "atomizer",
					Name:        "Атомизатор",
					Temperature: &temp0,
					TopP:        &topP1,
					Enabled:     &enabled,
				},
			},
		},
	}

	resolved := cfg.ResolveAgentConfig("atomizer")
	if resolved.Temperature != 0.0 {
		t.Errorf("atomizer temperature: got %f, want 0.0", resolved.Temperature)
	}
	if resolved.Name != "Атомизатор" {
		t.Errorf("atomizer name: got %q, want Атомизатор", resolved.Name)
	}
	if resolved.TopP != 1.0 {
		t.Errorf("atomizer top_p: got %f, want 1.0", resolved.TopP)
	}
}

func TestResolveAgentConfig_InheritDefaults(t *testing.T) {
	temp1 := 1.0

	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Temperature:    &temp1,
				IdleTimeoutMin: 30,
				ContextManager: "pika",
				MemoryDBPath:   "/workspace/memory/bot_memory.db",
			},
			List: []AgentConfig{
				{ID: "main", Default: true, Name: "Пика"},
			},
		},
	}

	resolved := cfg.ResolveAgentConfig("main")
	if resolved.Temperature != 1.0 {
		t.Errorf("main temperature: got %f, want 1.0", resolved.Temperature)
	}
	if resolved.IdleTimeoutMin != 30 {
		t.Errorf("main idle_timeout: got %d, want 30", resolved.IdleTimeoutMin)
	}
	if resolved.MemoryDBPath != "/workspace/memory/bot_memory.db" {
		t.Errorf("main memory_db_path: got %q", resolved.MemoryDBPath)
	}
}

func TestResolveAgentConfig_UnknownAgent(t *testing.T) {
	temp1 := 1.0

	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Temperature:    &temp1,
				IdleTimeoutMin: 30,
			},
			List: []AgentConfig{},
		},
	}

	resolved := cfg.ResolveAgentConfig("unknown")
	if resolved.Temperature != 1.0 {
		t.Errorf("unknown temperature: got %f, want 1.0 (defaults)", resolved.Temperature)
	}
	if resolved.ID != "unknown" {
		t.Errorf("unknown ID: got %q, want unknown", resolved.ID)
	}
}

func TestResolveAgentConfig_NestedOverride(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID:   "main",
					Name: "Пика",
					Reasoning: &ReasoningConfig{
						Effort:      "medium",
						Exclude:     false,
						LogMaxChars: 2000,
					},
					Budget: &BudgetConfig{
						DailyUSD:   2.00,
						SessionUSD: 0.50,
						WarnPct:    80,
					},
				},
			},
		},
	}

	resolved := cfg.ResolveAgentConfig("main")
	if resolved.Reasoning.Effort != "medium" {
		t.Errorf("reasoning effort: got %q, want medium", resolved.Reasoning.Effort)
	}
	if resolved.Budget.DailyUSD != 2.0 {
		t.Errorf("budget daily: got %f, want 2.0", resolved.Budget.DailyUSD)
	}
}

func TestResolveAgentConfig_PointerInherit(t *testing.T) {
	temp1 := 1.0

	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Temperature: &temp1,
			},
			List: []AgentConfig{
				{
					ID:          "main",
					Temperature: nil, // nil = inherit
				},
			},
		},
	}

	resolved := cfg.ResolveAgentConfig("main")
	if resolved.Temperature != 1.0 {
		t.Errorf("pointer inherit: got %f, want 1.0", resolved.Temperature)
	}
}

func TestResolveAgentConfig_PointerOverrideZero(t *testing.T) {
	temp1 := 1.0
	temp0 := 0.0

	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Temperature: &temp1,
			},
			List: []AgentConfig{
				{
					ID:          "atomizer",
					Temperature: &temp0, // explicit 0.0 override
				},
			},
		},
	}

	resolved := cfg.ResolveAgentConfig("atomizer")
	if resolved.Temperature != 0.0 {
		t.Errorf("pointer override zero: got %f, want 0.0", resolved.Temperature)
	}
}

func TestIsBaseToolEnabled_Default(t *testing.T) {
	cfg := &Config{}
	cfg.Tools.BaseTools = BaseToolsConfig{
		Enabled: true, Exec: true, ReadFile: true,
		WriteFile: true, EditFile: true, AppendFile: true, ListDir: true,
	}
	if !cfg.IsBaseToolEnabled("exec") {
		t.Error("default: exec should be enabled")
	}
	if !cfg.IsBaseToolEnabled("read_file") {
		t.Error("default: read_file should be enabled")
	}
}

func TestIsBaseToolEnabled_MasterSwitch(t *testing.T) {
	cfg := &Config{}
	cfg.Tools.BaseTools = BaseToolsConfig{
		Enabled: false, Exec: true,
	}
	if cfg.IsBaseToolEnabled("exec") {
		t.Error("master switch off: exec should be disabled")
	}
}

func TestIsBaseToolEnabled_PerTool(t *testing.T) {
	cfg := &Config{}
	cfg.Tools.BaseTools = BaseToolsConfig{
		Enabled: true, Exec: false, ReadFile: true,
	}
	if cfg.IsBaseToolEnabled("exec") {
		t.Error("per-tool: exec=false should be disabled")
	}
	if !cfg.IsBaseToolEnabled("read_file") {
		t.Error("per-tool: read_file=true should be enabled")
	}
}

func TestIsBaseToolEnabled_BrainTool(t *testing.T) {
	cfg := &Config{}
	cfg.Tools.BaseTools = BaseToolsConfig{Enabled: true}
	if cfg.IsBaseToolEnabled("search_memory") {
		t.Error("BRAIN tool search_memory should return false")
	}
}
