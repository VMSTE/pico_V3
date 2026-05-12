// PIKA-V3: config_mappers.go — mappers from ResolvedAgentConfig to subagent configs.
// ТЗ-v2-8h. Isolates config mapping from wiring logic in context_pika.go.

package agent

import (
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/pika"
)

// mapAtomizerConfig builds AtomizerConfig from resolved config with fallback to defaults.
func mapAtomizerConfig(resolved config.ResolvedAgentConfig) pika.AtomizerConfig {
	cfg := pika.DefaultAtomizerConfig()
	if resolved.TriggerTokens > 0 {
		cfg.TriggerTokens = resolved.TriggerTokens
	}
	if resolved.ChunkMaxTokens > 0 {
		cfg.ChunkMaxTokens = resolved.ChunkMaxTokens
	}
	if resolved.PromptFile != "" {
		cfg.PromptFile = resolved.PromptFile
	}
	if resolved.ModelName != "" {
		cfg.Model = resolved.ModelName
	}
	cfg.Enabled = resolved.Enabled
	return cfg
}

// mapReflectorConfig builds ReflectorConfig from resolved config with fallback to defaults.
func mapReflectorConfig(resolved config.ResolvedAgentConfig) pika.ReflectorConfig {
	cfg := pika.DefaultReflectorConfig()
	if resolved.PromptFile != "" {
		cfg.PromptFile = resolved.PromptFile
	}
	if resolved.ModelName != "" {
		cfg.Model = resolved.ModelName
	}
	if resolved.TimeoutMs > 0 {
		cfg.TimeoutMs = resolved.TimeoutMs
	}
	cfg.Enabled = resolved.Enabled
	return cfg
}

// mapArchivistConfig builds ArchivistConfig from resolved config with fallback to defaults.
func mapArchivistConfig(resolved config.ResolvedAgentConfig) pika.ArchivistConfig {
	cfg := pika.DefaultArchivistConfig()
	if resolved.PromptFile != "" {
		cfg.PromptFile = resolved.PromptFile
	}
	if resolved.ModelName != "" {
		cfg.Model = resolved.ModelName
	}
	if resolved.MaxToolCalls > 0 {
		cfg.MaxToolCalls = resolved.MaxToolCalls
	}
	if resolved.BuildPromptTimeoutMs > 0 {
		cfg.BuildPromptTimeoutMs = resolved.BuildPromptTimeoutMs
	}
	if resolved.MemoryBrief.SoftLimit > 0 {
		cfg.MemoryBriefSoftLimit = resolved.MemoryBrief.SoftLimit
	}
	if resolved.MemoryBrief.HardLimit > 0 {
		cfg.MemoryBriefHardLimit = resolved.MemoryBrief.HardLimit
	}
	if resolved.MemoryBrief.MaxRetries > 0 {
		cfg.MaxRetriesValidateBrief = resolved.MemoryBrief.MaxRetries
	}
	return cfg
}

// mapMCPGuardConfig builds MCPGuardConfig from resolved config with fallback to defaults.
func mapMCPGuardConfig(resolved config.ResolvedAgentConfig) pika.MCPGuardConfig {
	cfg := pika.DefaultMCPGuardConfig()
	if resolved.PromptFile != "" {
		cfg.PromptFile = resolved.PromptFile
	}
	if resolved.ModelName != "" {
		cfg.Model = resolved.ModelName
	}
	if resolved.TimeoutMs > 0 {
		cfg.TimeoutMs = resolved.TimeoutMs
	}
	if resolved.SuspiciousTextRatio > 0 {
		cfg.SuspiciousTextRatio = resolved.SuspiciousTextRatio
	}
	if resolved.SuspiciousSizeMultiplier > 0 {
		cfg.SuspiciousSizeMultiplier = resolved.SuspiciousSizeMultiplier
	}
	cfg.Enabled = resolved.Enabled
	return cfg
}

// mapTelemetryConfig builds TelemetryConfig from global Health + per-agent Budget.
func mapTelemetryConfig(health config.HealthConfig, budget config.BudgetConfig) pika.TelemetryConfig {
	return pika.TelemetryConfig{
		DailyBudgetUSD:       budget.DailyUSD,
		WindowSize:           health.WindowSize,
		ToolFailThresholdPct: health.ToolFailThresholdPct,
		LatencyThresholdMs:   int64(health.LatencyThresholdMs),
	}
}
