package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/routing"
)

// D-AUDIT-97: named agents CRUD for the web UI.
// Agents live in agents.list; Pika pipeline sub-agents (atomizer, reflexor,
// mcp_guard, archivist) are managed by the /subagents page and excluded here.

var agentIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

var pikaPipelineAgentIDs = map[string]bool{
	"atomizer":  true,
	"reflexor":  true,
	"mcp_guard": true,
	"archivist": true,
}

type agentInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Default     bool     `json:"default"`
	Workspace   string   `json:"workspace"`
	Model       string   `json:"model"`
	Skills      []string `json:"skills,omitempty"`
	AllowAgents []string `json:"allow_agents,omitempty"`
	Description string   `json:"description,omitempty"`
	HasAgentMD  bool     `json:"has_agent_md"`
}

type agentRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Workspace   string   `json:"workspace"`
	Model       string   `json:"model"`
	Skills      []string `json:"skills"`
	AllowAgents []string `json:"allow_agents"`
	Default     bool     `json:"default"`
}

// registerAgentRoutes binds named-agent CRUD endpoints to the ServeMux.
func (h *Handler) registerAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agents", h.handleListAgents)
	mux.HandleFunc("POST /api/agents", h.handleCreateAgent)
	mux.HandleFunc("PUT /api/agents/{id}", h.handleUpdateAgent)
	mux.HandleFunc("DELETE /api/agents/{id}", h.handleDeleteAgent)
}

// handleListAgents returns named agents from agents.list.
//
//	GET /api/agents
func (h *Handler) handleListAgents(w http.ResponseWriter, _ *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	agents := make([]agentInfo, 0, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if pikaPipelineAgentIDs[routing.NormalizeAgentID(ac.ID)] {
			continue
		}
		agents = append(agents, buildAgentInfo(cfg, ac))
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"agents":             agents,
		"defaults_workspace": expandHomePath(cfg.Agents.Defaults.Workspace),
	})
}

func buildAgentInfo(cfg *config.Config, ac *config.AgentConfig) agentInfo {
	info := agentInfo{
		ID:        routing.NormalizeAgentID(ac.ID),
		Name:      ac.Name,
		Default:   ac.Default,
		Workspace: agentWorkspacePath(cfg, ac),
	}
	if ac.Model != nil {
		info.Model = ac.Model.Primary
	}
	if len(ac.Skills) > 0 {
		info.Skills = append([]string(nil), ac.Skills...)
	}
	if ac.Subagents != nil && len(ac.Subagents.AllowAgents) > 0 {
		info.AllowAgents = append([]string(nil), ac.Subagents.AllowAgents...)
	}
	if info.Workspace != "" {
		desc, hasMD := readAgentMDDescription(info.Workspace)
		info.Description = desc
		info.HasAgentMD = hasMD
	}
	return info
}

// agentWorkspacePath resolves an agent's workspace the same way the agent
// runtime does: explicit workspace, defaults for main/default, and
// "<defaults workspace>-<id>" for named agents.
func agentWorkspacePath(cfg *config.Config, ac *config.AgentConfig) string {
	defaults := &cfg.Agents.Defaults
	if ac != nil && strings.TrimSpace(ac.Workspace) != "" {
		return expandHomePath(strings.TrimSpace(ac.Workspace))
	}
	if ac == nil || ac.Default || ac.ID == "" ||
		routing.NormalizeAgentID(ac.ID) == "main" {
		return expandHomePath(defaults.Workspace)
	}
	return expandHomePath(defaults.Workspace) + "-" + routing.NormalizeAgentID(ac.ID)
}

// readAgentMDDescription extracts the frontmatter description from AGENT.md.
func readAgentMDDescription(workspace string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(workspace, "AGENT.md"))
	if err != nil {
		return "", false
	}
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return "", true
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", true
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		if strings.HasPrefix(line, "description:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "description:")), true
		}
	}
	return "", true
}

// handleCreateAgent appends a named agent to agents.list and scaffolds its
// workspace with an AGENT.md template.
//
//	POST /api/agents
func (h *Handler) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req struct {
		ID string `json:"id"`
		agentRequest
	}
	if uErr := json.Unmarshal(body, &req); uErr != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", uErr), http.StatusBadRequest)
		return
	}
	id := routing.NormalizeAgentID(strings.TrimSpace(req.ID))
	if !agentIDRe.MatchString(id) {
		http.Error(w, fmt.Sprintf(
			"invalid agent id %q: use lowercase letters, digits and -", req.ID),
			http.StatusBadRequest)
		return
	}
	if pikaPipelineAgentIDs[id] {
		http.Error(w, fmt.Sprintf(
			"agent id %q is reserved for a Pika pipeline", id), http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	for i := range cfg.Agents.List {
		if routing.NormalizeAgentID(cfg.Agents.List[i].ID) == id {
			http.Error(w, fmt.Sprintf("agent %q already exists", id), http.StatusConflict)
			return
		}
	}

	entry := config.AgentConfig{
		ID:        id,
		Name:      strings.TrimSpace(req.Name),
		Default:   req.Default,
		Workspace: strings.TrimSpace(req.Workspace),
	}
	if m := strings.TrimSpace(req.Model); m != "" {
		entry.Model = &config.AgentModelConfig{Primary: m}
	}
	if len(req.Skills) > 0 {
		entry.Skills = append([]string(nil), req.Skills...)
	}
	if len(req.AllowAgents) > 0 {
		entry.Subagents = &config.SubagentsConfig{
			AllowAgents: append([]string(nil), req.AllowAgents...),
		}
	}
	if entry.Default {
		for i := range cfg.Agents.List {
			cfg.Agents.List[i].Default = false
		}
	}
	cfg.Agents.List = append(cfg.Agents.List, entry)

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	workspace := agentWorkspacePath(cfg, &entry)
	scaffolded := false
	if workspace != "" {
		if err := scaffoldAgentWorkspace(workspace, entry.Name, req.Description); err != nil {
			logger.Warnf("agents: scaffold AGENT.md for %q failed: %v", id, err)
		} else {
			scaffolded = true
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"id":         id,
		"workspace":  workspace,
		"scaffolded": scaffolded,
	})
}

// handleUpdateAgent replaces mutable fields of one named agent and upserts
// the AGENT.md frontmatter name/description when provided.
//
//	PUT /api/agents/{id}
func (h *Handler) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := routing.NormalizeAgentID(strings.TrimSpace(r.PathValue("id")))
	if pikaPipelineAgentIDs[id] {
		http.Error(w, fmt.Sprintf(
			"agent %q is managed by the subagents page", id), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req agentRequest
	if uErr := json.Unmarshal(body, &req); uErr != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", uErr), http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	idx := -1
	for i := range cfg.Agents.List {
		if routing.NormalizeAgentID(cfg.Agents.List[i].ID) == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		http.Error(w, fmt.Sprintf("agent %q not found", id), http.StatusNotFound)
		return
	}

	entry := &cfg.Agents.List[idx]
	entry.Name = strings.TrimSpace(req.Name)
	entry.Workspace = strings.TrimSpace(req.Workspace)
	if m := strings.TrimSpace(req.Model); m != "" {
		if entry.Model == nil {
			entry.Model = &config.AgentModelConfig{}
		}
		entry.Model.Primary = m
	} else {
		entry.Model = nil
	}
	entry.Skills = append([]string(nil), req.Skills...)
	if len(req.AllowAgents) > 0 {
		entry.Subagents = &config.SubagentsConfig{
			AllowAgents: append([]string(nil), req.AllowAgents...),
		}
	} else {
		entry.Subagents = nil
	}
	if req.Default && !entry.Default {
		for i := range cfg.Agents.List {
			cfg.Agents.List[i].Default = false
		}
		entry.Default = true
	}

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	workspace := agentWorkspacePath(cfg, entry)
	if workspace != "" {
		if err := upsertAgentMD(workspace, entry.Name, req.Description); err != nil {
			logger.Warnf("agents: AGENT.md upsert for %q failed: %v", id, err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "workspace": workspace})
}

// handleDeleteAgent removes one named agent from agents.list. Workspace files
// are preserved on disk.
//
//	DELETE /api/agents/{id}
func (h *Handler) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := routing.NormalizeAgentID(strings.TrimSpace(r.PathValue("id")))
	if pikaPipelineAgentIDs[id] {
		http.Error(w, fmt.Sprintf(
			"agent %q is managed by the subagents page", id), http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	idx := -1
	for i := range cfg.Agents.List {
		if routing.NormalizeAgentID(cfg.Agents.List[i].ID) == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		http.Error(w, fmt.Sprintf("agent %q not found", id), http.StatusNotFound)
		return
	}
	if cfg.Agents.List[idx].Default {
		http.Error(w, "cannot delete the default agent", http.StatusBadRequest)
		return
	}

	workspace := agentWorkspacePath(cfg, &cfg.Agents.List[idx])
	cfg.Agents.List = append(cfg.Agents.List[:idx], cfg.Agents.List[idx+1:]...)

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":              "ok",
		"workspace_preserved": workspace,
	})
}

// scaffoldAgentWorkspace creates the agent workspace with an AGENT.md
// template when the file does not exist yet.
func scaffoldAgentWorkspace(workspace, name, description string) error {
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		return err
	}
	path := filepath.Join(workspace, "AGENT.md")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if name == "" {
		name = filepath.Base(workspace)
	}
	content := "---\nname: " + name + "\n"
	if d := strings.TrimSpace(description); d != "" {
		content += "description: " + d + "\n"
	}
	content += "---\n\n# " + name + "\n\n" +
		"Describe the agent role: what it does, what it never does, " +
		"and how it should answer.\n"
	return fileutil.WriteFileAtomic(path, []byte(content), 0o600)
}

// upsertAgentMD creates AGENT.md with frontmatter when missing, or updates
// the name/description lines inside the existing frontmatter block.
func upsertAgentMD(workspace, name, description string) error {
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		return err
	}
	path := filepath.Join(workspace, "AGENT.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return scaffoldAgentWorkspace(workspace, name, description)
		}
		return err
	}

	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		fm := "---\n"
		if name != "" {
			fm += "name: " + name + "\n"
		}
		if d := strings.TrimSpace(description); d != "" {
			fm += "description: " + d + "\n"
		}
		fm += "---\n\n"
		return fileutil.WriteFileAtomic(path, []byte(fm+content), 0o600)
	}

	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fmt.Errorf("AGENT.md has an unterminated frontmatter block")
	}
	fm := upsertFrontmatterLine(rest[:end], "name", name)
	fm = upsertFrontmatterLine(fm, "description", strings.TrimSpace(description))
	return fileutil.WriteFileAtomic(path, []byte("---\n"+fm+rest[end:]), 0o600)
}

// upsertFrontmatterLine sets key inside a frontmatter block; an empty value
// leaves the block unchanged.
func upsertFrontmatterLine(frontmatter, key, value string) string {
	if value == "" {
		return frontmatter
	}
	lines := strings.Split(frontmatter, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, key+":") {
			lines[i] = key + ": " + value
			return strings.Join(lines, "\n")
		}
	}
	return frontmatter + "\n" + key + ": " + value
}
