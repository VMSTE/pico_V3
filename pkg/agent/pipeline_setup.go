// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// SetupTurn extracts the one-time initialization phase, returning a
// turnExecution populated with history, messages, and candidate selection.
// It replaces lines 56-145 of the original runTurn.
func (p *Pipeline) SetupTurn(ctx context.Context, ts *turnState) (*turnExecution, error) {
	cfg := p.Cfg
	maxMediaSize := cfg.Agents.Defaults.GetMaxMediaSize()

	var resp *AssembleResponse
	var history []providers.Message
	var summary string
	if !ts.opts.NoHistory {
		var err error
		resp, err = p.ContextManager.Assemble(ctx, &AssembleRequest{
			SessionKey: ts.sessionKey,
			Budget:     ts.agent.ContextWindow,
			MaxTokens:  ts.agent.MaxTokens,
		})
		if err != nil || resp == nil {
			resp = nil
		} else {
			history = resp.History
			summary = resp.Summary
		}
	}
	ts.captureRestorePoint(history, summary)

	// --- PIKA-V3 BYPASS: if CM returned a ready-made system prompt, skip upstream ContextBuilder ---
	var messages []providers.Message
	if resp != nil && resp.SystemPrompt != "" {
		// Append dynamic per-turn context (time, runtime, session, sender) that upstream
		// ContextBuilder.BuildMessagesFromPrompt would normally add via buildDynamicContext.
		// Without this, tests like TestProcessMessage_IncludesCurrentSenderInDynamicContext fail.
		dynamicCtx := ts.agent.ContextBuilder.buildDynamicContext(
			ts.channel, ts.chatID,
			ts.opts.Dispatch.SenderID(), ts.opts.SenderDisplayName,
		)
		systemPrompt := resp.SystemPrompt + "\n\n---\n\n" + dynamicCtx

		messages = []providers.Message{
			{Role: "system", Content: systemPrompt},
		}
		// Add conversation history from session store
		messages = append(messages, history...)
		// Add current user message
		messages = append(messages, userPromptMessage(ts.userMessage, ts.media))
		messages = resolveMediaRefs(messages, p.MediaStore, maxMediaSize)
	} else {
		// Fallback to upstream ContextBuilder (SystemPrompt empty or no PikaContextManager)
		messages = ts.agent.ContextBuilder.BuildMessagesFromPrompt(
			promptBuildRequestForTurn(ts, history, summary, ts.userMessage, ts.media),
		)
		messages = resolveMediaRefs(messages, p.MediaStore, maxMediaSize)
	}

	if !ts.opts.NoHistory {
		toolDefs := ts.agent.Tools.ToProviderDefs()
		if isOverContextBudget(ts.agent.ContextWindow, messages, toolDefs, ts.agent.MaxTokens) {
			// PIKA-V3: legacy proactive CompressReasonProactive removed (Phase C, wave 2b).
			// Context rotation via SessionLifecycle will handle budget overflow (wave 4).
			logger.WarnCF(
				"agent",
				"PIKA-V3: context budget exceeded before LLM call, legacy compression removed; pending session rotation (wave 4)",
				map[string]any{"session_key": ts.sessionKey},
			)
		}
	}

	if !ts.opts.NoHistory && (strings.TrimSpace(ts.userMessage) != "" || len(ts.media) > 0) {
		rootMsg := userPromptMessage(ts.userMessage, ts.media)
		if len(rootMsg.Media) > 0 {
			ts.agent.Sessions.AddFullMessage(ts.sessionKey, rootMsg)
		} else {
			ts.agent.Sessions.AddMessage(ts.sessionKey, rootMsg.Role, rootMsg.Content)
		}
		ts.recordPersistedMessage(rootMsg)
		ts.ingestMessage(ctx, p.al, rootMsg)
	}

	activeCandidates, activeModel, usedLight := p.al.selectCandidates(
		ts.agent,
		ts.userMessage,
		messages,
	)
	activeProvider := ts.agent.Provider
	if usedLight && ts.agent.LightProvider != nil {
		activeProvider = ts.agent.LightProvider
	}

	exec := newTurnExecution(
		ts.agent,
		ts.opts,
		history,
		summary,
		messages,
	)
	exec.activeCandidates = activeCandidates
	exec.activeModel = activeModel
	exec.activeProvider = activeProvider
	exec.usedLight = usedLight

	return exec, nil
}
