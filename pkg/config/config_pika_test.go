package config

import (
	"encoding/json"
	"testing"
)

func TestConfirmMode_String(t *testing.T) {
	var m ConfirmMode
	if err := json.Unmarshal([]byte(`"always"`), &m); err != nil {
		t.Fatal(err)
	}
	if m != ConfirmAlways {
		t.Errorf("want ConfirmAlways, got %q", m)
	}
}

func TestConfirmMode_Bool(t *testing.T) {
	var m ConfirmMode
	if err := json.Unmarshal([]byte(`true`), &m); err != nil {
		t.Fatal(err)
	}
	if m != ConfirmAlways {
		t.Errorf("want ConfirmAlways for true, got %q", m)
	}
	if err := json.Unmarshal([]byte(`false`), &m); err != nil {
		t.Fatal(err)
	}
	if m != ConfirmNever {
		t.Errorf("want ConfirmNever for false, got %q", m)
	}
}

func TestConfirmMode_Invalid(t *testing.T) {
	var m ConfirmMode
	if err := json.Unmarshal([]byte(`42`), &m); err == nil {
		t.Fatal("expected error for numeric input")
	}
}

func TestSecurityConfig_JSON(t *testing.T) {
	input := `{
		"dangerous_ops": {
			"ops": {
				"deploy.request": {"level": "critical", "confirm": "always"},
				"files.write": {"level": "medium", "confirm": true}
			},
			"confirm_timeout_min": 30,
			"critical_paths": ["/workspace/prompt/*"]
		},
		"rad": {"enabled": true, "drift_threshold": 0.2, "block_score": 3, "warn_score": 2},
		"mcp": {
			"taint_reset_policy": "explicit_only",
			"stdio_user": "mcp-sandbox",
			"stdio_isolation": "user",
			"per_server_rpm": 60,
			"default_capabilities": {"sampling": false},
			"default_allow_prompts": false,
			"default_allow_resources": true
		}
	}`

	var sec SecurityConfig
	if err := json.Unmarshal([]byte(input), &sec); err != nil {
		t.Fatal(err)
	}
	if !sec.RAD.Enabled {
		t.Error("RAD should be enabled")
	}
	if sec.RAD.DriftThreshold != 0.2 {
		t.Errorf("RAD.DriftThreshold = %v, want 0.2", sec.RAD.DriftThreshold)
	}
	if sec.MCP.PerServerRPM != 60 {
		t.Errorf("MCP.PerServerRPM = %d, want 60", sec.MCP.PerServerRPM)
	}
	op := sec.DangerousOps.Ops["files.write"]
	if op.Confirm != ConfirmAlways {
		t.Errorf("files.write confirm = %q, want %q", op.Confirm, ConfirmAlways)
	}
}

func TestHealthConfig_JSON(t *testing.T) {
	input := `{
		"window_size": 5,
		"tool_fail_threshold_pct": 30,
		"latency_threshold_ms": 30000,
		"fallback_provider": {
			"provider": "stepfun",
			"api_key_env": "STEPFUN_API_KEY",
			"base_url": "https://api.stepfun.com/v1",
			"model": "step-3.5-flash"
		},
		"reporting": {
			"typing_indicator_enabled": true,
			"alert_dedup_per_session": true,
			"daily_health_summary_enabled": true
		},
		"progress": {
			"enabled": true,
			"throttle_sec": 2,
			"delete_on_complete": true,
			"show_step_text": true,
			"stop_command_enabled": true
		}
	}`

	var h HealthConfig
	if err := json.Unmarshal([]byte(input), &h); err != nil {
		t.Fatal(err)
	}
	if h.WindowSize != 5 {
		t.Errorf("WindowSize = %d, want 5", h.WindowSize)
	}
	if h.FallbackProvider.Model != "step-3.5-flash" {
		t.Errorf("FallbackProvider.Model = %q, want step-3.5-flash", h.FallbackProvider.Model)
	}
	if !h.Progress.Enabled {
		t.Error("Progress should be enabled")
	}
}

func TestClarifyConfig_JSON(t *testing.T) {
	input := `{"enabled": true, "timeout_min": 30, "max_streak_before_bypass": 2, "precheck_timeout_ms": 3000}`

	var cl ClarifyConfig
	if err := json.Unmarshal([]byte(input), &cl); err != nil {
		t.Fatal(err)
	}
	if !cl.Enabled {
		t.Error("Clarify should be enabled")
	}
	if cl.TimeoutMin != 30 {
		t.Errorf("TimeoutMin = %d, want 30", cl.TimeoutMin)
	}
}

func TestBaseToolsConfig_JSON(t *testing.T) {
	input := `{"enabled": true, "exec": true, "read_file": true, "write_file": false}`

	var bt BaseToolsConfig
	if err := json.Unmarshal([]byte(input), &bt); err != nil {
		t.Fatal(err)
	}
	if !bt.Enabled {
		t.Error("BaseTools should be enabled")
	}
	if bt.WriteFile {
		t.Error("WriteFile should be disabled")
	}
}

func TestIsBaseToolEnabled_Default(t *testing.T) {
	bt := DefaultBaseToolsConfig()
	if !bt.IsBaseToolEnabled("exec") {
		t.Error("exec should be enabled by default")
	}
	if !bt.IsBaseToolEnabled("read_file") {
		t.Error("read_file should be enabled by default")
	}
}

func TestIsBaseToolEnabled_MasterSwitch(t *testing.T) {
	bt := BaseToolsConfig{Enabled: false, Exec: true, ReadFile: true}
	if bt.IsBaseToolEnabled("exec") {
		t.Error("exec should be disabled when master switch is off")
	}
}

func TestIsBaseToolEnabled_PerTool(t *testing.T) {
	bt := BaseToolsConfig{Enabled: true, Exec: false, ReadFile: true, WriteFile: true}
	if bt.IsBaseToolEnabled("exec") {
		t.Error("exec should be disabled when per-tool is off")
	}
	if !bt.IsBaseToolEnabled("read_file") {
		t.Error("read_file should be enabled")
	}
}

func TestIsBaseToolEnabled_BrainTool(t *testing.T) {
	bt := DefaultBaseToolsConfig()
	if bt.IsBaseToolEnabled("search_memory") {
		t.Error("BRAIN tool search_memory should return false")
	}
}

func TestNestedConfigs_JSON(t *testing.T) {
	input := `{
		"reasoning": {"effort": "medium", "exclude": false, "log_max_chars": 2000},
		"budget": {"daily_usd": 2.0, "session_usd": 0.5, "warn_pct": 80},
		"output_gate": {"enabled": true, "max_chars": 3500, "max_retries": 3},
		"loop": {"chain_max_calls": 8, "task_overflow_enabled": true, "overflow_chunk_size": 5},
		"memory_brief": {"soft_limit": 5000, "hard_limit": 6000, "compress_protected_sections": ["AVOID"], "max_retries": 3},
		"archive": {"fts_top_n": 20, "fts_window": 2, "read_max_snippets": 10, "read_timeout_ms": 5000},
		"schedule": {"daily": "03:00", "weekly": "Sun 04:00", "monthly": "1st 05:00"}
	}`

	var data struct {
		Reasoning   ReasoningConfig   `json:"reasoning"`
		Budget      BudgetConfig      `json:"budget"`
		OutputGate  OutputGateConfig  `json:"output_gate"`
		Loop        LoopConfig        `json:"loop"`
		MemoryBrief MemoryBriefConfig `json:"memory_brief"`
		Archive     ArchiveConfig     `json:"archive"`
		Schedule    ScheduleConfig    `json:"schedule"`
	}
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		t.Fatal(err)
	}
	if data.Reasoning.Effort != "medium" {
		t.Errorf("Reasoning.Effort = %q, want medium", data.Reasoning.Effort)
	}
	if data.Budget.DailyUSD != 2.0 {
		t.Errorf("Budget.DailyUSD = %v, want 2.0", data.Budget.DailyUSD)
	}
	if data.Loop.ChainMaxCalls != 8 {
		t.Errorf("Loop.ChainMaxCalls = %d, want 8", data.Loop.ChainMaxCalls)
	}
	if data.Schedule.Daily != "03:00" {
		t.Errorf("Schedule.Daily = %q, want 03:00", data.Schedule.Daily)
	}
}

func TestResolveAgentConfig_UnknownAgent(t *testing.T) {
	temp := 1.0
	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Workspace:   "/workspace",
				ModelName:   "main",
				Temperature: &temp,
			},
			List: []AgentConfig{
				{ID: "main", Default: true, Name: "Pika"},
			},
		},
	}

	resolved := cfg.ResolveAgentConfig("nonexistent")
	if resolved.ID != "nonexistent" {
		t.Errorf("ID = %q, want nonexistent", resolved.ID)
	}
	if resolved.Workspace != "/workspace" {
		t.Errorf("Workspace = %q, want /workspace", resolved.Workspace)
	}
	if resolved.Temperature != 1.0 {
		t.Errorf("Temperature = %v, want 1.0", resolved.Temperature)
	}
}

func TestResolveAgentConfig_InheritDefaults(t *testing.T) {
	temp := 1.0
	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Workspace:      "/workspace",
				Provider:       "openrouter",
				ModelName:      "main",
				Temperature:    &temp,
				ContextWindow:  256000,
				ContextManager: "pika",
			},
			List: []AgentConfig{
				{ID: "main", Default: true, Name: "Pika"},
			},
		},
	}

	resolved := cfg.ResolveAgentConfig("main")
	if resolved.Name != "Pika" {
		t.Errorf("Name = %q, want Pika", resolved.Name)
	}
	if resolved.ContextWindow != 256000 {
		t.Errorf("ContextWindow = %d, want 256000", resolved.ContextWindow)
	}
	if resolved.ContextManager != "pika" {
		t.Errorf("ContextManager = %q, want pika", resolved.ContextManager)
	}
}

func TestResolveAgentConfig_ModelOverride(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				ModelName: "main",
			},
			List: []AgentConfig{
				{
					ID:   "archivist",
					Name: "Archivist",
					Model: &AgentModelConfig{
						Primary: "background",
					},
				},
			},
		},
	}

	resolved := cfg.ResolveAgentConfig("archivist")
	if resolved.ModelName != "background" {
		t.Errorf("ModelName = %q, want background", resolved.ModelName)
	}
}
