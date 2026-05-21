package pika

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/tools"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// PIKA-V3: DiscoverTools — 🧠 BRAIN tool for dynamic tool discovery.
// Returns visible + hidden tools from upstream ToolRegistry.
// Registered via toolsRegistry.Register() — IsCore=true, always in prompt.

// DiscoverTools exposes the tool catalog to the LLM so it can
// reason about available capabilities without hard-coding names.
type DiscoverTools struct {
	registry *tools.ToolRegistry
}

// NewDiscoverTools creates a DiscoverTools tool backed by the upstream ToolRegistry.
func NewDiscoverTools(registry *tools.ToolRegistry) *DiscoverTools {
	return &DiscoverTools{registry: registry}
}

// Name implements toolshared.Tool.
func (dt *DiscoverTools) Name() string { return "discover_tools" }

// Description implements toolshared.Tool.
func (dt *DiscoverTools) Description() string {
	return "Returns the catalog of all registered tools. " +
		"Visible tools are active now; hidden tools can be promoted on demand. " +
		"Use this to discover what capabilities are available."
}

// Parameters implements toolshared.Tool.
func (dt *DiscoverTools) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"include_hidden": map[string]any{
				"type":        "boolean",
				"description": "If true, also return hidden (promotable) tools. Default false.",
			},
		},
	}
}

// catalogEntry is one tool in the returned JSON catalog.
type catalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"` // "visible" or "hidden"
}

// Execute implements toolshared.Tool.
func (dt *DiscoverTools) Execute(
	_ context.Context, args map[string]any,
) *toolshared.ToolResult {
	includeHidden := false
	if v, ok := args["include_hidden"].(bool); ok {
		includeHidden = v
	}

	// Visible tools: IsCore=true or TTL>0.
	allNames := dt.registry.List()
	visible := dt.registry.GetSummaries()

	var catalog []catalogEntry

	// Build visible entries from GetSummaries (formatted strings).
	// More reliable: iterate all tools and check visibility via Get().
	for _, name := range allNames {
		if tool, ok := dt.registry.Get(name); ok {
			catalog = append(catalog, catalogEntry{
				Name:        tool.Name(),
				Description: tool.Description(),
				Status:      "visible",
			})
		}
	}

	// Hidden tools (non-core, TTL<=0).
	var hiddenCount int
	if includeHidden {
		snapshot := dt.registry.SnapshotHiddenTools()
		hiddenCount = len(snapshot.Docs)
		for _, doc := range snapshot.Docs {
			catalog = append(catalog, catalogEntry{
				Name:        doc.Name,
				Description: doc.Description,
				Status:      "hidden",
			})
		}
	}

	payload := map[string]any{
		"visible_count": len(visible),
		"hidden_count":  hiddenCount,
		"total":         len(catalog),
		"catalog":       catalog,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return toolshared.ErrorResult(
			fmt.Sprintf("pika/discover_tools: marshal: %v", err),
		)
	}

	return &toolshared.ToolResult{
		ForLLM: string(data),
		Silent: true,
	}
}

// PromptMetadata implements toolshared.PromptMetadataProvider.
func (dt *DiscoverTools) PromptMetadata() toolshared.PromptMetadata {
	return toolshared.PromptMetadata{
		Layer:  toolshared.ToolPromptLayerCapability,
		Slot:   toolshared.ToolPromptSlotTooling,
		Source: toolshared.ToolPromptSourceDiscovery,
	}
}
