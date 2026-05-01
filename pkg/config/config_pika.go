// PIKA-V3: Pika-specific configuration types and functions.
//
// All new types introduced by Pika v3 unified config:
// - Cross-agent: ClarifyConfig, SecurityConfig, HealthConfig
// - Per-agent nested: ReasoningConfig, BudgetConfig, OutputGateConfig, etc.
// - BaseToolsConfig for D-TOOL-CLASS §4.1b
// - ResolvedAgentConfig + ResolveAgentConfig() for config merge
// - IsBaseToolEnabled() for BASE tool master switch
//
// NOTE: ResolveAgentConfig() and IsBaseToolEnabled() are methods on *Config
// that reference new fields on Config, AgentDefaults, AgentConfig, and
// ToolsConfig structs. These fields must be added to config.go for
// this file to compile. See PR description for exact patches.
package config

import (
	"encoding/json"
	"fmt"
)

// ========================================================================
// Cross-agent configs (top-level in Config struct)
// ========================================================================

// ClarifyConfig controls the clarification loop behavior.
// PIKA-V3: Added to Config struct as Clarify field.
type ClarifyConfig struct {
	Enabled              bool `json:"enabled"`                   // default: true
	TimeoutMin           int  `json:"timeout_min"`               // default: 30
	MaxStreakBeforeBypass int  `json:"max_streak_before_bypass"` // default: 2
	PrecheckTimeoutMs    int  `json:"precheck_timeout_ms"`      // default: 3000
}

// SecurityConfig groups all security-related settings.
// PIKA-V3: Added to Config struct as Security field.
type SecurityConfig struct {
	DangerousOps DangerousOpsConfig `json:"dangerous_ops"`
	RAD          RADConfig          `json:"rad"`
	MCP          MCPSecurityConfig  `json:"mcp"`
}

// DangerousOpsConfig controls confirmation flow for dangerous operations.
type DangerousOpsConfig struct {
	Ops               map[string]DangerousOpEntry `json:"ops"`
	ConfirmTimeoutMin int                         `json:"confirm_timeout_min"` // default: 30
	CriticalPaths     []string                    `json:"critical_paths"`
}

// ConfirmMode is a typed confirmation policy.
// JSON unmarshal supports both string ("always","never","if_healthy","if_critical_path")
// and bool (true → "always", false → "never") for convenience.
type ConfirmMode string

const (
	ConfirmAlways     ConfirmMode = "always"
	ConfirmNever      ConfirmMode = "never"
	ConfirmIfHealthy  ConfirmMode = "if_healthy"
	ConfirmIfCritical ConfirmMode = "if_critical_path"
)

// UnmarshalJSON implements custom JSON unmarshaling for ConfirmMode.
// Supports both string and bool values.
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
	return fmt.Errorf("pika/config: confirm: expected string or bool, got %s", data)
}

// DangerousOpEntry describes a single dangerous operation.
type DangerousOpEntry struct {
	Level   string      `json:"level"`   // "critical"|"high"|"medium"
	Confirm ConfirmMode `json:"confirm"` // ConfirmAlways|ConfirmNever|ConfirmIfHealthy|ConfirmIfCritical
}

// RADConfig controls Request Anomaly Detection.
type RADConfig struct {
	Enabled        bool    `json:"enabled"`         // default: true
	DriftThreshold float64 `json:"drift_threshold"` // default: 0.2
	BlockScore     int     `json:"block_score"`     // default: 3
	WarnScore      int     `json:"warn_score"`      // default: 2
}

// MCPSecurityConfig controls MCP server security settings.
type MCPSecurityConfig struct {
	TaintResetPolicy      string          `json:"taint_reset_policy"`      // default: "explicit_only"
	StdioUser             string          `json:"stdio_user"`              // default: "mcp-sandbox"
	StdioIsolation        string          `json:"stdio_isolation"`         // default: "user"
	PerServerRPM          int             `json:"per_server_rpm"`          // default: 60
	DefaultCapabilities   map[string]bool `json:"default_capabilities"`
	DefaultAllowPrompts   bool            `json:"default_allow_prompts"`   // default: false
	DefaultAllowResources bool            `json:"default_allow_resources"` // default: true
}

// HealthConfig groups health monitoring settings.
// PIKA-V3: Added to Config struct as Health field.
type HealthConfig struct {
	WindowSize           int                    `json:"window_size"`             // default: 5
	ToolFailThresholdPct int                    `json:"tool_fail_threshold_pct"` // default: 30
	LatencyThresholdMs   int                    `json:"latency_threshold_ms"`    // default: 30000
	FallbackProvider     FallbackProviderConfig `json:"fallback_provider"`
	Reporting            HealthReportingConfig  `json:"reporting"`
	Progress             ProgressConfig         `json:"progress"`
}

// FallbackProviderConfig configures the emergency fallback LLM provider.
type FallbackProviderConfig struct {
	Provider  string `json:"provider"`    // default: "stepfun"
	APIKeyEnv string `json:"api_key_env"` // default: "STEPFUN_API_KEY"
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"` // default: "step-3.5-flash"
}

// HealthReportingConfig controls how health status is communicated.
type HealthReportingConfig struct {
	TypingIndicatorEnabled    bool `json:"typing_indicator_enabled"`     // default: true
	AlertDedupPerSession      bool `json:"alert_dedup_per_session"`      // default: true
	DailyHealthSummaryEnabled bool `json:"daily_health_summary_enabled"` // default: true
}

// ProgressConfig controls progress indicator behavior.
type ProgressConfig struct {
	Enabled            bool `json:"enabled"`              // default: true
	ThrottleSec        int  `json:"throttle_sec"`         // default: 2
	DeleteOnComplete   bool `json:"delete_on_complete"`   // default: true
	ShowStepText       bool `json:"show_step_text"`       // default: true
	StopCommandEnabled bool `json:"stop_command_enabled"` // default: true
}

// ========================================================================
// Nested agent configs (per-agent overrides in AgentConfig)
// ========================================================================

// ReasoningConfig controls LLM reasoning/thinking behavior.
type ReasoningConfig struct {
	Effort      string `json:"effort"`        // default: "medium"
	Exclude     bool   `json:"exclude"`       // default: false
	LogMaxChars int    `json:"log_max_chars"` // default: 2000
}

// BudgetConfig controls per-agent cost budgets.
type BudgetConfig struct {
	DailyUSD   float64 `json:"daily_usd"`   // default: 2.00
	SessionUSD float64 `json:"session_usd"` // default: 0.50
	WarnPct    int     `json:"warn_pct"`    // default: 80
}

// OutputGateConfig controls output length gate.
type OutputGateConfig struct {
	Enabled    bool `json:"enabled"`     // default: true
	MaxChars   int  `json:"max_chars"`   // default: 3500
	MaxRetries int  `json:"max_retries"` // default: 3
}

// LoopConfig controls agent loop detection and overflow handling.
type LoopConfig struct {
	ChainMaxCalls            int  `json:"chain_max_calls"`             // default: 8
	TaskOverflowEnabled      bool `json:"task_overflow_enabled"`       // default: true
	OverflowChunkSize        int  `json:"overflow_chunk_size"`         // default: 5
	ProactiveRotateThreshold int  `json:"proactive_rotate_threshold"` // default: 3
	AvgTokensWindowSize      int  `json:"avg_tokens_window_size"`     // default: 10
}

// MemoryBriefConfig controls memory brief construction by Archivarius.
type MemoryBriefConfig struct {
	SoftLimit                 int      `json:"soft_limit"`                  // default: 5000
	HardLimit                 int      `json:"hard_limit"`                  // default: 6000
	CompressProtectedSections []string `json:"compress_protected_sections"` // default: ["AVOID","CONSTRAINTS"]
	MaxRetries                int      `json:"max_retries"`                 // default: 3
}

// ArchiveConfig controls knowledge archive retrieval.
type ArchiveConfig struct {
	FTSTopN         int `json:"fts_top_n"`         // default: 20
	FTSWindow       int `json:"fts_window"`        // default: 2
	ReadMaxSnippets int `json:"read_max_snippets"` // default: 10
	ReadTimeoutMs   int `json:"read_timeout_ms"`   // default: 5000
}

// ScheduleConfig controls scheduled tasks for background agents.
type ScheduleConfig struct {
	Daily   string `json:"daily"`   // default: "03:00"
	Weekly  string `json:"weekly"`  // default: "Sun 04:00"
	Monthly string `json:"monthly"` // default: "1st 05:00"
}

// ========================================================================
// BaseToolsConfig (D-TOOL-CLASS §4.1b)
// ========================================================================

// BaseToolsConfig controls the master switch and per-tool toggles for BASE tools.
// BRAIN tools (search_memory, registry_write, clarify) are always active and
// not controlled by this config.
// PIKA-V3: Added to ToolsConfig as BaseTools field.
type BaseToolsConfig struct {
	Enabled    bool `json:"enabled"`     // master switch, default: true
	Exec       bool `json:"exec"`        // default: true
	ReadFile   bool `json:"read_file"`   // default: true
	WriteFile  bool `json:"write_file"`  // default: true
	EditFile   bool `json:"edit_file"`   // default: true
	AppendFile bool `json:"append_file"` // default: true
	ListDir    bool `json:"list_dir"`    // default: true
}

// PIKA-V3: IsBaseToolEnabled returns true if a BASE tool is enabled.
// BRAIN tools always return false here (they are unconditionally active elsewhere).
// A tool is active if: IsToolEnabled(name) && (!isBASE || IsBaseToolEnabled(name)).
//
// Requires ToolsConfig.BaseTools field in config.go.
func (c *Config) IsBaseToolEnabled(toolName string) bool {
	if !c.Tools.BaseTools.Enabled {
		return false
	}
	switch toolName {
	case "exec":
		return c.Tools.BaseTools.Exec
	case "read_file":
		return c.Tools.BaseTools.ReadFile
	case "write_file":
		return c.Tools.BaseTools.WriteFile
	case "edit_file":
		return c.Tools.BaseTools.EditFile
	case "append_file":
		return c.Tools.BaseTools.AppendFile
	case "list_dir":
		return c.Tools.BaseTools.ListDir
	default:
		return false
	}
}

// ========================================================================
// ResolvedAgentConfig — fully resolved config for downstream consumers
// ========================================================================

// ResolvedAgentConfig is the fully resolved agent configuration.
// All pointer overrides from AgentConfig are flattened to value types.
// Downstream consumers should use this instead of reading AgentConfig directly.
type ResolvedAgentConfig struct {
	// Identity
	ID   string
	Name string

	// Upstream (from AgentDefaults)
	Workspace         string
	ModelName         string
	MaxTokens         int
	Temperature       float64
	ContextWindow     int
	MaxToolIterations int
	ContextManager    string

	// PIKA-V3: paths
	MemoryDBPath     string
	BaseToolsDir     string
	SkillsDir        string
	MaxToolsInPrompt int

	// PIKA-V3: model params
	TopP float64
	TopK int

	// PIKA-V3: telemetry
	TelemetryEnabled       bool
	TelemetryRetentionDays int

	// PIKA-V3: retry/loop
	MaxRetriesPerMessage   int
	ToolCallRetryEnabled   bool
	LoopDetectionThreshold int

	// PIKA-V3: debug/idle
	PromptDebug    bool
	IdleTimeoutMin int

	// PIKA-V3: per-agent
	PromptFile     string
	Enabled        bool
	BootstrapFiles []string

	// PIKA-V3: nested configs (resolved, value types)
	Reasoning   ReasoningConfig
	Budget      BudgetConfig
	OutputGate  OutputGateConfig
	Loop        LoopConfig
	MemoryBrief MemoryBriefConfig
	Archive     ArchiveConfig
	Schedule    ScheduleConfig

	// Main-agent specific
	SessionRotateThresholdPct int
	FocusStaleThresholdMsgs   int
	HeartbeatIntervalSeconds  int
	TokenEstimateMultiplier   float64
	CalibrationIntervalMin    int
	BudgetCacheIntervalMin    int
	BudgetSafetyMultiplier    float64

	// Archivist-specific
	MaxToolCalls             int
	BuildPromptTimeoutMs     int
	ReasoningGuidedRetrieval bool
	ReasoningKeywordsMax     int
	ReasoningDriftOverlapMin float64
	TopicTriggers            []string

	// Atomizer-specific
	TriggerTokens  int
	ChunkMaxTokens int

	// MCP Guard-specific
	TimeoutMs                int
	SuspiciousTextRatio      float64
	SuspiciousSizeMultiplier float64
	StartupAuditEnabled      bool
	ReauditOnListChanged     bool
	HashAlgorithm            string
}

// PIKA-V3: ResolveAgentConfig resolves the full configuration for a named agent.
// It merges cfg.Agents.Defaults with per-agent cfg.Agents.List[i] overrides.
//
// Override rules:
//   - Pointer fields:  nil = inherit from Defaults, non-nil = override (0.0 preserved!)
//   - Value fields:    zero = inherit, non-zero = override
//   - Slices:          nil = inherit, non-nil (including empty) = override
//   - Nested structs:  nil pointer = use defaults, non-nil = replace entire struct
//
// If the agent name is not found in Agents.List, returns a copy of Defaults.
//
// Requires new Pika-specific fields on AgentDefaults and AgentConfig in config.go.
func (c *Config) ResolveAgentConfig(name string) ResolvedAgentConfig {
	d := c.Agents.Defaults

	// 1. Start with flat copy of Defaults
	resolved := ResolvedAgentConfig{
		ID:                name,
		Name:              name,
		Workspace:         d.Workspace,
		ModelName:         d.GetModelName(),
		MaxTokens:         d.MaxTokens,
		ContextWindow:     d.ContextWindow,
		MaxToolIterations: d.MaxToolIterations,
		ContextManager:    d.ContextManager,
		// PIKA-V3 paths
		MemoryDBPath:     d.MemoryDBPath,
		BaseToolsDir:     d.BaseToolsDir,
		SkillsDir:        d.SkillsDir,
		MaxToolsInPrompt: d.MaxToolsInPrompt,
		// PIKA-V3 telemetry
		TelemetryEnabled:       d.TelemetryEnabled,
		TelemetryRetentionDays: d.TelemetryRetentionDays,
		// PIKA-V3 retry
		MaxRetriesPerMessage:   d.MaxRetriesPerMessage,
		ToolCallRetryEnabled:   d.ToolCallRetryEnabled,
		LoopDetectionThreshold: d.LoopDetectionThreshold,
		// PIKA-V3 debug
		PromptDebug:    d.PromptDebug,
		IdleTimeoutMin: d.IdleTimeoutMin,
		// Defaults
		Enabled: true,
	}

	// Resolve pointer defaults
	if d.Temperature != nil {
		resolved.Temperature = *d.Temperature
	}
	if d.TopP != nil {
		resolved.TopP = *d.TopP
	}
	if d.TopK != nil {
		resolved.TopK = *d.TopK
	}

	// 2. Find agent by ID in Agents.List
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

	// 3. Override non-nil/non-zero fields from AgentConfig

	// Identity
	if agent.Name != "" {
		resolved.Name = agent.Name
	}
	if agent.Workspace != "" {
		resolved.Workspace = agent.Workspace
	}

	// Model
	if agent.Model != nil && agent.Model.Primary != "" {
		resolved.ModelName = agent.Model.Primary
	}

	// Pointer fields: nil = inherit, non-nil = override (preserves zero values!)
	if agent.Temperature != nil {
		resolved.Temperature = *agent.Temperature
	}
	if agent.TopP != nil {
		resolved.TopP = *agent.TopP
	}
	if agent.TopK != nil {
		resolved.TopK = *agent.TopK
	}
	if agent.Enabled != nil {
		resolved.Enabled = *agent.Enabled
	}

	// String fields: empty = inherit
	if agent.PromptFile != "" {
		resolved.PromptFile = agent.PromptFile
	}

	// Slices: nil = inherit, non-nil = override
	if agent.BootstrapFiles != nil {
		resolved.BootstrapFiles = agent.BootstrapFiles
	}
	if agent.TopicTriggers != nil {
		resolved.TopicTriggers = agent.TopicTriggers
	}

	// Nested structs: nil pointer = defaults, non-nil = replace entire struct
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

	// Int fields: zero = inherit, non-zero = override
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

	// Archivist
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

	// Atomizer
	if agent.TriggerTokens != 0 {
		resolved.TriggerTokens = agent.TriggerTokens
	}
	if agent.ChunkMaxTokens != 0 {
		resolved.ChunkMaxTokens = agent.ChunkMaxTokens
	}

	// MCP Guard
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
