package agent

import (
	"context"
	"fmt"
	"strings"
)

type toolDiscoveryPromptContributor struct {
	useBM25  bool
	useRegex bool
}

func (c toolDiscoveryPromptContributor) PromptSource() PromptSourceDescriptor {
	return PromptSourceDescriptor{
		ID:              PromptSourceToolDiscovery,
		Owner:           "tools",
		Description:     "Tool discovery instructions",
		Allowed:         []PromptPlacement{{Layer: PromptLayerCapability, Slot: PromptSlotTooling}},
		StableByDefault: true,
	}
}

func (c toolDiscoveryPromptContributor) ContributePrompt(
	_ context.Context,
	_ PromptBuildRequest,
) ([]PromptPart, error) {
	content := formatToolDiscoveryRule(c.useBM25, c.useRegex)
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}

	return []PromptPart{
		{
			ID:      "capability.tool_discovery",
			Layer:   PromptLayerCapability,
			Slot:    PromptSlotTooling,
			Source:  PromptSource{ID: PromptSourceToolDiscovery, Name: "tool_registry:discovery"},
			Title:   "tool discovery",
			Content: content,
			Stable:  true,
			Cache:   PromptCacheEphemeral,
		},
	}, nil
}

type mcpServerPromptContributor struct {
	serverName string
	toolCount  int
	deferred   bool
	// Волна 121 (срез Б): живой статус сервера (nil → поведение как раньше).
	statusFn func(serverName string) (connected bool, tools int, lastError string, ok bool)
}

func (c mcpServerPromptContributor) PromptSource() PromptSourceDescriptor {
	return PromptSourceDescriptor{
		ID:              mcpPromptSourceID(c.serverName),
		Owner:           "mcp",
		Description:     fmt.Sprintf("MCP server %q capability prompt", c.serverName),
		Allowed:         []PromptPlacement{{Layer: PromptLayerCapability, Slot: PromptSlotMCP}},
		StableByDefault: true,
	}
}

func (c mcpServerPromptContributor) ContributePrompt(
	_ context.Context,
	_ PromptBuildRequest,
) ([]PromptPart, error) {
	serverName := strings.TrimSpace(c.serverName)
	if serverName == "" {
		return nil, nil
	}

	// Волна 121 (срез Б): настроен, но недоступен → модель обязана сказать
	// «переподключи в карточке /mcp», а НЕ «интеграция не установлена»
	// (бой 24 сен: агент честно врал «Notion не подключён» при мёртвом токене).
	if c.statusFn != nil {
		if connected, _, lastError, ok := c.statusFn(serverName); ok && !connected {
			reason := strings.TrimSpace(lastError)
			if reason == "" {
				reason = "unknown"
			}
			return []PromptPart{{
				ID:     "capability.mcp." + promptSourceComponent(serverName),
				Layer:  PromptLayerCapability,
				Slot:   PromptSlotMCP,
				Source: PromptSource{ID: mcpPromptSourceID(serverName), Name: "mcp:" + serverName},
				Title:  "MCP server UNAVAILABLE",
				Content: fmt.Sprintf(
					"MCP server `%s` is CONFIGURED but currently UNAVAILABLE (last error: %s). "+
						"Do NOT claim it is not installed. Instruct the user: open /mcp and reconnect the card; "+
						"if the card shows reconnect_required, re-authentication is required.",
					serverName, reason,
				),
				Stable: false,
				Cache:  PromptCacheEphemeral,
			}}, nil
		}
	}

	if c.toolCount <= 0 {
		return nil, nil
	}

	availability := "available as native tools"
	if c.deferred {
		availability = "hidden behind tool discovery until unlocked"
	}

	return []PromptPart{
		{
			ID:     "capability.mcp." + promptSourceComponent(serverName),
			Layer:  PromptLayerCapability,
			Slot:   PromptSlotMCP,
			Source: PromptSource{ID: mcpPromptSourceID(serverName), Name: "mcp:" + serverName},
			Title:  "MCP server capability",
			Content: fmt.Sprintf(
				"MCP server `%s` is connected. It contributes %d tool(s), currently %s.",
				serverName,
				c.toolCount,
				availability,
			),
			Stable: true,
			Cache:  PromptCacheEphemeral,
		},
	}, nil
}

func mcpPromptSourceID(serverName string) PromptSourceID {
	return PromptSourceID("mcp:" + promptSourceComponent(serverName))
}

func promptSourceComponent(value string) string {
	const maxLen = 64

	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unnamed"
	}

	var b strings.Builder
	lastWasSep := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastWasSep = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastWasSep = false
		case r == '-' || r == '_':
			if !lastWasSep && b.Len() > 0 {
				b.WriteRune(r)
				lastWasSep = true
			}
		default:
			if !lastWasSep && b.Len() > 0 {
				b.WriteRune('_')
				lastWasSep = true
			}
		}
	}

	result := strings.Trim(b.String(), "_")
	if result == "" {
		return "unnamed"
	}
	if len(result) > maxLen {
		return result[:maxLen]
	}
	return result
}
