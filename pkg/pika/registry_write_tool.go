// PIKA-V3: RegistryWriteTool — 🧠 BRAIN tool для записи в registry (D-104,
// ТЗ-v2-3f). Модель сохраняет runbook'и, скрипты, снапшоты и correction rules
// в постоянный реестр. Go — единственный писатель: модель вызывает тул,
// RegistryHandler валидирует и пишет в SQLite.

package pika

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// maxRegistryDataBytes — верхний предел data payload (ТЗ-v2-3f, риск 1).
const maxRegistryDataBytes = 64 * 1024

// RegistryWriteTool — Go-native 🧠 BRAIN tool, stateless singleton.
// Обёртка над RegistryHandler (registry.go), реализует toolshared.Tool.
type RegistryWriteTool struct {
	handler *RegistryHandler
}

// NewRegistryWriteTool creates the tool over an existing RegistryHandler.
func NewRegistryWriteTool(handler *RegistryHandler) *RegistryWriteTool {
	return &RegistryWriteTool{handler: handler}
}

func (t *RegistryWriteTool) Name() string {
	return "registry_write"
}

func (t *RegistryWriteTool) Description() string {
	return "Save a runbook, script, snapshot, or correction rule to persistent registry. " +
		"Use kind='runbook' for step-by-step procedures, 'script' for reusable code, " +
		"'snapshot' for state captures, 'correction_rule' for behavior corrections. " +
		"Existing entries with same kind+key are updated."
}

func (t *RegistryWriteTool) Parameters() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"kind", "key"},
		"properties": map[string]any{
			"kind": map[string]any{
				"type":        "string",
				"enum":        []string{"runbook", "script", "snapshot", "correction_rule"},
				"description": "Type of registry entry",
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Unique key within kind (max 255 chars). e.g. 'deploy_compose', 'backup_db'",
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "Human-readable summary of what this entry does",
			},
			"data": map[string]any{
				"type":        "object",
				"description": "JSON payload (content depends on kind)",
			},
			"tags": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Tags for search/filtering",
			},
		},
	}
}

func (t *RegistryWriteTool) Execute(
	ctx context.Context, args map[string]any,
) *toolshared.ToolResult {
	if t.handler == nil {
		return toolshared.ErrorResult("registry_write: handler not configured")
	}

	kind, _ := args["kind"].(string)
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return toolshared.ErrorResult(
			"invalid kind: required, one of runbook|script|snapshot|correction_rule",
		)
	}
	key, _ := args["key"].(string)
	key = strings.TrimSpace(key)
	if key == "" {
		return toolshared.ErrorResult("key is required")
	}
	summary, _ := args["summary"].(string)

	// data: модель шлёт объект; строку принимаем только если это валидный JSON.
	var data json.RawMessage
	switch v := args["data"].(type) {
	case nil:
	case string:
		if !json.Valid([]byte(v)) {
			return toolshared.ErrorResult("invalid data: not valid JSON")
		}
		data = json.RawMessage(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return toolshared.ErrorResult(fmt.Sprintf("invalid data: %v", err))
		}
		data = raw
	}
	if len(data) > maxRegistryDataBytes {
		return toolshared.ErrorResult(fmt.Sprintf(
			"data too large: %d bytes (max %d)", len(data), maxRegistryDataBytes,
		))
	}

	// tags: только массив строк (строка → ошибка, валидирует и handler).
	var tags json.RawMessage
	switch v := args["tags"].(type) {
	case nil:
	case []string:
		raw, _ := json.Marshal(v)
		tags = raw
	case []any:
		strs := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return toolshared.ErrorResult("invalid tags: all items must be strings")
			}
			strs = append(strs, s)
		}
		raw, _ := json.Marshal(strs)
		tags = raw
	default:
		return toolshared.ErrorResult("invalid tags: must be an array of strings")
	}

	created, err := t.handler.HandleWrite(ctx, kind, key, summary, data, tags)
	if err != nil {
		return toolshared.ErrorResult(fmt.Sprintf("registry write failed: %v", err))
	}
	status := "updated"
	if created {
		status = "created"
	}
	out, _ := json.Marshal(map[string]string{
		"status": status, "kind": kind, "key": key,
	})
	return toolshared.SilentResult(string(out))
}
