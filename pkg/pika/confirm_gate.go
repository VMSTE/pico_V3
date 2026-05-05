// PIKA-V3: confirm_gate.go — ConfirmGate builtin hook (ToolApprover, D-136a).
// Matches tool calls against security.dangerous_ops config,
// requests confirmation via Telegram, handles timeout/deny/approve.
// Fail-closed: timeout → deny, error → deny.
//
// NOTE: pkg/pika cannot import pkg/agent (import cycle via
// context_pika.go). Approval types are defined locally here.
// Wiring adapter in instance.go (ТЗ-4a) converts between
// these types and agent.ToolApprover / agent.ToolApprovalRequest /
// agent.ApprovalDecision.
//
// TelegramSender interface is defined in progress.go (shared across
// progress, clarify, confirm_gate). This file uses it without
// re-declaring.

package pika

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// ConfirmApprovalRequest is a local mirror of agent.ToolApprovalRequest.
// pkg/pika cannot import pkg/agent (import cycle via context_pika.go).
// Wiring adapter in instance.go (ТЗ-4a) converts between these
// and agent.ToolApprover / agent.ToolApprovalRequest / agent.ApprovalDecision.
type ConfirmApprovalRequest struct {
	Tool      string
	Arguments map[string]any
}

// ConfirmApprovalDecision is a local mirror of agent.ApprovalDecision.
type ConfirmApprovalDecision struct {
	Approved bool
	Reason   string
}

// ConfirmGate is a builtin ToolApprover hook (D-136a).
// Upon matching a tool call against the dangerous_ops table,
// it requests human confirmation via Telegram before allowing execution.
//
// Decision flow:
//  1. Build opKey = Tool + "." + operation from Arguments
//  2. Lookup in ops map; miss → allow
//  3. Reflex: compose.restart + exited → allow (container recovery)
//  4. Evaluate confirm rule: always / if_healthy / if_critical_path / never
//  5. If confirmation needed: send to Telegram, wait for reply
//  6. Timeout or error → deny (fail-closed)
type ConfirmGate struct {
	ops           map[string]config.DangerousOpEntry
	criticalPaths []string
	timeoutMin    int
	sender        TelegramSender
	healthGetter  SystemStateProvider
}

// ConfirmGateFactory creates a new ConfirmGate from config.
// Config path: hooks.builtins.confirm_gate.enabled: true.
// Registration in instance.go (ТЗ-4a):
//
//	hookManager.Mount(agent.NamedHook("pika.confirm_gate", adapter))
//	hookManager.ConfigureTimeouts(0, 0, time.Duration(timeoutMin)*time.Minute)
func ConfirmGateFactory(
	cfg *config.Config,
	sender TelegramSender,
	health SystemStateProvider,
) *ConfirmGate {
	ops := make(map[string]config.DangerousOpEntry, len(cfg.Security.DangerousOps.Ops))
	for key, entry := range cfg.Security.DangerousOps.Ops {
		ops[key] = entry
	}

	timeoutMin := cfg.Security.DangerousOps.ConfirmTimeoutMin
	if timeoutMin <= 0 {
		timeoutMin = 30 // default 30 min
	}

	return &ConfirmGate{
		ops:           ops,
		criticalPaths: cfg.Security.DangerousOps.CriticalPaths,
		timeoutMin:    timeoutMin,
		sender:        sender,
		healthGetter:  health,
	}
}

// ApproveTool evaluates whether a tool call requires confirmation
// and, if so, requests it via Telegram.
// Returns (decision, nil) in all cases — errors from the sender
// are converted to deny decisions (fail-closed).
func (cg *ConfirmGate) ApproveTool(
	ctx context.Context,
	req *ConfirmApprovalRequest,
) (ConfirmApprovalDecision, error) {
	approved := ConfirmApprovalDecision{Approved: true}

	// 1. Build operation key and match against dangerous_ops
	opKey := req.Tool + "." + getOperation(req.Arguments)
	op, found := cg.ops[opKey]
	if !found {
		return approved, nil // not in table → allow
	}

	// 2. Reflex: compose.restart + exited container → allow without confirmation
	if opKey == "compose.restart" && isExited(req.Arguments) {
		logger.InfoCF("confirm_gate",
			"reflex: compose.restart + exited → allow",
			nil,
		)
		return approved, nil
	}

	// 3. Evaluate confirm rule
	needConfirm := cg.evaluateConfirmRule(op, req.Arguments)
	if !needConfirm {
		return approved, nil
	}

	// 4. Guard: sender unavailable → deny (fail-closed)
	if cg.sender == nil {
		return ConfirmApprovalDecision{
			Approved: false,
			Reason:   "confirmation error: sender unavailable",
		}, nil
	}

	// 5. Send to Telegram + wait for response
	msg := fmt.Sprintf(
		"\U0001f534 Подтвердите %s (%s)\nУровень: %s\nОтветьте да/нет (%d мин)",
		opKey, summarizeArgs(req.Arguments), op.Level, cg.timeoutMin,
	)

	timeout := time.Duration(cg.timeoutMin) * time.Minute
	confirmCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := cg.sender.SendConfirmation(confirmCtx, msg)
	if err != nil {
		// Error → deny (fail-closed)
		logger.ErrorCF("confirm_gate",
			fmt.Sprintf("confirmation error for %s: %v", opKey, err),
			nil,
		)
		return ConfirmApprovalDecision{
			Approved: false,
			Reason:   "confirmation error: " + err.Error(),
		}, nil
	}

	if !result {
		return ConfirmApprovalDecision{
			Approved: false,
			Reason:   "менеджер отклонил",
		}, nil
	}

	return approved, nil
}

// evaluateConfirmRule determines whether confirmation is needed
// based on the DangerousOpEntry's Confirm mode.
func (cg *ConfirmGate) evaluateConfirmRule(
	op config.DangerousOpEntry,
	args map[string]any,
) bool {
	switch op.Confirm {
	case config.ConfirmAlways:
		return true

	case config.ConfirmNever:
		return false

	case config.ConfirmIfHealthy:
		if cg.healthGetter == nil {
			// No health provider → confirm for safety
			return true
		}
		state := cg.healthGetter.GetSystemState()
		// healthy → confirm; degraded/offline → allow (emergency fix)
		needConfirm := state.Status == StateHealthy.Status
		if !needConfirm {
			logger.InfoCF("confirm_gate",
				fmt.Sprintf("if_healthy: system %s → allow without confirmation",
					state.Status),
				nil,
			)
		}
		return needConfirm

	case config.ConfirmIfCritical:
		needConfirm := isInCriticalPath(args, cg.criticalPaths)
		if !needConfirm {
			logger.InfoCF("confirm_gate",
				"if_critical_path: path not in critical_paths → allow",
				nil,
			)
		}
		return needConfirm

	default:
		// Unknown confirm mode → confirm for safety
		logger.WarnCF("confirm_gate",
			fmt.Sprintf("unknown confirm mode %q → confirming for safety",
				string(op.Confirm)),
			nil,
		)
		return true
	}
}

// getOperation extracts the "operation" field from tool arguments.
// Returns empty string if not present or not a string.
func getOperation(args map[string]any) string {
	if args == nil {
		return ""
	}
	if op, ok := args["operation"]; ok {
		if s, ok := op.(string); ok {
			return s
		}
	}
	return ""
}

// isExited checks if the container state in arguments is "exited".
// Used for the compose.restart reflex: exited container → allow restart
// without confirmation (recovery scenario).
func isExited(args map[string]any) bool {
	if args == nil {
		return false
	}
	if state, ok := args["state"]; ok {
		if s, ok := state.(string); ok {
			return s == "exited"
		}
	}
	return false
}

// isInCriticalPath checks if the "path" (or "file") argument matches
// any of the critical_paths glob patterns.
func isInCriticalPath(args map[string]any, criticalPaths []string) bool {
	if args == nil || len(criticalPaths) == 0 {
		return false
	}

	targetPath := extractPath(args)
	if targetPath == "" {
		return false
	}

	for _, pattern := range criticalPaths {
		if matched, err := filepath.Match(pattern, targetPath); err == nil && matched {
			return true
		}
	}
	return false
}

// extractPath extracts a file path from tool arguments.
// Checks "path" first, then "file" as fallback.
func extractPath(args map[string]any) string {
	for _, key := range []string{"path", "file"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// summarizeArgs creates a short human-readable summary of tool arguments
// for the Telegram confirmation message.
func summarizeArgs(args map[string]any) string {
	if args == nil || len(args) == 0 {
		return "no args"
	}

	var parts []string
	for k, v := range args {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}

	summary := strings.Join(parts, ", ")
	if len(summary) > 100 {
		summary = summary[:97] + "..."
	}
	return summary
}
