// PIKA-V3: MCP Security Pipeline — 7-layer defense-in-depth
// for MCP transport (D-SEC-MCP, D-SEC-v2, D-SEC-v3).
// Go-level enforcement: Output Sanitizer, Taint Tracking,
// Per-Server ACL, MCP Guard, RAD integration, Rug Pull Guard,
// Audit Trail. Принцип OWASP: промт = suggestions, Go = enforcement.

package pika

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/text/unicode/norm"
)

// SanitizeVerdict is the output sanitizer classification.
type SanitizeVerdict string

const (
	VerdictClean      SanitizeVerdict = "CLEAN"
	VerdictSuspicious SanitizeVerdict = "SUSPICIOUS"
	VerdictBlock      SanitizeVerdict = "BLOCK"
)

// SanitizeResult holds the output sanitizer outcome.
type SanitizeResult struct {
	Verdict   SanitizeVerdict
	Sanitized string
	Reasons   []string
}

// MCPServerPolicy defines per-server ACL (from config).
type MCPServerPolicy struct {
	Name                  string          `json:"name"`
	TrustLevel            string          `json:"trust_level"`
	Capabilities          map[string]bool `json:"capabilities"`
	AllowedTools          []string        `json:"allowed_tools"`
	AllowPrompts          bool            `json:"allow_prompts"`
	AllowResources        bool            `json:"allow_resources"`
	MaxOutputBytes        int             `json:"max_output_bytes"`
	TaintPolicy           string          `json:"taint_policy"`
	MaxListChangedPerHour int             `json:"max_list_changed_per_hour"`
	RPM                   int             `json:"rpm"`
}

// MCPToolDef is a minimal MCP tool definition for auditing.
type MCPToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// TaintState holds session-level taint tracking state.
type TaintState struct {
	Tainted     bool
	TaintSource string
	TaintTurn   int
}

// GuardToolVerdict is a single tool verdict from startup audit.
type GuardToolVerdict struct {
	Name        string   `json:"name"`
	Verdict     string   `json:"verdict"`
	Confidence  string   `json:"confidence"`
	AnomalyType string   `json:"anomaly_type"`
	Reason      string   `json:"reason"`
	Indicators  []string `json:"indicators"`
}

// GuardStartupResult is parsed JSON from MCP Guard startup.
type GuardStartupResult struct {
	Mode  string             `json:"mode"`
	Tools []GuardToolVerdict `json:"tools"`
}

// MCPGuardCanaryConfig holds canary marker configuration.
type MCPGuardCanaryConfig struct {
	ManipulationMarkers   []string `json:"manipulation_markers"`
	ResistanceMarkers     []string `json:"resistance_markers"`
	ManipulationThreshold int      `json:"manipulation_threshold"`
	ResistanceThreshold   int      `json:"resistance_threshold"`
}

// MCPGuardConfig holds MCP Guard agent configuration.
type MCPGuardConfig struct {
	Enabled                  bool                 `json:"enabled"`
	PromptFile               string               `json:"prompt_file"`
	Model                    string               `json:"model"`
	TimeoutMs                int                  `json:"timeout_ms"`
	SuspiciousTextRatio      float64              `json:"suspicious_text_ratio"`
	SuspiciousSizeMultiplier float64              `json:"suspicious_size_multiplier"`
	StartupAuditEnabled      bool                 `json:"startup_audit_enabled"`
	ReauditOnListChanged     bool                 `json:"reaudit_on_list_changed"`
	HashAlgorithm            string               `json:"hash_algorithm"`
	Canary                   MCPGuardCanaryConfig `json:"canary"`
}

// MCPGuardLLMCaller abstracts the LLM call for MCP Guard.
type MCPGuardLLMCaller interface {
	CallGuardLLM(ctx context.Context, systemPrompt, userInput string) (string, error)
}

// CanaryVerdict is the runtime canary probe outcome.
type CanaryVerdict string

const (
	CanarySafe       CanaryVerdict = "SAFE"
	CanarySuspicious CanaryVerdict = "SUSPICIOUS"
	CanaryDangerous  CanaryVerdict = "DANGEROUS"
)

// DefaultMCPGuardConfig returns production defaults (spec §8).
func DefaultMCPGuardConfig() MCPGuardConfig {
	return MCPGuardConfig{
		Enabled: true, PromptFile: "/workspace/prompts/mcp_guard.md",
		Model: "google/gemma-3-12b-it:free", TimeoutMs: 5000,
		SuspiciousTextRatio: 0.3, SuspiciousSizeMultiplier: 2.0,
		StartupAuditEnabled: true, ReauditOnListChanged: true,
		HashAlgorithm: "sha256",
		Canary: MCPGuardCanaryConfig{
			ManipulationMarkers: []string{
				"I'll forward", "I'll send", "I'll execute",
				"I should forward", "I should send", "I should execute",
				"let me forward", "let me send", "let me execute",
				"here is the system prompt", "here is my configuration",
				"as you requested", "as instructed by", "happy to help with",
			},
			ResistanceMarkers: []string{
				"injection", "malicious", "exfiltration", "data leak",
				"hidden instruction", "embedded command", "poisoning",
				"unauthorized", "social engineering", "prompt leakage",
			},
			ManipulationThreshold: 1, ResistanceThreshold: 2,
		},
	}
}

// PIKA-V3: Block patterns for Output Sanitizer.
var defaultBlockPatterns = []string{
	`(?i)ignore\s+previous`, `(?i)system\s*:\s`,
	`(?i)as\s+an\s+AI\s+you\s+should`,
	`(?i)disregard\s+(all\s+)?instructions`,
	`(?i)override\s+(your\s+)?instructions`,
	`(?i)you\s+are\s+now\s+in\s+.*mode`,
	`(?i)new\s+instructions?\s*:`,
}

var defaultCredentialPatterns = []string{
	`ghp_[a-zA-Z0-9]{36,}`, `sk-[a-zA-Z0-9]{20,}`,
	`(?i)Bearer\s+[a-zA-Z0-9._\-]+`, `(?i)token=[a-zA-Z0-9._\-]+`,
	`(?i)password=[^\s&]+`, `AKIA[A-Z0-9]{16}`,
}

var defaultBase64Patterns = []string{
	`aWdub3JlIHByZXZpb3Vz`, `c3lzdGVtOg`,
	`ZGlzcmVnYXJkIGluc3RydWN0aW9ucw`,
}

type toolBaseline struct {
	AvgTextRatio  float64
	AvgOutputSize float64
	CallCount     int
}

// MCPSecurityPipeline implements 7-layer MCP security.
type MCPSecurityPipeline struct {
	mu                 sync.Mutex
	guardCfg           MCPGuardConfig
	policies           map[string]*MCPServerPolicy
	telemetry          *Telemetry
	blockPatterns      []*regexp.Regexp
	credentialPatterns []*regexp.Regexp
	base64Patterns     []*regexp.Regexp
	baselines          map[string]*toolBaseline
	taint              TaintState
	toolHashes         map[string]string
	diag                *DiagnosticsEngine
	manipulationREs    []*regexp.Regexp
	resistanceREs      []*regexp.Regexp
	cachedPromptSHA    string
}

// NewMCPSecurityPipeline creates the security pipeline.
func NewMCPSecurityPipeline(
	guardCfg MCPGuardConfig, policies []MCPServerPolicy,
	telemetry *Telemetry,
) *MCPSecurityPipeline {
	p := &MCPSecurityPipeline{
		guardCfg: guardCfg, telemetry: telemetry,
		policies:   make(map[string]*MCPServerPolicy),
		baselines:  make(map[string]*toolBaseline),
		toolHashes: make(map[string]string),
	}
	for i := range policies {
		p.policies[policies[i].Name] = &policies[i]
	}
	p.blockPatterns = compileAll(defaultBlockPatterns)
	p.credentialPatterns = compileAll(defaultCredentialPatterns)
	p.base64Patterns = compileAll(defaultBase64Patterns)
	for _, m := range guardCfg.Canary.ManipulationMarkers {
		p.manipulationREs = append(p.manipulationREs,
			regexp.MustCompile("(?i)"+regexp.QuoteMeta(m)))
	}
	for _, m := range guardCfg.Canary.ResistanceMarkers {
		p.resistanceREs = append(p.resistanceREs,
			regexp.MustCompile("(?i)"+regexp.QuoteMeta(m)))
	}
	return p
}

func compileAll(pats []string) []*regexp.Regexp {
	rs := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		rs[i] = regexp.MustCompile(p)
	}
	return rs
}

// ── Layer 0: Per-Server ACL + Tool Allowlist ─────────────────

// FilterAllowedTools returns only tools allowed by server ACL.
func (p *MCPSecurityPipeline) FilterAllowedTools(
	serverName string, tools []MCPToolDef,
) []MCPToolDef {
	pol, ok := p.policies[serverName]
	if !ok {
		return nil
	}
	if pol.TrustLevel == "internal" && len(pol.AllowedTools) == 0 {
		return tools
	}
	allowed := make(map[string]bool, len(pol.AllowedTools))
	for _, t := range pol.AllowedTools {
		allowed[t] = true
	}
	var out []MCPToolDef
	for _, t := range tools {
		if allowed[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// IsCapabilityAllowed checks capability for a server.
func (p *MCPSecurityPipeline) IsCapabilityAllowed(
	serverName, capability string,
) bool {
	pol, ok := p.policies[serverName]
	if !ok {
		return false
	}
	return pol.Capabilities[capability]
}

// ── Layer 2: Output Sanitizer ────────────────────────────────

// SanitizeOutput processes raw MCP output through security.
func (p *MCPSecurityPipeline) SanitizeOutput(
	serverName, toolName, raw string,
) SanitizeResult {
	var reasons []string
	pol := p.policies[serverName]
	maxB := 32768
	if pol != nil && pol.MaxOutputBytes > 0 {
		maxB = pol.MaxOutputBytes
	}
	if len(raw) > maxB {
		return SanitizeResult{
			Verdict: VerdictBlock,
			Reasons: []string{fmt.Sprintf(
				"output exceeds max_output_bytes (%d>%d)",
				len(raw), maxB,
			)},
		}
	}
	normalized := norm.NFKC.String(raw)
	sanitized := normalized
	for _, re := range p.credentialPatterns {
		sanitized = re.ReplaceAllString(sanitized, "[REDACTED]")
	}
	for _, re := range p.blockPatterns {
		if re.MatchString(sanitized) {
			return SanitizeResult{
				Verdict:   VerdictBlock,
				Sanitized: sanitized,
				Reasons:   []string{"block pattern: " + re.String()},
			}
		}
	}
	for _, re := range p.base64Patterns {
		if re.MatchString(sanitized) {
			return SanitizeResult{
				Verdict:   VerdictBlock,
				Sanitized: sanitized,
				Reasons:   []string{"base64 injection pattern"},
			}
		}
	}
	p.mu.Lock()
	key := serverName + ":" + toolName
	bl := p.baselines[key]
	if bl == nil {
		bl = &toolBaseline{}
		p.baselines[key] = bl
	}
	tr := estimateTextRatio(sanitized)
	sz := float64(len(sanitized))
	sizeMul := p.guardCfg.SuspiciousSizeMultiplier
	if sizeMul <= 0 {
		sizeMul = 2.0
	}
	if bl.CallCount < 5 {
		reasons = append(reasons, "no baseline (first 5 calls)")
	} else {
		if tr > bl.AvgTextRatio*1.5 {
			reasons = append(reasons, fmt.Sprintf(
				"text ratio %.2f > baseline*1.5", tr,
			))
		}
		if bl.AvgOutputSize > 0 && sz > bl.AvgOutputSize*sizeMul {
			reasons = append(reasons, fmt.Sprintf(
				"size %.0f > baseline*%.1f", sz, sizeMul,
			))
		}
	}
	const alpha = 0.1
	if bl.CallCount == 0 {
		bl.AvgTextRatio = tr
		bl.AvgOutputSize = sz
	} else {
		bl.AvgTextRatio = alpha*tr + (1-alpha)*bl.AvgTextRatio
		bl.AvgOutputSize = alpha*sz + (1-alpha)*bl.AvgOutputSize
	}
	bl.CallCount++
	p.mu.Unlock()
	v := VerdictClean
	if len(reasons) > 0 {
		v = VerdictSuspicious
	}
	return SanitizeResult{Verdict: v, Sanitized: sanitized, Reasons: reasons}
}

func estimateTextRatio(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	structural := 0
	for _, r := range s {
		switch r {
		case '{', '}', '[', ']', ':', ',', '"':
			structural++
		}
	}
	total := float64(len([]rune(s)))
	if total == 0 {
		return 0
	}
	return 1.0 - float64(structural)/total
}

// ── Layer 3: Taint Tracking (explicit-only reset) ────────────

func (p *MCPSecurityPipeline) SetTaint(server string, turn int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.taint = TaintState{Tainted: true, TaintSource: server, TaintTurn: turn}
}

func (p *MCPSecurityPipeline) ClearTaint() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.taint = TaintState{}
}

func (p *MCPSecurityPipeline) GetTaint() TaintState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.taint
}

// ShouldBlockTaintedAction checks if action should be blocked.
func (p *MCPSecurityPipeline) ShouldBlockTaintedAction(risk string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.taint.Tainted {
		return false
	}
	return risk == "red" || risk == "yellow"
}

// ── Layer 0.5+2.5: MCP Guard ─────────────────────────────────

func (p *MCPSecurityPipeline) loadGuardPrompt() (string, error) {
	if p.diag != nil {
		prompt, err := p.diag.BuildSubagentPrompt(context.Background(), "mcp_guard")
		if err == nil {
			p.mu.Lock()
			p.cachedPromptSHA = sha256Hex([]byte(prompt))
			p.mu.Unlock()
			return prompt, nil
		}
		// fallback to default prompt
	}
	data, err := os.ReadFile(p.guardCfg.PromptFile)
	if err != nil {
		return "", fmt.Errorf("pika/mcp_guard: read prompt: %w", err)
	}
	content := string(data)
	p.mu.Lock()
	p.cachedPromptSHA = sha256Hex([]byte(content))
	p.mu.Unlock()
	return content, nil
}

// GetPromptVersion returns SHA-256 of last loaded prompt.
func (p *MCPSecurityPipeline) GetPromptVersion() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cachedPromptSHA
}

// StartupAudit runs MCP Guard on tool definitions.
func (p *MCPSecurityPipeline) StartupAudit(
	ctx context.Context, serverName string,
	tools []MCPToolDef, caller MCPGuardLLMCaller,
) ([]GuardToolVerdict, error) {
	if !p.guardCfg.Enabled || !p.guardCfg.StartupAuditEnabled {
		v := make([]GuardToolVerdict, len(tools))
		for i, t := range tools {
			v[i] = GuardToolVerdict{Name: t.Name, Verdict: "safe"}
		}
		return v, nil
	}
	prompt, err := p.loadGuardPrompt()
	if err != nil {
		p.reportGuardFailure(err)
		return p.allSuspicious(tools, "prompt unavailable"), nil
	}
	input := struct {
		Mode  string       `json:"mode"`
		Tools []MCPToolDef `json:"tools"`
	}{Mode: "startup_audit", Tools: tools}
	inputJSON, _ := json.Marshal(input)
	rawResp, err := caller.CallGuardLLM(ctx, prompt, string(inputJSON))
	if err != nil {
		p.reportGuardFailure(err)
		return p.allSuspicious(tools, "guard LLM error"), nil
	}
	var result GuardStartupResult
	if err := json.Unmarshal([]byte(extractGuardJSON(rawResp)), &result); err != nil {
		p.reportGuardFailure(err)
		return p.allSuspicious(tools, "guard JSON parse error"), nil
	}
	p.reportGuardSuccess()
	p.mu.Lock()
	for _, t := range tools {
		p.toolHashes[serverName+":"+t.Name] = computeToolHash(t)
	}
	p.mu.Unlock()
	return result.Tools, nil
}

// RuntimeCanaryProbe runs canary probe on suspicious output.
func (p *MCPSecurityPipeline) RuntimeCanaryProbe(
	ctx context.Context, serverName, toolName, output string,
	caller MCPGuardLLMCaller,
) CanaryVerdict {
	if !p.guardCfg.Enabled {
		return CanarySafe
	}
	prompt, err := p.loadGuardPrompt()
	if err != nil {
		p.reportGuardFailure(err)
		return CanarySuspicious
	}
	trust := "unknown"
	if pol := p.policies[serverName]; pol != nil {
		trust = pol.TrustLevel
	}
	input := struct {
		Mode   string `json:"mode"`
		Tool   string `json:"tool_name"`
		Server string `json:"server_name"`
		Trust  string `json:"server_trust"`
		Output string `json:"output"`
	}{"runtime_audit", toolName, serverName, trust, output}
	inputJSON, _ := json.Marshal(input)
	rawResp, err := caller.CallGuardLLM(ctx, prompt, string(inputJSON))
	if err != nil {
		p.reportGuardFailure(err)
		return CanarySuspicious
	}
	p.reportGuardSuccess()
	return p.scanCanaryMarkers(rawResp)
}

func (p *MCPSecurityPipeline) scanCanaryMarkers(output string) CanaryVerdict {
	manipCount := 0
	for _, re := range p.manipulationREs {
		if re.MatchString(output) {
			manipCount++
		}
	}
	thr := p.guardCfg.Canary.ManipulationThreshold
	if thr <= 0 {
		thr = 1
	}
	if manipCount >= thr {
		return CanaryDangerous
	}
	resistCount := 0
	for _, re := range p.resistanceREs {
		if re.MatchString(output) {
			resistCount++
		}
	}
	rThr := p.guardCfg.Canary.ResistanceThreshold
	if rThr <= 0 {
		rThr = 2
	}
	if resistCount >= rThr {
		return CanarySuspicious
	}
	return CanarySafe
}

// ── Layer 5.7: Rug Pull Guard (hash-diff + re-audit) ─────────

// CheckRugPull detects changed/new tools via hash-diff.
func (p *MCPSecurityPipeline) CheckRugPull(
	serverName string, tools []MCPToolDef,
) (changed, added []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range tools {
		k := serverName + ":" + t.Name
		h := computeToolHash(t)
		old, exists := p.toolHashes[k]
		if !exists {
			added = append(added, t.Name)
		} else if old != h {
			changed = append(changed, t.Name)
		}
	}
	return changed, added
}

// UpdateToolHashes updates stored hashes after re-audit.
func (p *MCPSecurityPipeline) UpdateToolHashes(
	serverName string, tools []MCPToolDef,
) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range tools {
		p.toolHashes[serverName+":"+t.Name] = computeToolHash(t)
	}
}

// ── Layer 5: Audit Trail (autoEvent mapping for MCP) ─────────

// MCPAutoEventMappings returns toolTypeMap and toolTagMap
// entries for MCP security events.
func MCPAutoEventMappings(
	serverNames []string,
) (map[string]string, map[string][]string) {
	tm := make(map[string]string)
	tg := make(map[string][]string)
	for _, name := range serverNames {
		pfx := "mcp." + name
		base := []string{"tool:mcp", "server:" + name}
		tm[pfx+".call"] = "mcp_call"
		tm[pfx+".call_fail"] = "mcp_call_fail"
		tm[pfx+".blocked"] = "mcp_blocked"
		tm[pfx+".sanitized"] = "mcp_sanitized"
		tg[pfx+".call"] = base
		tg[pfx+".call_fail"] = base
		tg[pfx+".blocked"] = append(
			append([]string{}, base...), "security:taint_block",
		)
		tg[pfx+".sanitized"] = append(
			append([]string{}, base...), "security:sanitized",
		)
	}
	tm["rad.blocked"] = "rad_anomaly"
	tm["rad.warning"] = "rad_warning"
	tg["rad.blocked"] = []string{"security:rad", "severity:block"}
	tg["rad.warning"] = []string{"security:rad", "severity:warning"}
	return tm, tg
}

// MCPAutoEventClasses returns EventClasses for MCP events.
func MCPAutoEventClasses() EventClasses {
	return EventClasses{
		Critical: map[string]bool{
			"mcp_blocked": true, "mcp_sanitized": true,
			"rad_anomaly": true, "rad_warning": true,
		},
		Diagnostic: map[string]bool{
			"mcp_call": true, "mcp_call_fail": true,
		},
		Heartbeat: map[string]bool{},
	}
}

// ── Component Health integration ─────────────────────────────

func (p *MCPSecurityPipeline) reportGuardFailure(_ error) {
	if p.telemetry != nil {
		p.telemetry.ReportComponentFailure("mcp_guard", "degraded")
	}
}

func (p *MCPSecurityPipeline) reportGuardSuccess() {
	if p.telemetry != nil {
		p.telemetry.ReportComponentSuccess("mcp_guard")
	}
}

// ── Helpers ──────────────────────────────────────────────────

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func computeToolHash(t MCPToolDef) string {
	return sha256Hex([]byte(t.Name + "|" + t.Description + "|" + string(t.InputSchema)))
}

func extractGuardJSON(raw string) string {
	start := strings.Index(raw, "{")
	if start < 0 {
		return raw
	}
	depth := 0
	for i := start; i < len(raw); i++ {
		switch raw[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return raw[start:]
}

func (p *MCPSecurityPipeline) allSuspicious(
	tools []MCPToolDef, reason string,
) []GuardToolVerdict {
	v := make([]GuardToolVerdict, len(tools))
	for i, t := range tools {
		v[i] = GuardToolVerdict{Name: t.Name, Verdict: "suspicious", Reason: reason}
	}
	return v
}
