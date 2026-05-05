// PIKA-V3: PikaContextManager adapter and factory registration.
// Bridges pkg/pika.PikaContextManager to pkg/agent.ContextManager
// interface without circular imports.

package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
	"github.com/sipeed/picoclaw/pkg/providers"
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
	planStore := pika.NewActivePlanStore()

	// PIKA-V3 wave 4: Get BotMemory from PikaSessionStore
	var botmem *pika.BotMemory
	if ps, ok := agent.Sessions.(*pika.PikaSessionStore); ok {
		botmem = ps.GetBotMemory()
	}

	// PIKA-V3 wave 4: Try to create real Archivist.
	// Only uses dedicated "background" model — no fallback to main
	// model to avoid interfering with test mock servers.
	var arch pika.ArchivistCaller
	if botmem != nil && al.cfg != nil {
		archProvider := resolveArchivistProvider(al.cfg)
		if archProvider != nil {
			arch = pika.NewArchivist(
				botmem, archProvider, trail, meta,
				pika.DefaultArchivistConfig(),
			)
			logger.InfoCF("pika",
				"Real Archivist wired successfully",
				nil,
			)
		}
	}
	if arch == nil {
		arch = pika.NewNoopArchivistCaller()
		logger.InfoCF("pika",
			"Using NoopArchivist (no background model)",
			nil,
		)
	}

	cm := pika.NewPikaContextManager(
		agent.Workspace, trail, meta, sp, arch,
	)

	// PIKA-V3 wave 4: Wire BotMemory and PlanStore
	if botmem != nil {
		cm.SetBotMemory(botmem)
	}
	cm.SetPlanStore(planStore)

	logger.InfoCF("pika",
		"PikaContextManager initialized (wave 4)",
		map[string]any{"workspace": agent.Workspace},
	)

	return &pikaContextManagerAdapter{
		cm: cm,
		al: al,
	}, nil
}

// resolveArchivistProvider creates an LLM provider for the
// "background" model from config. Returns nil if the model
// is not configured. Does NOT fall back to the main model
// to avoid side effects in tests and production.
func resolveArchivistProvider(
	cfg *config.Config,
) providers.LLMProvider {
	mc, err := cfg.GetModelConfig("background")
	if err != nil {
		// "background" model not configured → no provider
		return nil
	}
	p, _, pErr := providers.CreateProviderFromConfig(mc)
	if pErr != nil {
		logger.WarnCF("pika",
			"Archivist provider creation failed",
			map[string]any{"error": pErr.Error()},
		)
		return nil
	}
	return p
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
		return &AssembleResponse{ //nolint:nilerr // degraded mode
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
