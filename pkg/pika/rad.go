// PIKA-V3: RAD — Reasoning Anomaly Detector (D-SEC-v2, Layer 6).
// Fast pre-action security gate on reasoning tokens.
// Go regex + structural analysis, 0 LLM, sync.
// Stands between model reasoning and tool execution.
//
// Three detectors:
//   1. Pattern Detector — regex match on injection-indicating phrases
//   2. Drift Detector — Jaccard keyword overlap after MCP call
//   3. Escalation Detector — red-risk action after MCP output
//
// Scoring: pattern=3, drift=2, escalation=2.
//   score >= block_score (3) → ANOMALY
//   score >= warn_score  (2) → WARNING
//   score <  warn_score      → SAFE

package pika

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// RADVerdict is the outcome of RAD analysis.
type RADVerdict string

const (
	RADSafe    RADVerdict = "safe"
	RADWarning RADVerdict = "warning"
	RADAnomaly RADVerdict = "anomaly"
)

// RADResult holds the RAD analysis outcome.
type RADResult struct {
	Verdict   RADVerdict
	Score     int
	Detectors []string // which detectors fired
	Reason    string   // brief description for audit trail
}

// RADConfig holds configuration for the RAD subsystem.
// Populated from security.rad in config.json.
type RADConfig struct {
	Enabled           bool
	PatternKeywordsRU []string
	PatternKeywordsEN []string
	DriftThreshold    float64 // default 0.2
	BlockScore        int     // default 3
	WarnScore         int     // default 2
}

// DefaultRADConfig returns RADConfig with production defaults.
func DefaultRADConfig() RADConfig {
	return RADConfig{
		Enabled: true,
		PatternKeywordsRU: []string{
			"tool сказал", "tool вернул", "tool просит",
			"инструкция из", "output содержит команду",
			"вывод указывает", "нужно отправить на",
			"следуя указаниям",
		},
		PatternKeywordsEN: []string{
			"tool said", "tool returned", "tool asks",
			"as instructed by", "output contains command",
			"need to send", "should execute",
			"following instructions from",
		},
		DriftThreshold: 0.2,
		BlockScore:     3,
		WarnScore:      2,
	}
}

// RADSession is a minimal view of session state needed by RAD.
// Decoupled from SessionLifecycle to avoid tight coupling.
type RADSession struct {
	LastToolSource string   // "mcp", "brain", "base", "skill", ""
	PrevKeywords   []string // reasoning keywords from previous turn
}

// RADToolCall is a minimal view of a pending tool call for RAD.
type RADToolCall struct {
	Name      string
	RiskLevel string // "red", "yellow", "green"
}

// RAD is the Reasoning Anomaly Detector.
// Compiled regex patterns are created once at construction.
type RAD struct {
	enabled        bool
	patterns       []*regexp.Regexp
	patternSources []string // original keyword for each pattern
	driftThreshold float64
	blockScore     int
	warnScore      int
}

// NewRAD creates a RAD from config. Compiles regex at creation.
// Panics on invalid regex in keywords (fail fast).
func NewRAD(cfg RADConfig) *RAD {
	r := &RAD{
		enabled:        cfg.Enabled,
		driftThreshold: cfg.DriftThreshold,
		blockScore:     cfg.BlockScore,
		warnScore:      cfg.WarnScore,
	}
	if r.driftThreshold <= 0 {
		r.driftThreshold = 0.2
	}
	if r.blockScore <= 0 {
		r.blockScore = 3
	}
	if r.warnScore <= 0 {
		r.warnScore = 2
	}

	// Compile all keywords as case-insensitive regex.
	allKeywords := make(
		[]string, 0,
		len(cfg.PatternKeywordsRU)+len(cfg.PatternKeywordsEN),
	)
	allKeywords = append(allKeywords, cfg.PatternKeywordsRU...)
	allKeywords = append(allKeywords, cfg.PatternKeywordsEN...)

	for _, kw := range allKeywords {
		pattern := fmt.Sprintf(
			"(?i)%s", regexp.QuoteMeta(kw),
		)
		re, err := regexp.Compile(pattern)
		if err != nil {
			panic(fmt.Sprintf(
				"pika/rad: invalid pattern keyword %q: %v",
				kw, err,
			))
		}
		r.patterns = append(r.patterns, re)
		r.patternSources = append(r.patternSources, kw)
	}
	return r
}

// Analyze is the main entry point. Synchronous call from
// main loop between reasoning tokens and tool execution.
//
//	reasoning   = text of reasoning tokens for current turn
//	session     = current session state (last_tool_source, prev keywords)
//	pendingCall = tool_call the model wants to execute (may be nil)
func (r *RAD) Analyze(
	_ context.Context,
	reasoning string,
	session *RADSession,
	pendingCall *RADToolCall,
) RADResult {
	if !r.enabled {
		return RADResult{Verdict: RADSafe}
	}

	score := 0
	var detectors []string
	var reasons []string

	// 1. Pattern Detector (+3)
	if matched, keywords := r.patternDetect(reasoning); matched {
		score += 3
		detectors = append(detectors, "pattern")
		reasons = append(reasons, fmt.Sprintf(
			"pattern match: %v", keywords,
		))
	}

	// 2. Drift Detector (+2)
	if session != nil {
		currKeywords := extractKeywords(reasoning)
		if r.driftDetect(
			session.LastToolSource,
			session.PrevKeywords,
			currKeywords,
		) {
			score += 2
			detectors = append(detectors, "drift")
			reasons = append(reasons,
				"topic drift after MCP call")
		}
	}

	// 3. Escalation Detector (+2)
	if r.escalationDetect(session, pendingCall) {
		score += 2
		detectors = append(detectors, "escalation")
		reasons = append(reasons,
			"red-risk action after MCP output")
	}

	// Scoring → verdict
	result := RADResult{
		Score:     score,
		Detectors: detectors,
		Reason:    strings.Join(reasons, "; "),
	}

	switch {
	case score >= r.blockScore:
		result.Verdict = RADAnomaly
	case score >= r.warnScore:
		result.Verdict = RADWarning
	default:
		result.Verdict = RADSafe
	}

	return result
}

// patternDetect scans reasoning for injection-indicating
// patterns. Returns (true, matched keywords) on hit.
func (r *RAD) patternDetect(
	reasoning string,
) (bool, []string) {
	if reasoning == "" {
		return false, nil
	}
	var matched []string
	for i, re := range r.patterns {
		if re.MatchString(reasoning) {
			matched = append(matched, r.patternSources[i])
		}
	}
	return len(matched) > 0, matched
}

// driftDetect checks for gross topic shift after MCP tool_result.
// Uses Jaccard index: overlap < threshold → drift detected.
// Skips if previous tool was not MCP.
func (r *RAD) driftDetect(
	lastToolSource string,
	prevKeywords, currKeywords []string,
) bool {
	// Only trigger after MCP calls
	if lastToolSource != "mcp" {
		return false
	}
	if len(prevKeywords) == 0 || len(currKeywords) == 0 {
		return false
	}
	overlap := jaccardIndex(prevKeywords, currKeywords)
	return overlap < r.driftThreshold
}

// escalationDetect checks if model plans a red-risk action
// right after MCP output.
func (r *RAD) escalationDetect(
	session *RADSession,
	pendingCall *RADToolCall,
) bool {
	if session == nil || pendingCall == nil {
		return false
	}
	if session.LastToolSource != "mcp" {
		return false
	}
	return pendingCall.RiskLevel == "red"
}

// jaccardIndex computes the Jaccard similarity coefficient
// between two keyword sets: |A ∩ B| / |A ∪ B|.
func jaccardIndex(a, b []string) float64 {
	setA := make(map[string]struct{}, len(a))
	for _, w := range a {
		setA[strings.ToLower(w)] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, w := range b {
		setB[strings.ToLower(w)] = struct{}{}
	}

	intersection := 0
	for w := range setA {
		if _, ok := setB[w]; ok {
			intersection++
		}
	}

	unionSize := len(setA)
	for w := range setB {
		if _, ok := setA[w]; !ok {
			unionSize++
		}
	}

	if unionSize == 0 {
		return 1.0 // both empty → no drift
	}
	return float64(intersection) / float64(unionSize)
}

// extractKeywords splits reasoning text into lowercase keyword
// tokens. Simple Unicode-aware split on non-letter/digit chars.
// Used for drift detection.
func extractKeywords(text string) []string {
	words := strings.FieldsFunc(
		text, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		},
	)
	seen := make(map[string]struct{}, len(words))
	var result []string
	for _, w := range words {
		lower := strings.ToLower(w)
		if len(lower) < 2 {
			continue // skip single-char tokens
		}
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		result = append(result, lower)
	}
	return result
}
