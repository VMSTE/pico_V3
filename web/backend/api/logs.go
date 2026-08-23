// Волна 106 (ТЗ-106, срез B): чтение файловых логов из workspace/logs/.
// Файл — JSONL от zerolog → структурные записи; фильтры level/q, follow по
// offset (номер строки), reset при ротации. Старый /api/gateway/logs не тронут.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

const (
	logsDefaultLimit = 200
	logsMaxLimit     = 1000
)

var logLevelOrder = map[string]int{
	"trace":   -1,
	"debug":   0,
	"info":    1,
	"warn":    2,
	"warning": 2,
	"error":   3,
	"fatal":   4,
	"panic":   5,
}

type logEntry struct {
	N         int            `json:"n"`
	Level     string         `json:"level"`
	Time      string         `json:"time,omitempty"`
	Component string         `json:"component,omitempty"`
	Caller    string         `json:"caller,omitempty"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
	Raw       string         `json:"raw"`
}

func (h *Handler) registerLogsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/logs", h.handleLogs)
}

// handleLogs возвращает записи файлового лога.
//
//	GET /api/logs?source=gateway|errors|launcher&level=info&q=...&offset=N&limit=200
func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	if source == "" {
		source = "gateway"
	}

	path, err := h.logFilePath(source)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// файл ещё не писался — честный пустой ответ
			writeLogsJSON(w, map[string]any{
				"available": false,
				"source":    source,
				"entries":   []logEntry{},
			})
			return
		}
		http.Error(w, "Failed to read log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	lines := strings.Split(string(data), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total := len(lines)

	minLevel, hasLevel := parseLevelParam(r.URL.Query().Get("level"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit := logsDefaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, aErr := strconv.Atoi(v); aErr == nil && n > 0 && n <= logsMaxLimit {
			limit = n
		}
	}

	matched := make([]logEntry, 0, len(lines))
	for i, line := range lines {
		entry, ok := parseLogLine(line, i+1)
		if !ok {
			continue
		}
		if hasLevel && logLevelOrder[entry.Level] < minLevel {
			continue
		}
		if query != "" && !logEntryMatches(entry, query) {
			continue
		}
		matched = append(matched, entry)
	}

	reset := false
	var out []logEntry
	switch {
	case offset > 0 && offset > total:
		// файл ротирован или усечён — клиент перечитывает хвост
		reset = true
		out = tailLogEntries(matched, limit)
	case offset > 0:
		for _, e := range matched {
			if e.N > offset {
				out = append(out, e)
			}
		}
		if len(out) > limit {
			out = out[len(out)-limit:]
		}
	default:
		out = tailLogEntries(matched, limit)
	}
	if out == nil {
		out = []logEntry{}
	}

	writeLogsJSON(w, map[string]any{
		"available": true,
		"source":    source,
		"path":      path,
		"total":     total,
		"matched":   len(matched),
		"reset":     reset,
		"entries":   out,
	})
}

// logFilePath резолвит файл источника: gateway/errors — из workspace
// (волна 106, канон); launcher — из home (его пишет сам лаунчер, как раньше).
func (h *Handler) logFilePath(source string) (string, error) {
	switch source {
	case "gateway", "errors":
		cfg, err := config.LoadConfig(h.configPath)
		if err != nil {
			return "", err
		}
		name := "gateway.log"
		if source == "errors" {
			name = "errors.log"
		}
		return filepath.Join(cfg.WorkspacePath(), "logs", name), nil
	case "launcher":
		return filepath.Join(globalConfigDir(), "logs", "launcher.log"), nil
	default:
		return "", fmt.Errorf("unknown log source %q (gateway|errors|launcher)", source)
	}
}

func parseLogLine(line string, n int) (logEntry, bool) {
	line = strings.TrimRight(line, "\r")
	if line == "" {
		return logEntry{}, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return logEntry{}, false
	}
	e := logEntry{N: n, Raw: line}
	if v, ok := m["level"].(string); ok {
		e.Level = v
		delete(m, "level")
	}
	if v, ok := m["time"].(string); ok {
		e.Time = v
		delete(m, "time")
	}
	if v, ok := m["component"].(string); ok {
		e.Component = v
		delete(m, "component")
	}
	if v, ok := m["caller"].(string); ok {
		e.Caller = v
		delete(m, "caller")
	}
	if v, ok := m["message"].(string); ok {
		e.Message = v
		delete(m, "message")
	}
	if len(m) > 0 {
		e.Fields = m
	}
	return e, true
}

func logEntryMatches(e logEntry, query string) bool {
	return strings.Contains(strings.ToLower(e.Message), query) ||
		strings.Contains(strings.ToLower(e.Component), query) ||
		strings.Contains(strings.ToLower(e.Caller), query) ||
		strings.Contains(strings.ToLower(e.Raw), query)
}

func parseLevelParam(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "all" {
		return 0, false
	}
	v, ok := logLevelOrder[s]
	return v, ok
}

func tailLogEntries(entries []logEntry, limit int) []logEntry {
	if len(entries) > limit {
		return entries[len(entries)-limit:]
	}
	return entries
}

func writeLogsJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
