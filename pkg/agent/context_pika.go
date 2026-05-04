// PIKA-V3: PikaContextManager adapter and factory registration.
// Bridges pkg/pika.PikaContextManager to pkg/agent.ContextManager
// interface without circular imports.

package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
)

func init() {
	if err := RegisterContextManager(
		"pika", pikaContextManagerFactory,
	); err != nil {
		logger.ErrorCF("agent",
			"Failed to register pika context manager",
			map[string]any{"error": err.Error()},
		)
	}
}

// pikaContextManagerFactory creates a PikaContextManager wrapped
// as agent.ContextManager. Signature matches ContextManagerFactory.
func pikaContextManagerFactory(
	_ json.RawMessage, al *AgentLoop,
) (ContextManager, error) {
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		return nil, fmt.Errorf(
			"pika/cm: no default agent configured",
		)
	}

	trail := pika.NewTrail()
	meta := pika.NewMeta()
	sp := pika.NewAlwaysHealthyProvider()
	arch := pika.NewNoopArchivistCaller()

	cm := pika.NewPikaContextManager(
		agent.Workspace, trail, meta, sp, arch,
	)

	logger.InfoCF("pika",
		"PikaContextManager initialized",
		map[string]any{"workspace": agent.Workspace},
	)

	return &pikaContextManagerAdapter{
		cm: cm,
		al: al,
	}, nil
}

// pikaContextManagerAdapter wraps pika.PikaContextManager as
// agent.ContextManager. Avoids circular import between
// pkg/agent and pkg/pika.
type pikaContextManagerAdapter struct {
	cm *pika.PikaContextManager
	al *AgentLoop
}

func (a *pikaContextManagerAdapter) Assemble(
	ctx context.Context, req *AssembleRequest,
) (*AssembleResponse, error) {
	agent := a.al.registry.GetDefaultAgent()
	if agent == nil {
		return &AssembleResponse{}, nil
	}

	// Get history from PikaSessionStore (SQLite)
	history := agent.Sessions.GetHistory(req.SessionKey)
	summary := agent.Sessions.GetSummary(req.SessionKey)

	// Build full system prompt
	systemPrompt, err := a.cm.BuildSystemPrompt(
		ctx, req.SessionKey,
	)
	if err != nil {
		logger.WarnCF("pika",
			"BuildSystemPrompt failed (degraded mode)",
			map[string]any{"error": err.Error()},
		)
		// Degraded: return history without system prompt;
		// upstream ContextBuilder will be used as fallback.
		return &AssembleResponse{
			History: history,
			Summary: summary,
		}, nil
	}

	return &AssembleResponse{
		History:      history,
		Summary:      summary,
		SystemPrompt: systemPrompt,
	}, nil
}

func (a *pikaContextManagerAdapter) Compact(
	_ context.Context, req *CompactRequest,
) error {
	return a.cm.Compact(
		req.SessionKey, string(req.Reason),
	)
}

func (a *pikaContextManagerAdapter) Ingest(
	_ context.Context, req *IngestRequest,
) error {
	return a.cm.Ingest(req.SessionKey)
}

func (a *pikaContextManagerAdapter) Clear(
	_ context.Context, sessionKey string,
) error {
	return a.cm.Clear(sessionKey)
}
