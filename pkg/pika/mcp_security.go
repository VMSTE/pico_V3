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

// ──── Types ──────────────────────────────────────────────────

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

// GuardStartupResult is the parsed JSON from MCP Guard startup.
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
// Implemented by provider layer. 0 tools.
type MCPGuardLLMCaller interface {
	CallGuardLLM(
		ctx context.Context, systemPrompt, userInput string,
	) (string, error)
}

// CanaryVerdict is the runtime canary probe outcome.
type CanaryVerdict string

const (
	CanarySafe       CanaryVerdict = "SAFE"
	CanarySuspicious CanaryVerdict = "SUSPICIOUS"
	CanaryDangerous  CanaryVerdict = "DANGEROUS"
)

// ──── Default Config ─────────────────────────────────────────

// DefaultMCPGuardConfig returns MCPGuardConfig with production
// defaults from spec §8.
func DefaultMCPGuardConfig() MCPGuardConfig {
	return MCPGuardConfig{
		Enabled:                  true,
		PromptFile:               "/workspace/prompts/mcp_guard.md",
		Model:                    "google/gemma-3-12b-it:free",
		TimeoutMs:                5000,
		SuspiciousTextRatio:      0.3,
		SuspiciousSizeMultiplier: 2.0,
		StartupAuditEnabled:      true,
		ReauditOnListChanged:     true,
		HashAlgorithm:            "sha256",
		Canary: MCPGuardCanaryConfig{
			ManipulationMarkers: []string{
				"I'll forward", "I'll send", "I'll execute",
				"I should forward", "I should send",
				"I should execute", "let me forward",
				"let me send", "let me execute",
				"here is the system prompt",
				"here is my configuration",
				"as you requested", "as instructed by",
				"happy to help with",
			},
			ResistanceMarkers: []string{
				"injection", "malicious", "exfiltration",
				"data leak", "hidden instruction",
				"embedded command", "poisoning",
				"unauthorized", "social engineering",
				"prompt leakage",
			},
			ManipulationThreshold: 1,
			ResistanceThreshold:   2,
		},
	}
}

// ──── Compiled Patterns ──────────────────────────────────────

// PIKA-V3: Block patterns for Output Sanitizer.
var defaultBlockPatterns = []string{
	`(?i)ignore\s+previous`,
	`(?i)system\s*:\s`,
	`(?i)as\s+an\s+AI\s+you\s+should`,
	`(?i)disregard\s+(all\s+)?instructions`,
	`(?i)override\s+(your\s+)?instructions`,
	`(?i)you\s+are\s+now\s+in\s+.*mode`,
	`(?i)new\s+instructions?\s*:`,
}

// PIKA-V3: Credential patterns for stripping.
var defaultCredentialPatterns = []string{
	`ghp_[a-zA-Z0-9]{36,}`,
	`sk-[a-zA-Z0-9]{20,}`,
	`(?i)Bearer\s+[a-zA-Z0-9._\-]+`,
	`(?i)token=[a-zA-Z0-9._\-]+`,
	`(?i)password=[^\s&]+`,
	`AKIA[A-Z0-9]{16}`,
}

// PIKA-V3: Base64-encoded injection patterns (D-SEC-v2).
var defaultBase64Patterns = []string{
	`aWdub3JlIHByZXZpb3Vz`,           // "ignore previous"
	`c3lzdGVtOg`,                       // "system:"
	`ZGlzcmVnYXJkIGluc3RydWN0aW9ucw`, // "disregard instructions"
}

// ──── toolBaseline (per-tool adaptive EMA, D-SEC-v2) ─────────

type toolBaseline struct {
	AvgTextRatio  float64
	AvgOutputSize float64
	CallCount     int
}

// ──── MCPSecurityPipeline ────────────────────────────────────

// MCPSecurityPipeline implements the 7-layer MCP security
// defense-in-depth (D-SEC-MCP, D-SEC-v2, D-SEC-v3).
type MCPSecurityPipeline struct {
	mu        sync.Mutex
	guardCfg  MCPGuardConfig
	policies  map[string]*MCPServerPolicy
	telemetry *Telemetry

	blockPatterns      []*regexp.Regexp
	credentialPatterns []*regexp.Regexp
	base64Patterns     []*regexp.Regexp
	baselines          map[string]*toolBaseline

	taint TaintState

	toolHashes map[string]string // "server:tool" → SHA-256

	manipulationREs []*regexp.Regexp
	resistanceREs   []*regexp.Regexp

	cachedPromptSHA string
}

// NewMCPSecurityPipeline creates the security pipeline.
func NewMCPSecurityPipeline(
	guardCfg MCPGuardConfig,
	policies []MCPServerPolicy,
	telemetry *Telemetry,
) *MCPSecurityPipeline {
	p := &MCPSecurityPipeline{
		guardCfg:   guardCfg,
		policies:   make(map[string]*MCPServerPolicy),
		telemetry:  telemetry,
		baselines:  make(map[string]*toolBaseline),
		toolHashes: make(map[string]string),
	}
	for i := range policies {
		p.policies[policies[i].Name] = &policies[i]
	}
	p.blockPatterns = compilePatterns(defaultBlockPatterns)
	p.credentialPatterns = compilePatterns(
		defaultCredentialPatterns)
	p.base64Patterns =