// Волна 107 (ТЗ-107): search_logs — модель читает свои боевые логи.
// Файлы пишет волна 106 (workspace/logs/*.log, JSONL от zerolog).
// Только чтение; имя файла из whitelist; путь всегда внутри workspace/logs;
// секреты замаскированы на записи (maskSecrets, волна 106).

package pika

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	searchLogsDefaultLimit = 20
	searchLogsMaxLimit     = 50
	searchLogsMaxOutBytes  = 8 * 1024
)

var searchLogsLevels = map[string]int{
	"trace":   -1,
	"debug":   0,
	"info":    1,
	"warn":    2,
	"warning": 2,
	"error":   3,
	"fatal":   4,
	"panic":   5,
}

// SearchLogsTool — BRAIN tool: структурный поиск по файловым логам.
type SearchLogsTool struct {
	workspace string
}

// NewSearchLogsTool creates the tool bound to the agent workspace.
func NewSearchLogsTool(workspace string) *SearchLogsTool {
	return &SearchLogsTool{workspace: workspace}
}

// Name returns the tool name.
func (t *SearchLogsTool) Name() string { return "search_logs" }

// Description returns the tool description.
func (t *SearchLogsTool) Description() string {
	return "Search own gateway logs (files under workspace/logs/, " +
		"they persist across restarts). Use to diagnose errors, restarts, " +
		"channel issues. Filters: query substring (message/component/caller), " +
		"minimum level, since_minutes. Returns newest matching lines."
}

// Parameters returns the JSON schema for the tool arguments.
func (t *SearchLogsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Case-insensitive substring matched against message/component/caller",
			},
			"level": map[string]any{
				"type":        "string",
				"enum":        []string{"debug", "info", "warn", "error"},
				"description": "Minimum level (default: all)",
			},
			"source": map[string]any{
				"type":        "string",
				"enum":        []string{"gateway", "errors"},
				"description": "Log file: gateway (everything) or errors (warn+ only). Default: gateway",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max lines to return (1-50, default 20)",
			},
			"since_minutes": map[string]any{
				"type":        "integer",
				"description": "Only entries from the last N minutes",
			},
		},
	}
}

// Execute runs the log search.
func (t *SearchLogsTool) Execute(
	_ context.Context, args map[string]any,
) *toolshared.ToolResult {
	if t.workspace == "" {
		return toolshared.ErrorResult("search_logs: workspace not configured")
	}

	source := logStrArg(args["source"])
	if source == "" {
		source = "gateway"
	}
	if source != "gateway" && source != "errors" {
		return toolshared.ErrorResult(
			fmt.Sprintf("invalid source %q: gateway|errors", source),
		)
	}

	minLevel, err := parseLogLevelArg(args["level"])
	if err != nil {
		return toolshared.ErrorResult(err.Error())
	}

	limit := searchLogsDefaultLimit
	if v, ok := logNumArg(args["limit"]); ok {
		limit = int(v)
	}
	if limit < 1 {
		limit = 1
	}
	if limit > searchLogsMaxLimit {
		limit = searchLogsMaxLimit
	}

	var cutoff time.Time
	if v, ok := logNumArg(args["since_minutes"]); ok && v > 0 {
		cutoff = time.Now().Add(-time.Duration(v) * time.Minute)
	}

	query := strings.ToLower(strings.TrimSpace(logStrArg(args["query"])))

	path := filepath.Join(t.workspace, "logs", source+".log")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return toolshared.SilentResult(fmt.Sprintf(
				"log file not found: %s — no %s logs written yet "+
					"(file logging exists since wave 106)",
				path, source,
			))
		}
		return toolshared.ErrorResult(fmt.Sprintf("read log: %v", err))
	}

	matched := 0
	lines := make([]string, 0, limit)
	for _, raw := range strings.Split(string(data), "\n") {
		raw = strings.TrimRight(raw, "\r")
		if raw == "" {
			continue
		}
		entry, ok := parseLogJSONLine(raw)
		if !ok {
			continue
		}
		if searchLogsLevels[entry.level] < minLevel {
			continue
		}
		if !cutoff.IsZero() && entry.ts != "" {
			if ts, perr := time.Parse(time.RFC3339, entry.ts); perr == nil &&
				ts.Before(cutoff) {
				continue
			}
		}
		if query != "" && !logEntryContains(entry, query) {
			continue
		}
		matched++
		lines = appendLogLine(lines, formatLogLine(entry), limit)
	}

	header := fmt.Sprintf(
		"source=%s matched=%d showing=%d (newest last)",
		source, matched, len(lines),
	)
	if matched == 0 {
		return toolshared.SilentResult(header + "\n(no matching entries)")
	}

	body := strings.Join(lines, "\n")
	if len(body) > searchLogsMaxOutBytes {
		body = body[len(body)-searchLogsMaxOutBytes:]
		if i := strings.IndexByte(body, '\n'); i >= 0 {
			body = body[i+1:]
		}
		header += " [output truncated to last 8KB]"
	}
	return toolshared.SilentResult(header + "\n" + body)
}

type logJSONEntry struct {
	level     string
	ts        string
	component string
	caller    string
	message   string
}

func parseLogJSONLine(raw string) (logJSONEntry, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return logJSONEntry{}, false
	}
	return logJSONEntry{
		level:     logStrArg(m["level"]),
		ts:        logStrArg(m["time"]),
		component: logStrArg(m["component"]),
		caller:    logStrArg(m["caller"]),
		message:   logStrArg(m["message"]),
	}, true
}

func logEntryContains(e logJSONEntry, query string) bool {
	return strings.Contains(strings.ToLower(e.message), query) ||
		strings.Contains(strings.ToLower(e.component), query) ||
		strings.Contains(strings.ToLower(e.caller), query)
}

func formatLogLine(e logJSONEntry) string {
	ts := e.ts
	if len(ts) > 19 {
		ts = ts[:19] // обрезаем зону — читаемость важнее
	}
	comp := ""
	if e.component != "" {
		comp = " [" + e.component + "]"
	}
	return fmt.Sprintf(
		"%s %s%s: %s", ts, strings.ToUpper(e.level), comp, e.message,
	)
}

// appendLogLine держит только последние limit строк (новейшие в хвосте).
func appendLogLine(lines []string, line string, limit int) []string {
	if len(lines) >= limit {
		lines = lines[1:]
	}
	return append(lines, line)
}

func parseLogLevelArg(v any) (int, error) {
	s := strings.ToLower(strings.TrimSpace(logStrArg(v)))
	if s == "" || s == "all" {
		return -2, nil
	}
	lvl, ok := searchLogsLevels[s]
	if !ok {
		return 0, fmt.Errorf("invalid level %q: debug|info|warn|error", s)
	}
	return lvl, nil
}

func logStrArg(v any) string {
	s, _ := v.(string)
	return s
}

func logNumArg(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}
