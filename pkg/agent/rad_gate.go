package agent

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
)

// radPreActionGate runs RAD analysis before a single tool call.
// PIKA-V3: Direct call in pipeline — D-136a checkpoint F16, TZ-v2-8i.
func radPreActionGate(
	ctx context.Context,
	al *AgentLoop,
	sessionKey string,
	toolName string,
) (blocked bool, reason string) {
	if al == nil || al.rad == nil {
		return false, ""
	}

	var reasoning string
	if al.botmem != nil {
		var err error
		reasoning, err = al.botmem.GetLastReasoningText(ctx, sessionKey)
		if err != nil {
			logger.DebugCF("rad", "GetLastReasoningText failed", map[string]any{"err": err.Error()})
		}
	}

	result := al.rad.Analyze(ctx, reasoning, nil, &pika.RADToolCall{
		Name: toolName,
	})

	switch result.Verdict {
	case pika.RADAnomaly:
		logger.WarnCF("rad", "RAD BLOCK", map[string]any{
			"score": result.Score, "detectors": fmt.Sprint(result.Detectors), "reason": result.Reason,
		})
		return true, result.Reason
	case pika.RADWarning:
		logger.WarnCF("rad", "RAD WARN", map[string]any{
			"score": result.Score, "detectors": fmt.Sprint(result.Detectors), "reason": result.Reason,
		})
	}
	return false, ""
}
