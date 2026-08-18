package api

import (
	"encoding/json"
	"net/http"

	"github.com/sipeed/picoclaw/pkg/commands"
)

type commandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Usage       string `json:"usage,omitempty"`
}

// registerCommandRoutes exposes the canonical slash-command list for the
// chat command palette (D-AUDIT-105). Single source of truth:
// pkg/commands.BuiltinDefinitions + agent-level /memory (D-AUDIT-104,
// intercepted in agent_command.go, hence listed here manually).
func (h *Handler) registerCommandRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/commands", h.handleListCommands)
}

func (h *Handler) handleListCommands(w http.ResponseWriter, r *http.Request) {
	defs := commands.BuiltinDefinitions()
	out := make([]commandInfo, 0, len(defs)+1)
	for _, d := range defs {
		out = append(out, commandInfo{
			Name:        d.Name,
			Description: d.Description,
			Usage:       d.Usage,
		})
	}
	out = append(out, commandInfo{
		Name:        "memory",
		Description: "Per-chat memory search scope",
		Usage:       "/memory [all|session]",
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"commands": out})
}
