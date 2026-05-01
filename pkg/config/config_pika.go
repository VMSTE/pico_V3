// PIKA-V3: Pika v3 config types, functions, and extensions.
//
// This file contains all new types for Pika v3 that do not exist in upstream
// PicoClaw. Functions like ResolveAgentConfig and IsBaseToolEnabled currently
// resolve only upstream-available fields. Once config.go struct definitions are
// extended per ТЗ-v2-0b (adding fields to Config, AgentDefaults, AgentConfig,
// ToolsConfig, ModelConfig), these functions will be updated to cover all
// Pika-specific fields.

package config

import (
	"encoding/json"
	"fmt"
)

// ===========================================================================
// Cross-agent config types (will be top-level fields in Config struct)
// ===========================================================================

// PIKA-V3: ClarifyConfig — HITL clarification settings.
type ClarifyConfig struct {
	Enabled              bool `json:"enabled"`
	TimeoutMin           int  `json:"timeout_min"`
	MaxStreakBeforeBypass int  `json:"max_streak_before_bypass"`
	PrecheckTimeoutMs    int  `json:"precheck_timeout_ms"`
}

// PIKA-V3: SecurityConfig — security policy settings.
type SecurityConfig struct {
	DangerousOps DangerousOpsConfig `json:"dangerous_ops"`
	RAD          RADConfig          `json:"rad"`
	MCP          MCPSecurityConfig  `json:"mcp"`
}

// DangerousOpsConfig — dangerous operations confirmation policy.
type DangerousOpsConfig struct {
	Ops               map[string]DangerousOpEntry `json:"ops"`
	ConfirmTimeoutMin int                         `json:"confirm_timeout_min"`
	CriticalPaths     []string                    `json:"critical_paths"`
}

// DangerousOpEntry — single dangerous operation definition.
type DangerousOpEntry struct {
	Level   string      `json:"level"`
	Confirm ConfirmMode `json:"confirm"`
}

// ConfirmMode — typed confirmation policy.
// JSON unmarshal: true → "always", false → "never", string → as-is.
type ConfirmMode string

const (
	ConfirmAlways     ConfirmMode = "always"
	ConfirmNever      ConfirmMode = "never"
	ConfirmIfHealthy  ConfirmMode = "if_healthy"
	ConfirmIfCritical ConfirmMode = "if_critical_path"
)

// UnmarshalJSON implements custom JSON unmarshal for ConfirmMode.
// Accepts both string ("always") and bool (true → "always", false → "never").
func (m *ConfirmMode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*m = ConfirmMode(s)
		return nil
	}
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if b {
			*m = ConfirmAlways
		} else {
			*m = ConfirmNever
		}
		return nil
	}
	return fmt.Errorf("confirm: expected string or bool, got %s", data)
}

// RADConfig — Reasoning Anomaly Detector settings.
type RADConfig struct {
	Enabled        bool    `json:"enabled"`
	DriftThreshold float64 `json:"drift_threshold"`
	BlockScore     int     `json:"block_score"`
	WarnScore      int     `json:"warn_score"`
}

// MCPSecurityConfig — MCP protocol security settings.
type MCPSecurityConfig struct {
	TaintResetPolicy      string          `json:"taint_reset_policy"`
	StdioUser             string          `json:"stdio_user"`
	StdioIsolation        string          `json:"stdio_isolation"`
	PerServerRPM          int             `json:"per_server_rpm"`
	DefaultCapabilities   map[string]bool `json:"default_capabilities"`
	DefaultAllowPrompts   bool            `json:"default_allow_prompts"`
	DefaultAllowResources bool            `json:"default_allow_resources"`
}

// PIKA-V3: HealthConfig — system health monitoring settings.
type HealthConfig struct {
	WindowSize           int                    `json:"window_size"`
	ToolFailThresholdPct int                    `json:"tool_fail_threshold_pct"`
	LatencyThresholdMs   int                    `json:"latency_threshold_ms"`
	FallbackProvider     FallbackProviderConfig `json:"fallback_provider"`
	Reporting            HealthReportingConfig  `json:"reporting"`
	Progress             ProgressConfig         `json:"progress"`
}

// FallbackProviderConfig — fallback LLM provider settings.
type FallbackProviderConfig struct {
	Provider  string `json:"provider"`
	APIKeyEnv string `json:"api_key_env"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
}

// HealthReportingConfig — health reporting settings.
type HealthReportingConfig struct {
	TypingIndicatorEnabled    bool `json:"typing_indicator_enabled"`
	AlertDedupPerSession      bool `json:"alert_dedup_per_session"`
	DailyHealthSummaryEnabled bool `json:"daily_health_summary_enabled"`
}

// ProgressConfig — progress notification settings.
type ProgressConfig struct {
	Enabled            bool `json:"enabled"`
	ThrottleSec        int  `json:"throttle_sec"`
	DeleteOnComplete   bool `json:"delete_on_complete"`
	ShowStepText       bool `json:"show_step_text"`
	StopCommandEnabled bool `json:"stop_command_enabled"`
}

// ===========================================================================
// Per-agent nested config types
// ===========================================================================

// PIKA-V3: ReasoningConfig — per-agent reasoning settings.
type ReasoningConfig struct {
	Effort      string `json:"effort"`
	Exclude     bool   `json:"exclude"`
	LogMaxChars int    `json:"log_max_chars"`
}

// BudgetConfig — per-agent budget limits.
type BudgetConfig struct {
	DailyUSD   float64 `json:"daily_usd"`
	SessionUSD float64 `json:"session_usd"`
	WarnPct    int     `json:"warn_pct"`
}

// OutputGateConfig — output length gate settings.
type OutputGateConfig struct {
	Enabled    bool `json:"enabled"`
	MaxChars   int  `json:"max_chars"`
	MaxRetries int  `json:"max_retries"`
}

// LoopConfig — agentic loop settings.
type LoopConfig struct {
	ChainMaxCalls            int  `json:"chain_max_calls"`
	TaskOverflowEnabled      bool `json:"task_overflow_enabled"`
	OverflowChunkSize        int  `json:"overflow_chunk_size"`
	ProactiveRotateThreshold int  `json:"proactive_rotate_threshold"`
	AvgTokensWindowSize      int  `json:"avg_tokens_window_size"`
}

// MemoryBriefConfig — Archivist memory brief settings.
type MemoryBriefConfig struct {
	SoftLimit                 int      `json:"soft_limit"`
	HardLimit                 int      `json:"hard_limit"`
	CompressProtectedSections []string `json:"compress_protected_sections"`
	MaxRetries                int      `json:"max_retries"`
}

// ArchiveConfig — cold archive read settings.
type ArchiveConfig struct {
	FTSTopN         int `json:"fts_top_n"`
	FTSWindow       int `json:"fts_window"`
	ReadMaxSnippets int `json:"read_max_snippets"`
	ReadTimeoutMs   int `json:"read_timeout_ms"`
}

// ScheduleConfig — periodic task schedule.
type ScheduleConfig struct {
	Daily   string `json:"daily"`
	Weekly  string `json:"weekly"`
	Monthly string `json:"monthly"`
}

// ===========================================================================
// BaseToolsConfig (D-TOOL-CLASS §4.1b)
// ===========================================================================

// PIKA-V3: BaseToolsConfig — master switch + per-tool toggles for BASE tools.
// BRAIN tools (search_memory, registry_write, clarify) are always active.
//
// Will be added to ToolsConfig as: BaseTools BaseToolsConfig `json:"base_tools"`
// once config.go is patched per ТЗ-v2-0b.
type BaseToolsConfig struct {
	Enabled    bool `json:"enabled"`
	Exec       bool `json:"exec"`
	ReadFile   bool `json:"read_file"`
	WriteFile  bool `json:"write_file"`
	EditFile   bool `json:"edit_file"`
	AppendFile bool `json:"append_file"`
	ListDir    bool `json:"list_dir"`
}

// IsBaseToolEnabled checks whether a specific BASE tool is enabled.
// BRAIN tools (search_memory, registry_write, clarify) always return false —
// they are always active via separate logic.
//
// After config.go is patched, add a convenience method on *Config:
//
//	func (c *Config) IsBaseToolEnabled(name string) bool {
//	    return c.Tools.BaseTools.IsBaseToolEnabled(name)
//	}
func (bt *BaseToolsConfig) IsBaseToolEnabled(name string) bool {
	if !bt.Enabled {
		return false
	}
	switch name {
	case "exec":
		return bt.Exec
	case "read_file":
		return bt.ReadFile
	case "write_file":
		return bt.WriteFile
	case "edit_file":
		return bt.EditFile
	case "append_file":
		return bt.AppendFile
	case "list_dir":
		return bt.ListDir
	default:
		return false
	}
}

// DefaultBaseToolsConfig returns BaseToolsConfig with all tools enabled.
func DefaultBaseToolsConfig() BaseToolsConfig {
	return BaseToolsConfig{
		Enabled:    true,
		Exec:       true,
		ReadFile:   true,
		WriteFile:  true,
		EditFile:   true,
		AppendFile: true,
		ListDir:    true,
	}
}

// ===========================================================================
// ResolvedAgentConfig — flat resolved struct
// ===========================================================================

// PIKA-V3: ResolvedAgentConfig is the fully-resolved configuration for a
// specific agent. Downstream consumers use this instead of reading AgentConfig
// fields directly. All pointer types are resolved to value types.
type ResolvedAgentConfig struct {
	// Identity
	ID   string `json:"id"`
	Name string `json:"name"`

	// From upstream AgentDefaults
	Workspace      string  `json:"workspace"`
	Provider       string  `json:"provider"`
	ModelName      string  `json:"model_name"`
	Temperature    float64 `json:"temperature"`
	ContextWindow  int     `json:"context_window"`
	MaxTokens      int     `json:"max_tokens"`
	ContextManager string  `json:"context_manager"`

	// Pika-specific paths (populated after config.go patch)
	MemoryDBPath string `json:"memory_db_path"`
	BaseToolsDir string `json:"base_tools_dir"`
	SkillsDir    string `json:"skills_dir"`

	// Pika-specific model params
	MaxToolsInPrompt int      `json:"max_tools_in_prompt"`
	TopP             *float64 `json:"top_p,omitempty"`
	TopK             *int     `json:"top_k,omitempty"`

	// Pika-specific telemetry
	TelemetryEnabled       bool `json:"telemetry_enabled"`
	TelemetryRetentionDays int  `json:"telemetry_retention_days"`

	// Pika-specific retry/loop
	MaxRetriesPerMessage   int  `json:"max_retries_per_message"`
	ToolCallRetryEnabled   bool `json:"tool_call_retry_enabled"`
	LoopDetectionThreshold int  `json:"loop_detection_threshold"`

	// Pika-specific debug/idle
	PromptDebug    bool `json:"prompt_debug"`
	IdleTimeoutMin int  `json:"idle_timeout_min"`

	// Per-agent overrides
	PromptFile     string   `json:"prompt_file,omitempty"`
	Enabled        bool     `json:"enabled"`
	BootstrapFiles []string `json:"bootstrap_files,omitempty"`

	// Nested configs
	Reasoning   ReasoningConfig   `json:"reasoning"`
	Budget      BudgetConfig      `json:"budget"`
	OutputGate  OutputGateConfig  `json:"output_gate"`
	Loop        LoopConfig        `json:"loop"`
	MemoryBrief MemoryBriefConfig `json:"memory_brief"`
	Archive     ArchiveConfig     `json:"archive"`
	Schedule    ScheduleConfig    `json:"schedule"`

	// Role-specific: main agent
	SessionRotateThresholdPct int     `json:"session_rotate_threshold_pct,omitempty"`
	FocusStaleThresholdMsgs   int     `json:"focus_stale_threshold_msgs,omitempty"`
	HeartbeatIntervalSeconds  int     `json:"heartbeat_interval_seconds,omitempty"`
	TokenEstimateMultiplier   float64 `json:"token_estimate_multiplier,omitempty"`
	CalibrationIntervalMin    int     `json:"calibration_interval_min,omitempty"`
	BudgetCacheIntervalMin    int     `json:"budget_cache_interval_min,omitempty"`
	BudgetSafetyMultiplier    float64 `json:"budget_safety_multiplier,omitempty"`

	// Role-specific: archivist
	MaxToolCalls             int      `json:"max_tool_calls,omitempty"`
	BuildPromptTimeoutMs     int      `json:"build_prompt_timeout_ms,omitempty"`
	ReasoningGuidedRetrieval bool     `json:"reasoning_guided_retrieval,omitempty"`
	ReasoningKeywordsMax     int      `json:"reasoning_keywords_max,omitempty"`
	ReasoningDriftOverlapMin float64  `json:"reasoning_drift_overlap_min,omitempty"`
	TopicTriggers            []string `json:"topic_triggers,omitempty"`

	// Role-specific: atomizer
	TriggerTokens  int `json:"trigger_tokens,omitempty"`
	ChunkMaxTokens int `json:"chunk_max_tokens,omitempty"`

	// Role-specific: MCP Guard
	TimeoutMs                int     `json:"timeout_ms,omitempty"`
	SuspiciousTextRatio      float64 `json:"suspicious_text_ratio,omitempty"`
	SuspiciousSizeMultiplier float64 `json:"suspicious_size_multiplier,omitempty"`
	StartupAuditEnabled      bool    `json:"startup_audit_enabled,omitempty"`
	ReauditOnListChanged     bool    `json:"reaudit_on_list_changed,omitempty"`
	HashAlgorithm            string  `json:"hash_algorithm,omitempty"`
}

// ===========================================================================
// ResolveAgentConfig — merge defaults + per-agent overrides
// ===========================================================================

// ResolveAgentConfig finds an agent by ID in Config.Agents.List and merges
// Config.Agents.Defaults with the agent's overrides.
//
// Merge rules:
//   - Pointer fields (*float64, *bool, etc.): nil = inherit, non-nil = override.
//     temperature: 0.0 is preserved as override (pointer semantics).
//   - Value fields (int, string, float64): zero = inherit, non-zero = override.
//   - Slices ([]string): nil = inherit, non-nil (even empty) = override.
//   - Nested struct pointers: nil = use defaults, non-nil = replace entirely.
//
// If the agent is not found, returns a copy of defaults.
//
// NOTE: Currently resolves only upstream AgentDefaults/AgentConfig fields.
// Pika-specific fields (MemoryDBPath, TopP, Reasoning, Budget, etc.) will be
// resolved once config.go struct definitions are extended per ТЗ-v2-0b.
func (c *Config) ResolveAgentConfig(name string) ResolvedAgentConfig {
	d := c.Agents.Defaults

	// Start with upstream defaults
	resolved := ResolvedAgentConfig{
		ID:             name,
		Name:           name,
		Workspace:      d.Workspace,
		Provider:       d.Provider,
		ModelName:      d.ModelName,
		MaxTokens:      d.MaxTokens,
		ContextWindow:  d.ContextWindow,
		ContextManager: d.ContextManager,
		Enabled:        true,
	}

	// Resolve Temperature from pointer
	if d.Temperature != nil {
		resolved.Temperature = *d.Temperature
	}

	// TODO(pika-v3): Once config.go AgentDefaults is extended, populate:
	// resolved.MemoryDBPath = d.MemoryDBPath
	// resolved.BaseToolsDir = d.BaseToolsDir
	// resolved.SkillsDir = d.SkillsDir
	// resolved.MaxToolsInPrompt = d.MaxToolsInPrompt
	// resolved.TopP = d.TopP
	// resolved.TopK = d.TopK
	// resolved.TelemetryEnabled = d.TelemetryEnabled
	// resolved.TelemetryRetentionDays = d.TelemetryRetentionDays
	// resolved.MaxRetriesPerMessage = d.MaxRetriesPerMessage
	// resolved.ToolCallRetryEnabled = d.ToolCallRetryEnabled
	// resolved.LoopDetectionThreshold = d.LoopDetectionThreshold
	// resolved.PromptDebug = d.PromptDebug
	// resolved.IdleTimeoutMin = d.IdleTimeoutMin

	// Find agent by ID
	var agent *AgentConfig
	for i := range c.Agents.List {
		if c.Agents.List[i].ID == name {
			agent = &c.Agents.List[i]
			break
		}
	}

	if agent == nil {
		return resolved
	}

	// Apply agent identity
	resolved.ID = agent.ID
	if agent.Name != "" {
		resolved.Name = agent.Name
	}

	// Override workspace
	if agent.Workspace != "" {
		resolved.Workspace = agent.Workspace
	}

	// Override model
	if agent.Model != nil && agent.Model.Primary != "" {
		resolved.ModelName = agent.Model.Primary
	}

	// TODO(pika-v3): Once config.go AgentConfig is extended, apply per-agent
	// overrides for Temperature, TopP, TopK, PromptFile, Enabled,
	// BootstrapFiles, Reasoning, Budget, OutputGate, Loop, MemoryBrief,
	// Archive, Schedule, and role-specific fields.

	return resolved
}
