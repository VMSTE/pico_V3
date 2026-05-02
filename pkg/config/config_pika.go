// PIKA-V3: Pika v3 config types, functions, and extensions.
// This file contains all new types for Pika v3 that do not exist in upstream
// PicoClaw.

package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ClarifyConfig defines HITL clarification settings. PIKA-V3.
type ClarifyConfig struct {
	Enabled               bool `json:"enabled"`
	TimeoutMin            int  `json:"timeout_min"`
	MaxStreakBeforeBypass int  `json:"max_streak_before_bypass"`
	PrecheckTimeoutMs     int  `json:"precheck_timeout_ms"`
}

// SecurityConfig defines security policy settings. PIKA-V3.
type SecurityConfig struct {
	DangerousOps DangerousOpsConfig `json:"dangerous_ops"`
	RAD          RADConfig          `json:"rad"`
	MCP          MCPSecurityConfig  `json:"mcp"`
}

// DangerousOpsConfig defines dangerous operations confirmation policy.
type DangerousOpsConfig struct {
	Ops               map[string]DangerousOpEntry `json:"ops"`
	ConfirmTimeoutMin int                         `json:"confirm_timeout_min"`
	CriticalPaths     []string                    `json:"critical_paths"`
}

// DangerousOpEntry defines a single dangerous operation.
type DangerousOpEntry struct {
	Level   string      `json:"level"`
	Confirm ConfirmMode `json:"confirm"`
}

// ConfirmMode is a typed confirmation policy.
// JSON unmarshal: true -> "always", false -> "never", string -> as-is.
type ConfirmMode string

const (
	// ConfirmAlways requires confirmation for every invocation.
	ConfirmAlways ConfirmMode = "always"
	// ConfirmNever skips confirmation entirely.
	ConfirmNever ConfirmMode = "never"
	// ConfirmIfHealthy confirms only when the system is healthy.
	ConfirmIfHealthy ConfirmMode = "if_healthy"
	// ConfirmIfCritical confirms only for critical-path operations.
	ConfirmIfCritical ConfirmMode = "if_critical_path"
)

// UnmarshalJSON implements custom JSON unmarshal for ConfirmMode.
// Accepts both string ("always") and bool (true -> "always", false -> "never").
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

// RADConfig defines Reasoning Anomaly Detector settings.
type RADConfig struct {
	Enabled        bool    `json:"enabled"`
	DriftThreshold float64 `json:"drift_threshold"`
	BlockScore     int     `json:"block_score"`
	WarnScore      int     `json:"warn_score"`
}

// MCPSecurityConfig defines MCP protocol security settings.
type MCPSecurityConfig struct {
	TaintResetPolicy      string          `json:"taint_reset_policy"`
	StdioUser             string          `json:"stdio_user"`
	StdioIsolation        string          `json:"stdio_isolation"`
	PerServerRPM          int             `json:"per_server_rpm"`
	DefaultCapabilities   map[string]bool `json:"default_capabilities"`
	DefaultAllowPrompts   bool            `json:"default_allow_prompts"`
	DefaultAllowResources bool            `json:"default_allow_resources"`
}

// HealthConfig defines system health monitoring settings. PIKA-V3.
type HealthConfig struct {
	WindowSize           int                    `json:"window_size"`
	ToolFailThresholdPct int                    `json:"tool_fail_threshold_pct"`
	LatencyThresholdMs   int                    `json:"latency_threshold_ms"`
	FallbackProvider     FallbackProviderConfig `json:"fallback_provider"`
	Reporting            HealthReportingConfig  `json:"reporting"`
	Progress             ProgressConfig         `json:"progress"`
}

// FallbackProviderConfig defines fallback LLM provider settings.
type FallbackProviderConfig struct {
	Provider  string `json:"provider"`
	APIKeyEnv string `json:"api_key_env"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
}

// HealthReportingConfig defines health reporting settings.
type HealthReportingConfig struct {
	TypingIndicatorEnabled    bool `json:"typing_indicator_enabled"`
	AlertDedupPerSession      bool `json:"alert_dedup_per_session"`
	DailyHealthSummaryEnabled bool `json:"daily_health_summary_enabled"`
}

// ProgressConfig defines progress notification settings.
type ProgressConfig struct {
	Enabled            bool `json:"enabled"`
	ThrottleSec        int  `json:"throttle_sec"`
	DeleteOnComplete   bool `json:"delete_on_complete"`
	ShowStepText       bool `json:"show_step_text"`
	StopCommandEnabled bool `json:"stop_command_enabled"`
}

// ReasoningConfig defines per-agent reasoning settings. PIKA-V3.
type ReasoningConfig struct {
	Effort      string `json:"effort"`
	Exclude     bool   `json:"exclude"`
	LogMaxChars int    `json:"log_max_chars"`
}

// BudgetConfig defines per-agent budget limits.
type BudgetConfig struct {
	DailyUSD   float64 `json:"daily_usd"`
	SessionUSD float64 `json:"session_usd"`
	WarnPct    int     `json:"warn_pct"`
}

// OutputGateConfig defines output length gate settings.
type OutputGateConfig struct {
	Enabled    bool `json:"enabled"`
	MaxChars   int  `json:"max_chars"`
	MaxRetries int  `json:"max_retries"`
}

// LoopConfig defines agentic loop settings.
type LoopConfig struct {
	ChainMaxCalls            int  `json:"chain_max_calls"`
	TaskOverflowEnabled      bool `json:"task_overflow_enabled"`
	OverflowChunkSize        int  `json:"overflow_chunk_size"`
	ProactiveRotateThreshold int  `json:"proactive_rotate_threshold"`
	AvgTokensWindowSize      int  `json:"avg_tokens_window_size"`
}

// MemoryBriefConfig defines Archivist memory brief settings.
type MemoryBriefConfig struct {
	SoftLimit                 int      `json:"soft_limit"`
	HardLimit                 int      `json:"hard_limit"`
	CompressProtectedSections []string `json:"compress_protected_sections"`
	MaxRetries                int      `json:"max_retries"`
}

// ArchiveConfig defines cold archive read settings.
type ArchiveConfig struct {
	FTSTopN         int `json:"fts_top_n"`
	FTSWindow       int `json:"fts_window"`
	ReadMaxSnippets int `json:"read_max_snippets"`
	ReadTimeoutMs   int `json:"read_timeout_ms"`
}

// ScheduleConfig defines periodic task schedule.
type ScheduleConfig struct {
	Daily   string `json:"daily"`
	Weekly  string `json:"weekly"`
	Monthly string `json:"monthly"`
}

// BaseToolsConfig defines master switch and per-tool toggles for BASE tools.
// BRAIN tools (search_memory, registry_write, clarify) are always active.
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
// BRAIN tools always return false here.
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

// ResolvedAgentConfig is the fully-resolved configuration for a specific agent.
// Downstream consumers use this instead of reading AgentConfig fields directly.
// All pointer types are resolved to value types. PIKA-V3.
type ResolvedAgentConfig struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// From upstream AgentDefaults.
	Workspace      string  `json:"workspace"`
	Provider       string  `json:"provider"`
	ModelName      string  `json:"model_name"`
	Temperature    float64 `json:"temperature"`
	ContextWindow  int     `json:"context_window"`
	MaxTokens      int     `json:"max_tokens"`
	ContextManager string  `json:"context_manager"`

	// Pika-specific paths (populated after config.go patch).
	MemoryDBPath string `json:"memory_db_path"`
	BaseToolsDir string `json:"base_tools_dir"`
	SkillsDir    string `json:"skills_dir"`

	// Pika-specific model params.
	MaxToolsInPrompt int      `json:"max_tools_in_prompt"`
	TopP             *float64 `json:"top_p,omitempty"`
	TopK             *int     `json:"top_k,omitempty"`

	// Pika-specific telemetry.
	TelemetryEnabled       bool `json:"telemetry_enabled"`
	TelemetryRetentionDays int  `json:"telemetry_retention_days"`

	// Pika-specific retry/loop.
	MaxRetriesPerMessage   int  `json:"max_retries_per_message"`
	ToolCallRetryEnabled   bool `json:"tool_call_retry_enabled"`
	LoopDetectionThreshold int  `json:"loop_detection_threshold"`

	// Pika-specific debug/idle.
	PromptDebug    bool `json:"prompt_debug"`
	IdleTimeoutMin int  `json:"idle_timeout_min"`

	// Per-agent overrides.
	PromptFile     string   `json:"prompt_file,omitempty"`
	Enabled        bool     `json:"enabled"`
	BootstrapFiles []string `json:"bootstrap_files,omitempty"`

	// Nested configs.
	Reasoning   ReasoningConfig   `json:"reasoning"`
	Budget      BudgetConfig      `json:"budget"`
	OutputGate  OutputGateConfig  `json:"output_gate"`
	Loop        LoopConfig        `json:"loop"`
	MemoryBrief MemoryBriefConfig `json:"memory_brief"`
	Archive     ArchiveConfig     `json:"archive"`
	Schedule    ScheduleConfig    `json:"schedule"`

	// Role-specific: main agent.
	SessionRotateThresholdPct int     `json:"session_rotate_threshold_pct,omitempty"`
	FocusStaleThresholdMsgs   int     `json:"focus_stale_threshold_msgs,omitempty"`
	HeartbeatIntervalSeconds  int     `json:"heartbeat_interval_seconds,omitempty"`
	TokenEstimateMultiplier   float64 `json:"token_estimate_multiplier,omitempty"`
	CalibrationIntervalMin    int     `json:"calibration_interval_min,omitempty"`
	BudgetCacheIntervalMin    int     `json:"budget_cache_interval_min,omitempty"`
	BudgetSafetyMultiplier    float64 `json:"budget_safety_multiplier,omitempty"`

	// Role-specific: archivist.
	MaxToolCalls             int      `json:"max_tool_calls,omitempty"`
	BuildPromptTimeoutMs     int      `json:"build_prompt_timeout_ms,omitempty"`
	ReasoningGuidedRetrieval bool     `json:"reasoning_guided_retrieval,omitempty"`
	ReasoningKeywordsMax     int      `json:"reasoning_keywords_max,omitempty"`
	ReasoningDriftOverlapMin float64  `json:"reasoning_drift_overlap_min,omitempty"`
	TopicTriggers            []string `json:"topic_triggers,omitempty"`

	// Role-specific: atomizer.
	TriggerTokens  int `json:"trigger_tokens,omitempty"`
	ChunkMaxTokens int `json:"chunk_max_tokens,omitempty"`

	// Role-specific: MCP Guard.
	TimeoutMs                int     `json:"timeout_ms,omitempty"`
	SuspiciousTextRatio      float64 `json:"suspicious_text_ratio,omitempty"`
	SuspiciousSizeMultiplier float64 `json:"suspicious_size_multiplier,omitempty"`
	StartupAuditEnabled      bool    `json:"startup_audit_enabled,omitempty"`
	ReauditOnListChanged     bool    `json:"reaudit_on_list_changed,omitempty"`
	HashAlgorithm            string  `json:"hash_algorithm,omitempty"`
}

// ResolveAgentConfig finds an agent by ID in Config.Agents.List and merges
// Config.Agents.Defaults with the agent's overrides.
// PIKA-V3: Full resolution of all Pika-specific fields.
func (c *Config) ResolveAgentConfig(name string) ResolvedAgentConfig {
	d := c.Agents.Defaults

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

		// PIKA-V3: paths from defaults
		MemoryDBPath:     d.MemoryDBPath,
		BaseToolsDir:     d.BaseToolsDir,
		SkillsDir:        d.SkillsDir,
		MaxToolsInPrompt: d.MaxToolsInPrompt,

		// PIKA-V3: telemetry
		TelemetryEnabled:       d.TelemetryEnabled,
		TelemetryRetentionDays: d.TelemetryRetentionDays,

		// PIKA-V3: retry/loop
		MaxRetriesPerMessage:   d.MaxRetriesPerMessage,
		ToolCallRetryEnabled:   d.ToolCallRetryEnabled,
		LoopDetectionThreshold: d.LoopDetectionThreshold,

		// PIKA-V3: debug/idle
		PromptDebug:    d.PromptDebug,
		IdleTimeoutMin: d.IdleTimeoutMin,
	}

	// Resolve pointer defaults
	if d.Temperature != nil {
		resolved.Temperature = *d.Temperature
	}
	if d.TopP != nil {
		resolved.TopP = d.TopP // copy pointer
	}
	if d.TopK != nil {
		resolved.TopK = d.TopK // copy pointer
	}

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

	// --- Apply per-agent overrides ---
	resolved.ID = agent.ID
	if agent.Name != "" {
		resolved.Name = agent.Name
	}
	if agent.Workspace != "" {
		resolved.Workspace = agent.Workspace
	}
	if agent.Model != nil && agent.Model.Primary != "" {
		resolved.ModelName = agent.Model.Primary
	}

	// Pointer overrides: nil = inherit, non-nil = override (even if zero!)
	if agent.Temperature != nil {
		resolved.Temperature = *agent.Temperature
	}
	if agent.TopP != nil {
		resolved.TopP = agent.TopP
	}
	if agent.TopK != nil {
		resolved.TopK = agent.TopK
	}
	if agent.Enabled != nil {
		resolved.Enabled = *agent.Enabled
	}

	// String overrides: "" = inherit
	if agent.PromptFile != "" {
		resolved.PromptFile = agent.PromptFile
	}
	if agent.BootstrapFiles != nil {
		resolved.BootstrapFiles = agent.BootstrapFiles
	}

	// Nested struct overrides: nil = inherit/zero, non-nil = replace entire
	if agent.Reasoning != nil {
		resolved.Reasoning = *agent.Reasoning
	}
	if agent.Budget != nil {
		resolved.Budget = *agent.Budget
	}
	if agent.OutputGate != nil {
		resolved.OutputGate = *agent.OutputGate
	}
	if agent.Loop != nil {
		resolved.Loop = *agent.Loop
	}
	if agent.MemoryBrief != nil {
		resolved.MemoryBrief = *agent.MemoryBrief
	}
	if agent.Archive != nil {
		resolved.Archive = *agent.Archive
	}
	if agent.Schedule != nil {
		resolved.Schedule = *agent.Schedule
	}

	// Role-specific int/float: zero = inherit
	if agent.SessionRotateThresholdPct != 0 {
		resolved.SessionRotateThresholdPct = agent.SessionRotateThresholdPct
	}
	if agent.FocusStaleThresholdMsgs != 0 {
		resolved.FocusStaleThresholdMsgs = agent.FocusStaleThresholdMsgs
	}
	if agent.HeartbeatIntervalSeconds != 0 {
		resolved.HeartbeatIntervalSeconds = agent.HeartbeatIntervalSeconds
	}
	if agent.TokenEstimateMultiplier != 0 {
		resolved.TokenEstimateMultiplier = agent.TokenEstimateMultiplier
	}
	if agent.CalibrationIntervalMin != 0 {
		resolved.CalibrationIntervalMin = agent.CalibrationIntervalMin
	}
	if agent.BudgetCacheIntervalMin != 0 {
		resolved.BudgetCacheIntervalMin = agent.BudgetCacheIntervalMin
	}
	if agent.BudgetSafetyMultiplier != 0 {
		resolved.BudgetSafetyMultiplier = agent.BudgetSafetyMultiplier
	}
	if agent.MaxToolCalls != 0 {
		resolved.MaxToolCalls = agent.MaxToolCalls
	}
	if agent.BuildPromptTimeoutMs != 0 {
		resolved.BuildPromptTimeoutMs = agent.BuildPromptTimeoutMs
	}
	if agent.ReasoningGuidedRetrieval != nil {
		resolved.ReasoningGuidedRetrieval = *agent.ReasoningGuidedRetrieval
	}
	if agent.ReasoningKeywordsMax != 0 {
		resolved.ReasoningKeywordsMax = agent.ReasoningKeywordsMax
	}
	if agent.ReasoningDriftOverlapMin != 0 {
		resolved.ReasoningDriftOverlapMin = agent.ReasoningDriftOverlapMin
	}
	if agent.TopicTriggers != nil {
		resolved.TopicTriggers = agent.TopicTriggers
	}
	if agent.TriggerTokens != 0 {
		resolved.TriggerTokens = agent.TriggerTokens
	}
	if agent.ChunkMaxTokens != 0 {
		resolved.ChunkMaxTokens = agent.ChunkMaxTokens
	}
	if agent.TimeoutMs != 0 {
		resolved.TimeoutMs = agent.TimeoutMs
	}
	if agent.SuspiciousTextRatio != 0 {
		resolved.SuspiciousTextRatio = agent.SuspiciousTextRatio
	}
	if agent.SuspiciousSizeMultiplier != 0 {
		resolved.SuspiciousSizeMultiplier = agent.SuspiciousSizeMultiplier
	}
	if agent.StartupAuditEnabled != nil {
		resolved.StartupAuditEnabled = *agent.StartupAuditEnabled
	}
	if agent.ReauditOnListChanged != nil {
		resolved.ReauditOnListChanged = *agent.ReauditOnListChanged
	}
	if agent.HashAlgorithm != "" {
		resolved.HashAlgorithm = agent.HashAlgorithm
	}

	return resolved
}

// PIKA-V3: mergeAPIKeys moved from migration.go (legacy cleanup).
// Used by multikey_test.go for API key deduplication tests.
func mergeAPIKeys(apiKey string, apiKeys []string) []string {
	seen := make(map[string]struct{})
	var all []string

	if k := strings.TrimSpace(apiKey); k != "" {
		if _, exists := seen[k]; !exists {
			seen[k] = struct{}{}
			all = append(all, k)
		}
	}

	for _, k := range apiKeys {
		if trimmed := strings.TrimSpace(k); trimmed != "" {
			if _, exists := seen[trimmed]; !exists {
				seen[trimmed] = struct{}{}
				all = append(all, trimmed)
			}
		}
	}

	return all
}
