// PIKA-V3: config_mappers.go — mappers from ResolvedAgentConfig to subagent configs.
// ТЗ-v2-8h. Isolates config mapping from wiring logic in context_pika.go.

package agent

import (
	"path/filepath"

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

// mapMCPServerPolicies строит политику по умолчанию для каждого включённого
// MCP-сервера из конфига (Р-3, D-AUDIT-50). Per-server ACL (AllowedTools,
// TrustLevel) — этап 5: пока каждый сервер получает deny-by-default политику
// с общими дефолтами из security.mcp.
func mapMCPServerPolicies(cfg *config.Config) []pika.MCPServerPolicy {
	if cfg == nil {
		return nil
	}
	servers := cfg.Tools.MCP.Servers
	if len(servers) == 0 {
		return nil
	}
	mcpCfg := cfg.Security.MCP
	policies := make([]pika.MCPServerPolicy, 0, len(servers))
	for name, srv := range servers {
		if !srv.Enabled {
			continue
		}
		policies = append(policies, pika.MCPServerPolicy{
			Name:           name,
			TrustLevel:     "external",
			Capabilities:   mcpCfg.DefaultCapabilities,
			AllowPrompts:   mcpCfg.DefaultAllowPrompts,
			AllowResources: mcpCfg.DefaultAllowResources,
			RPM:            mcpCfg.PerServerRPM,
		})
	}
	return policies
}

// pikaPromptPaths — карта компонент → файл промта в workspace (Р-3).
// Ключи обязаны совпадать с validCRComponents в pkg/pika/diagnostics.go.
func pikaPromptPaths(workspace string) map[string]string {
	dir := filepath.Join(workspace, "prompts")
	return map[string]string{
		"archivist": filepath.Join(dir, "archivist_build.md"),
		"atomizer":  filepath.Join(dir, "atomizer.md"),
		"reflexor":  filepath.Join(dir, "reflexor.md"),
		"mcp_guard": filepath.Join(dir, "mcp_guard.md"),
	}
}
