package agent

import (
	"context"
	"strings"
)

// PromptSourceAgentDiscovery identifies the peer-agent discovery prompt source.
// PIKA-PORT: upstream v0.2.9 (feat/agent-discovery-prompt), rewritten against
// our PromptBuildRequest API (no per-agent allowlist fields).
const PromptSourceAgentDiscovery PromptSourceID = "agent:discovery"

// agentDiscoveryPromptContributor injects the list of peer agents this agent
// is permitted to spawn/delegate to, so the LLM can pick a peer by identity.
type agentDiscoveryPromptContributor struct {
	agentID  string
	discover func(agentID string) []AgentDescriptor
}

func (c agentDiscoveryPromptContributor) PromptSource() PromptSourceDescriptor {
	return PromptSourceDescriptor{
		ID:              PromptSourceAgentDiscovery,
		Owner:           "agent",
		Description:     "Peer agent discovery instructions",
		Allowed:         []PromptPlacement{{Layer: PromptLayerCapability, Slot: PromptSlotTooling}},
		StableByDefault: true,
	}
}

func (c agentDiscoveryPromptContributor) ContributePrompt(
	_ context.Context,
	_ PromptBuildRequest,
) ([]PromptPart, error) {
	if c.discover == nil {
		return nil, nil
	}
	content := formatAgentDiscoverySection(c.discover(c.agentID))
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}

	return []PromptPart{
		{
			ID:      "capability.agent_discovery",
			Layer:   PromptLayerCapability,
			Slot:    PromptSlotTooling,
			Source:  PromptSource{ID: PromptSourceAgentDiscovery, Name: "agent:discovery"},
			Title:   "agent discovery",
			Content: content,
			Stable:  true,
			Cache:   PromptCacheEphemeral,
		},
	}, nil
}
