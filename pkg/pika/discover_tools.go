package pika

import (
	"encoding/json"
	"fmt"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// PIKA-V3: DiscoverTools — 🧠 BRAIN tool for dynamic tool discovery.
// Returns a structured catalog of all registered tools (brain + base + skill).
// Registered via toolRouter.RegisterBrain(dt).

// DiscoverTools exposes the tool catalog to the LLM so it can
// reason about available capabilities without hard-coding names.
type DiscoverTools struct {
	router *ToolRouter
}

// NewDiscoverTools creates a DiscoverTools tool backed by the given router.
func NewDiscoverTools(router *ToolRouter) *DiscoverTools {
	return &DiscoverTools{router: router}
}

// Name implements toolshared.Tool.
func (dt *DiscoverTools) Name() string { return "discover_tools" }

// Description implements toolshared.Tool.
func (dt *DiscoverTools) Description() string {
	return "Returns the catalog of all registered tools " +
		"grouped by category (brain/base/skill). " +
		"Use this to discover what capabilities are available."
}

// Parameters implements toolshared.Tool.
func (dt *DiscoverTools) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category": map[string]any{
				"type": "string",
				"enum": []string{
					"brain", "base", "skill",
				},
				"description": "Optional filter: " +
					"return only tools of this category.",
			},
		},
	}
}

// catalogEntry is one tool in the returned JSON catalog.
type catalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// Execute implements toolshared.Tool.
func (dt *DiscoverTools) Execute(
	args map[string]any,
) (toolshared.ToolResult, error) {
	// Optional category filter.
	var filterCat string
	if v, ok := args["category"].(string); ok {
		filterCat = v
	}

	// Collect definitions: name -> description.
	defs := dt.router.ToolDefinitions()
	defMap := make(map[string]string, len(defs))
	for _, d := range defs {
		defMap[d.Function.Name] = d.Function.Description
	}

	// Collect category grouping: category -> []name.
	catNames := dt.router.EnabledToolNames()

	var catalog []catalogEntry
	for cat, names := range catNames {
		catStr := cat.String()
		if filterCat != "" && catStr != filterCat {
			continue
		}
		for _, name := range names {
			desc := defMap[name]
			catalog = append(catalog, catalogEntry{
				Name:        name,
				Description: desc,
				Category:    catStr,
			})
		}
	}

	payload := map[string]any{
		"total_tools": len(catalog),
		"catalog":     catalog,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return toolshared.ToolResult{
			IsError: true,
			ForLLM: fmt.Sprintf(
				"pika/discover_tools: marshal: %v", err),
		}, nil
	}

	return toolshared.ToolResult{
		ForLLM: string(data),
		Silent: true,
	}, nil
}

// PromptMetadata implements toolshared.PromptMetadataProvider.
func (dt *DiscoverTools) PromptMetadata() toolshared.PromptMetadata {
	return toolshared.PromptMetadata{
		Layer:  toolshared.ToolPromptLayerCapability,
		Slot:   toolshared.ToolPromptSlotTooling,
		Source: toolshared.ToolPromptSourceDiscovery,
	}
}
