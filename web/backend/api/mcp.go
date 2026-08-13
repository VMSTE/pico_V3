package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	picomcp "github.com/sipeed/picoclaw/pkg/mcp"
)

// D-AUDIT-84: MCP servers management for the web UI.
// Secrets never leave the backend: env/headers are returned as key names only.

var mcpServerNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type mcpServerInfo struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Target   string            `json:"target"`
	Enabled  bool              `json:"enabled"`
	Deferred *bool             `json:"deferred,omitempty"`
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	URL      string            `json:"url,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	EnvFile  string            `json:"env_file,omitempty"`
}

type mcpServerRequest struct {
	Enabled  bool              `json:"enabled"`
	Deferred *bool             `json:"deferred"`
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	EnvFile  string            `json:"env_file"`
	Type     string            `json:"type"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
}

// registerMCPRoutes binds MCP server management endpoints to the ServeMux.
func (h *Handler) registerMCPRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mcp/servers", h.handleListMCPServers)
	mux.HandleFunc("PUT /api/mcp/servers/{name}", h.handlePutMCPServer)
	mux.HandleFunc("PATCH /api/mcp/servers/{name}", h.handlePatchMCPServer)
	mux.HandleFunc("DELETE /api/mcp/servers/{name}", h.handleDeleteMCPServer)
	mux.HandleFunc("POST /api/mcp/servers/{name}/test", h.handleTestMCPServer)
}

// handleListMCPServers returns configured MCP servers (secrets masked to key names).
//
//	GET /api/mcp/servers
func (h *Handler) handleListMCPServers(w http.ResponseWriter, _ *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	servers := make([]mcpServerInfo, 0, len(cfg.Tools.MCP.Servers))
	for name, srv := range cfg.Tools.MCP.Servers {
		servers = append(servers, mcpServerInfoFromConfig(name, srv))
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"enabled": cfg.Tools.MCP.Enabled,
		"servers": servers,
	})
}

// handlePutMCPServer creates or replaces one server entry. Enables tools.mcp
// (same behavior as `picoclaw mcp add`).
//
//	PUT /api/mcp/servers/{name}
func (h *Handler) handlePutMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req mcpServerRequest
	if uErr := json.Unmarshal(body, &req); uErr != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", uErr), http.StatusBadRequest)
		return
	}
	if vErr := validateMCPServerRequest(name, &req); vErr != nil {
		http.Error(w, vErr.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	if cfg.Tools.MCP.Servers == nil {
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{}
	}
	oldSrv, hadOld := cfg.Tools.MCP.Servers[name]
	newSrv := config.MCPServerConfig{
		Enabled:  req.Enabled,
		Deferred: req.Deferred,
		Command:  strings.TrimSpace(req.Command),
		Args:     req.Args,
		Env:      req.Env,
		EnvFile:  req.EnvFile,
		Type:     req.Type,
		URL:      strings.TrimSpace(req.URL),
		Headers:  req.Headers,
	}
	if hadOld {
		// D-AUDIT-88: клиент мог вернуть маску — восстановить реальные значения.
		restoreMaskedServerValues(&newSrv, oldSrv)
	}
	cfg.Tools.MCP.Servers[name] = newSrv
	cfg.Tools.MCP.Enabled = true

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleDeleteMCPServer removes one server entry. Removing the last server
// disables tools.mcp (same behavior as `picoclaw mcp remove`).
//
//	DELETE /api/mcp/servers/{name}
func (h *Handler) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	if _, ok := cfg.Tools.MCP.Servers[name]; !ok {
		http.Error(w, fmt.Sprintf("MCP server %q not found", name), http.StatusNotFound)
		return
	}

	delete(cfg.Tools.MCP.Servers, name)
	if len(cfg.Tools.MCP.Servers) == 0 {
		cfg.Tools.MCP.Enabled = false
	}

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleTestMCPServer probes one configured server (connect + count tools).
//
//	POST /api/mcp/servers/{name}/test
func (h *Handler) handleTestMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	srv, ok := cfg.Tools.MCP.Servers[name]
	if !ok {
		http.Error(w, fmt.Sprintf("MCP server %q not found", name), http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	res, err := picomcp.ProbeServer(ctx, name, srv, cfg.WorkspacePath())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"tool_count": res.ToolCount,
	})
}

func mcpServerInfoFromConfig(name string, srv config.MCPServerConfig) mcpServerInfo {
	info := mcpServerInfo{
		Name:     name,
		Type:     inferMCPTransport(srv),
		Target:   mcpServerTarget(srv),
		Enabled:  srv.Enabled,
		Deferred: srv.Deferred,
		Command:  srv.Command,
		Args:     srv.Args,
		URL:      srv.URL,
		EnvFile:  srv.EnvFile,
	}
	if len(srv.Env) > 0 {
		info.Env = make(map[string]string, len(srv.Env))
		for k, v := range srv.Env {
			if v != "" {
				info.Env[k] = mcpMaskedSecret
			} else {
				info.Env[k] = ""
			}
		}
	}
	if len(srv.Headers) > 0 {
		info.Headers = make(map[string]string, len(srv.Headers))
		for k, v := range srv.Headers {
			if v != "" {
				info.Headers[k] = mcpMaskedSecret
			} else {
				info.Headers[k] = ""
			}
		}
	}
	return info
}

func inferMCPTransport(srv config.MCPServerConfig) string {
	switch srv.Type {
	case "stdio", "http", "sse":
		return srv.Type
	}
	if srv.URL != "" {
		return "sse"
	}
	if srv.Command != "" {
		return "stdio"
	}
	return "unknown"
}

func mcpServerTarget(srv config.MCPServerConfig) string {
	t := inferMCPTransport(srv)
	if t == "http" || t == "sse" {
		if srv.URL == "" {
			return "<missing url>"
		}
		return srv.URL
	}
	parts := append([]string{srv.Command}, srv.Args...)
	rendered := strings.TrimSpace(strings.Join(parts, " "))
	if rendered == "" {
		return "<missing command>"
	}
	return rendered
}

func validateMCPServerRequest(name string, req *mcpServerRequest) error {
	if !mcpServerNameRe.MatchString(name) {
		return fmt.Errorf("invalid server name %q: use letters, digits, _ and -", name)
	}
	switch req.Type {
	case "", "stdio", "http", "sse":
	default:
		return fmt.Errorf("invalid type %q: must be stdio, http or sse", req.Type)
	}
	if strings.TrimSpace(req.Command) == "" && strings.TrimSpace(req.URL) == "" {
		return fmt.Errorf("either command (stdio) or url (http/sse) is required")
	}
	return nil
}

// D-AUDIT-87: MCP env/headers are plain maps (not SecureString), so without
// masking their values would be fully visible in GET /api/config.

const mcpMaskedSecret = "[NOT_HERE]"

// maskMCPServerSecrets replaces non-empty MCP env/header values with the
// [NOT_HERE] placeholder, consistent with SecureString masking.
func maskMCPServerSecrets(cfg *config.Config) {
	for name, srv := range cfg.Tools.MCP.Servers {
		for k, v := range srv.Env {
			if v != "" {
				srv.Env[k] = mcpMaskedSecret
			}
		}
		for k, v := range srv.Headers {
			if v != "" {
				srv.Headers[k] = mcpMaskedSecret
			}
		}
		cfg.Tools.MCP.Servers[name] = srv
	}
}

// restoreMCPServerSecrets puts back real MCP env/header values where the
// client echoed the [NOT_HERE] mask. Without this, a full-config save from
// the UI would overwrite real secrets with the placeholder.
func restoreMCPServerSecrets(cfg *config.Config, old *config.Config) {
	for name, srv := range cfg.Tools.MCP.Servers {
		oldSrv, ok := old.Tools.MCP.Servers[name]
		if !ok {
			continue
		}
		for k, v := range srv.Env {
			if v == mcpMaskedSecret {
				if oldV, exists := oldSrv.Env[k]; exists {
					srv.Env[k] = oldV
				}
			}
		}
		for k, v := range srv.Headers {
			if v == mcpMaskedSecret {
				if oldV, exists := oldSrv.Headers[k]; exists {
					srv.Headers[k] = oldV
				}
			}
		}
		cfg.Tools.MCP.Servers[name] = srv
	}
}

type mcpServerPatchRequest struct {
	Enabled  *bool `json:"enabled"`
	Deferred *bool `json:"deferred"`
}

// handlePatchMCPServer partially updates one server entry. Only enabled and
// deferred are patchable here — secrets never pass through this endpoint.
//
//	PATCH /api/mcp/servers/{name}
func (h *Handler) handlePatchMCPServer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req mcpServerPatchRequest
	if uErr := json.Unmarshal(body, &req); uErr != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", uErr), http.StatusBadRequest)
		return
	}
	if req.Enabled == nil && req.Deferred == nil {
		http.Error(w, "nothing to update: send enabled and/or deferred", http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	srv, ok := cfg.Tools.MCP.Servers[name]
	if !ok {
		http.Error(w, fmt.Sprintf("MCP server %q not found", name), http.StatusNotFound)
		return
	}

	if req.Enabled != nil {
		srv.Enabled = *req.Enabled
	}
	if req.Deferred != nil {
		srv.Deferred = req.Deferred
	}
	cfg.Tools.MCP.Servers[name] = srv

	if sErr := config.SaveConfig(h.configPath, cfg); sErr != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", sErr), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// restoreMaskedServerValues copies real values from oldSrv where the client
// echoed the [NOT_HERE] mask (D-AUDIT-88).
func restoreMaskedServerValues(newSrv *config.MCPServerConfig, oldSrv config.MCPServerConfig) {
	for k, v := range newSrv.Env {
		if v == mcpMaskedSecret {
			if oldV, exists := oldSrv.Env[k]; exists {
				newSrv.Env[k] = oldV
			}
		}
	}
	for k, v := range newSrv.Headers {
		if v == mcpMaskedSecret {
			if oldV, exists := oldSrv.Headers[k]; exists {
				newSrv.Headers[k] = oldV
			}
		}
	}
}
