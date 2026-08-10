// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// ExecuteTools executes the tool loop, handling BeforeTool/ApproveTool/AfterTool hooks,
// tool execution with async callbacks, media delivery, and steering injection.
// Returns ToolControl indicating what the coordinator should do next:
//   - ToolControlContinue: all tool results handled, pendingMessages or steering exists, continue turn
//   - ToolControlBreak: tool loop exited, proceed to coordinator's hardAbort/finalContent/finalize
func (p *Pipeline) ExecuteTools(
	ctx context.Context,
	turnCtx context.Context,
	ts *turnState,
	exec *turnExecution,
	iteration int,
) ToolControl {
	al := p.al
	normalizedToolCalls := exec.normalizedToolCalls

	ts.setPhase(TurnPhaseTools)
	messages := exec.messages
	handledAttachments := make([]providers.Attachment, 0)

toolLoop:
	for i, tc := range normalizedToolCalls {
		if ts.hardAbortRequested() {
			exec.abortedByHardAbort = true
			return ToolControlBreak
		}

		toolName := tc.Name
		toolArgs := cloneStringAnyMap(tc.Arguments)

		// PIKA-V3: RAD pre-action gate (D-136a F16, TZ-v2-8i).
		if blocked, radReason := radPreActionGate(ctx, al, ts.sessionKey, toolName); blocked {
			logger.WarnCF("pipeline", "RAD blocked tool", map[string]any{"tool": toolName, "reason": radReason})
			return ToolControlBreak
		}

		if al.hooks != nil {
			toolReq, decision := al.hooks.BeforeTool(turnCtx, &ToolCallHookRequest{
				Meta:      ts.eventMeta("runTurn", "turn.tool.before"),
				Context:   cloneTurnContext(ts.turnCtx),
				Tool:      toolName,
				Arguments: toolArgs,
			})
			switch decision.normalizedAction() {
			case HookActionContinue, HookActionModify:
				if toolReq != nil {
					toolName = toolReq.Tool
					toolArgs = toolReq.Arguments
				}
			case HookActionRespond:
				// D-AUDIT-69: respond для ЗАРЕГИСТРИРОВАННОГО тула не обходит
				// ApproveTool — иначе hook мог бы подменить результат exec и т.п.
				// Плагинные (незарегистрированные) тулы — как раньше, без одобрения.
				if _, registered := ts.agent.Tools.Get(toolName); registered && al.hooks != nil {
					approval := al.hooks.ApproveTool(turnCtx, &ToolApprovalRequest{
						Meta:      ts.eventMeta("runTurn", "turn.tool.approve"),
						Context:   cloneTurnContext(ts.turnCtx),
						Tool:      toolName,
						Arguments: toolArgs,
					})
					if !approval.Approved {
						exec.allResponsesHandled = false
						denyContent := hookDeniedToolContent("Hook respond denied by approval hook", approval.Reason)
						al.emitEvent(
							EventKindToolExecSkipped,
							ts.eventMeta("runTurn", "turn.tool.skipped"),
							ToolExecSkippedPayload{Tool: toolName, Reason: denyContent},
						)
						deniedMsg := providers.Message{Role: "tool", Content: denyContent, ToolCallID: tc.ID}
						messages = append(messages, deniedMsg)
						if !ts.opts.NoHistory {
							ts.agent.Sessions.AddFullMessage(ts.sessionKey, deniedMsg)
							ts.recordPersistedMessage(deniedMsg)
						}
						continue
					}
				}
				if toolReq != nil && toolReq.HookResult != nil {
					hookResult := toolReq.HookResult

					argsJSON, _ := json.Marshal(toolArgs)
					argsPreview := utils.Truncate(string(argsJSON), 200)
					logger.InfoCF("agent", fmt.Sprintf("Tool call (hook respond): %s(%s)", toolName, argsPreview),
						map[string]any{
							"agent_id":  ts.agent.ID,
							"tool":      toolName,
							"iteration": iteration,
						})

					al.emitEvent(
						EventKindToolExecStart,
						ts.eventMeta("runTurn", "turn.tool.start"),
						ToolExecStartPayload{
							Tool:      toolName,
							Arguments: cloneEventArguments(toolArgs),
						},
					)

					if shouldPublishToolFeedback(al.cfg, ts) && ts.channel != "pico" {
						toolFeedbackMaxLen := al.cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength()
						toolFeedbackExplanation := toolFeedbackExplanationForToolCall(
							exec.response,
							tc,
							messages,
						)
						feedbackMsg := utils.FormatToolFeedbackMessage(
							toolName,
							toolFeedbackExplanation,
							toolFeedbackArgsPreview(toolArgs, toolFeedbackMaxLen),
						)
						fbCtx, fbCancel := context.WithTimeout(turnCtx, 3*time.Second)
						_ = al.bus.PublishOutbound(fbCtx, outboundMessageForTurnWithKind(ts, feedbackMsg, messageKindToolFeedback))
						fbCancel()
					}

					toolDuration := time.Duration(0)

					shouldSendForUser := !hookResult.Silent && hookResult.ForUser != "" &&
						(ts.opts.SendResponse || hookResult.ResponseHandled)
					if shouldSendForUser {
						al.bus.PublishOutbound(ctx, bus.OutboundMessage{
							Context: bus.InboundContext{
								Channel: ts.channel,
								ChatID:  ts.chatID,
								Raw: map[string]string{
									"is_tool_call": "true",
								},
							},
							Content: hookResult.ForUser,
						})
					}

					if len(hookResult.Media) > 0 && hookResult.ResponseHandled {
						parts := make([]bus.MediaPart, 0, len(hookResult.Media))
						for _, ref := range hookResult.Media {
							part := bus.MediaPart{Ref: ref}
							if al.mediaStore != nil {
								if _, meta, err := al.mediaStore.ResolveWithMeta(ref); err == nil {
									part.Filename = meta.Filename
									part.ContentType = meta.ContentType
									part.Type = inferMediaType(meta.Filename, meta.ContentType)
								}
							}
							parts = append(parts, part)
						}
						outboundMedia := bus.OutboundMediaMessage{
							Channel: ts.channel,
							ChatID:  ts.chatID,
							Context: outboundContextFromInbound(
								ts.opts.Dispatch.InboundContext,
								ts.channel,
								ts.chatID,
								ts.opts.Dispatch.ReplyToMessageID(),
							),
							AgentID:    ts.agent.ID,
							SessionKey: ts.sessionKey,
							Scope:      outboundScopeFromSessionScope(ts.opts.Dispatch.SessionScope),
							Parts:      parts,
						}
						if al.channelManager != nil && ts.channel != "" && !constants.IsInternalChannel(ts.channel) {
							if err := al.channelManager.SendMedia(ctx, outboundMedia); err != nil {
								logger.WarnCF("agent", "Failed to deliver hook media",
									map[string]any{
										"agent_id": ts.agent.ID,
										"tool":     toolName,
										"channel":  ts.channel,
										"chat_id":  ts.chatID,
										"error":    err.Error(),
									})
								hookResult.IsError = true
								hookResult.ForLLM = fmt.Sprintf("failed to deliver attachment: %v", err)
							} else {
								handledAttachments = append(
									handledAttachments,
									buildProviderAttachments(al.mediaStore, hookResult.Media)...,
								)
							}
						} else if al.bus != nil {
							al.bus.PublishOutboundMedia(ctx, outboundMedia)
							hookResult.ResponseHandled = false
						}
					}

					if !hookResult.ResponseHandled {
						exec.allResponsesHandled = false
					}

					contentForLLM := hookResult.ContentForLLM()
					if al.cfg.Tools.IsFilterSensitiveDataEnabled() {
						contentForLLM = al.cfg.FilterSensitiveData(contentForLLM)
					}

					toolResultMsg := providers.Message{
						Role:       "tool",
						Content:    contentForLLM,
						ToolCallID: tc.ID,
					}

					if len(hookResult.Media) > 0 && !hookResult.ResponseHandled {
						hookResult.ArtifactTags = buildArtifactTags(al.mediaStore, hookResult.Media)
						contentForLLM = hookResult.ContentForLLM()
						if al.cfg.Tools.IsFilterSensitiveDataEnabled() {
							contentForLLM = al.cfg.FilterSensitiveData(contentForLLM)
						}
						toolResultMsg.Content = contentForLLM
						toolResultMsg.Media = append(toolResultMsg.Media, hookResult.Media...)
					}

					al.emitEvent(
						EventKindToolExecEnd,
						ts.eventMeta("runTurn", "turn.tool.end"),
						ToolExecEndPayload{
							Tool:       toolName,
							Duration:   toolDuration,
							ForLLMLen:  len(contentForLLM),
							Operation:  toolOperationArg(toolArgs),
							ForUserLen: len(hookResult.ForUser),
							IsError:    hookResult.IsError,
							Async:      hookResult.Async,
						},
					)

					messages = append(messages, toolResultMsg)
					if !ts.opts.NoHistory {
						ts.agent.Sessions.AddFullMessage(ts.sessionKey, toolResultMsg)
						ts.recordPersistedMessage(toolResultMsg)
						ts.ingestMessage(turnCtx, al, toolResultMsg)
					}

					if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
						exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
					}

					skipReason := ""
					skipMessage := ""
					if len(exec.pendingMessages) > 0 {
						skipReason = "queued user steering message"
						skipMessage = "Skipped due to queued user message."
					} else if gracefulPending, _ := ts.gracefulInterruptRequested(); gracefulPending {
						skipReason = "graceful interrupt requested"
						skipMessage = "Skipped due to graceful interrupt."
					}

					if skipReason != "" {
						remaining := len(normalizedToolCalls) - i - 1
						if remaining > 0 {
							logger.InfoCF("agent", "Turn checkpoint: skipping remaining tools after hook respond",
								map[string]any{
									"agent_id":  ts.agent.ID,
									"completed": i + 1,
									"skipped":   remaining,
									"reason":    skipReason,
								})
							for j := i + 1; j < len(normalizedToolCalls); j++ {
								skippedTC := normalizedToolCalls[j]
								al.emitEvent(
									EventKindToolExecSkipped,
									ts.eventMeta("runTurn", "turn.tool.skipped"),
									ToolExecSkippedPayload{
										Tool:   skippedTC.Name,
										Reason: skipReason,
									},
								)
								skippedMsg := providers.Message{
									Role:       "tool",
									Content:    skipMessage,
									ToolCallID: skippedTC.ID,
								}
								messages = append(messages, skippedMsg)
								if !ts.opts.NoHistory {
									ts.agent.Sessions.AddFullMessage(ts.sessionKey, skippedMsg)
									ts.recordPersistedMessage(skippedMsg)
								}
							}
						}
						break toolLoop
					}

					if ts.pendingResults != nil {
						select {
						case result, ok := <-ts.pendingResults:
							if ok && result != nil && result.ForLLM != "" {
								content := al.cfg.FilterSensitiveData(result.ForLLM)
								msg := subTurnResultPromptMessage(content)
								messages = append(messages, msg)
								ts.agent.Sessions.AddFullMessage(ts.sessionKey, msg)
							}
						default:
						}
					}

					continue
				}
				logger.WarnCF("agent", "Hook returned respond action but no HookResult provided",
					map[string]any{
						"agent_id": ts.agent.ID,
						"tool":     toolName,
						"action":   "respond",
					})
			case HookActionDenyTool:
				exec.allResponsesHandled = false
				denyContent := hookDeniedToolContent("Tool execution denied by hook", decision.Reason)
				al.emitEvent(
					EventKindToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						Tool:   toolName,
						Reason: denyContent,
					},
				)
				deniedMsg := providers.Message{
					Role:       "tool",
					Content:    denyContent,
					ToolCallID: tc.ID,
				}
				messages = append(messages, deniedMsg)
				if !ts.opts.NoHistory {
					ts.agent.Sessions.AddFullMessage(ts.sessionKey, deniedMsg)
					ts.recordPersistedMessage(deniedMsg)
				}
				continue
			case HookActionAbortTurn:
				exec.abortedByHook = true
				return ToolControlBreak
			case HookActionHardAbort:
				_ = ts.requestHardAbort()
				exec.abortedByHardAbort = true
				return ToolControlBreak
			}
		}

		if al.hooks != nil {
			approval := al.hooks.ApproveTool(turnCtx, &ToolApprovalRequest{
				Meta:      ts.eventMeta("runTurn", "turn.tool.approve"),
				Context:   cloneTurnContext(ts.turnCtx),
				Tool:      toolName,
				Arguments: toolArgs,
			})
			if !approval.Approved {
				exec.allResponsesHandled = false
				denyContent := hookDeniedToolContent("Tool execution denied by approval hook", approval.Reason)
				al.emitEvent(
					EventKindToolExecSkipped,
					ts.eventMeta("runTurn", "turn.tool.skipped"),
					ToolExecSkippedPayload{
						Tool:   toolName,
						Reason: denyContent,
					},
				)
				deniedMsg := providers.Message{
					Role:       "tool",
					Content:    denyContent,
					ToolCallID: tc.ID,
				}
				messages = append(messages, deniedMsg)
				if !ts.opts.NoHistory {
					ts.agent.Sessions.AddFullMessage(ts.sessionKey, deniedMsg)
					ts.recordPersistedMessage(deniedMsg)
				}
				continue
			}
		}

		argsJSON, _ := json.Marshal(toolArgs)
		argsPreview := utils.Truncate(string(argsJSON), 200)
		logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", toolName, argsPreview),
			map[string]any{
				"agent_id":  ts.agent.ID,
				"tool":      toolName,
				"iteration": iteration,
			})
		al.emitEvent(
			EventKindToolExecStart,
			ts.eventMeta("runTurn", "turn.tool.start"),
			ToolExecStartPayload{
				Tool:      toolName,
				Arguments: cloneEventArguments(toolArgs),
			},
		)

		if shouldPublishToolFeedback(al.cfg, ts) && ts.channel != "pico" {
			toolFeedbackMaxLen := al.cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength()
			toolFeedbackExplanation := toolFeedbackExplanationForToolCall(
				exec.response,
				tc,
				messages,
			)
			feedbackMsg := utils.FormatToolFeedbackMessage(
				toolName,
				toolFeedbackExplanation,
				toolFeedbackArgsPreview(toolArgs, toolFeedbackMaxLen),
			)
			fbCtx, fbCancel := context.WithTimeout(turnCtx, 3*time.Second)
			_ = al.bus.PublishOutbound(fbCtx, outboundMessageForTurnWithKind(ts, feedbackMsg, messageKindToolFeedback))
			fbCancel()
		}

		toolCallID := tc.ID
		asyncToolName := toolName
		asyncCallback := func(_ context.Context, result *tools.ToolResult) {
			if !result.Silent && result.ForUser != "" {
				outCtx, outCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer outCancel()
				_ = al.bus.PublishOutbound(outCtx, outboundMessageForTurn(ts, result.ForUser))
			}

			content := result.ContentForLLM()
			if content == "" {
				return
			}

			content = al.cfg.FilterSensitiveData(content)

			logger.InfoCF("agent", "Async tool completed, publishing result",
				map[string]any{
					"tool":        asyncToolName,
					"content_len": len(content),
					"channel":     ts.channel,
				})
			al.emitEvent(
				EventKindFollowUpQueued,
				ts.scope.meta(iteration, "runTurn", "turn.follow_up.queued"),
				FollowUpQueuedPayload{
					SourceTool: asyncToolName,
					ContentLen: len(content),
				},
			)
			pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer pubCancel()
			_ = al.bus.PublishInbound(pubCtx, bus.InboundMessage{
				Context: bus.InboundContext{
					Channel:  "system",
					ChatID:   fmt.Sprintf("%s:%s", ts.channel, ts.chatID),
					ChatType: "direct",
					SenderID: fmt.Sprintf("async:%s", asyncToolName),
				},
				Content: content,
			})
		}

		toolStart := time.Now()
		execCtx := tools.WithToolInboundContext(
			turnCtx,
			ts.channel,
			ts.chatID,
			ts.opts.Dispatch.MessageID(),
			ts.opts.Dispatch.ReplyToMessageID(),
		)
		execCtx = tools.WithToolSessionContext(
			execCtx,
			ts.agent.ID,
			ts.sessionKey,
			ts.opts.Dispatch.SessionScope,
		)
		toolResult := ts.agent.Tools.ExecuteWithContext(
			execCtx,
			toolName,
			toolArgs,
			ts.channel,
			ts.chatID,
			asyncCallback,
		)
		toolDuration := time.Since(toolStart)

		if ts.hardAbortRequested() {
			exec.abortedByHardAbort = true
			return ToolControlBreak
		}

		// PIKA-V3: журнал событий для MCP-вызовов (D-SEC-MCP слой 5,
		// D-AUDIT-59). Ключи mcp.<сервер>.call/call_fail/blocked. Пишет Go.
		mcpEventBlocked := false
		mcpEventServer, isMCPTool := mcpServerFromToolName(toolName)

		// PIKA-V3: MCP Security — sanitize MCP tool output (TZ-v2-9b).
		if al.mcpSecurity != nil && toolResult != nil && !toolResult.IsError {
			output, blocked := al.mcpSecurity.ProcessToolOutput(toolName, toolResult.ForLLM)
			toolResult.ForLLM = output
			if blocked {
				toolResult.IsError = true
				mcpEventBlocked = true
			}
		}

		if isMCPTool && al.autoEvent != nil {
			op := "call"
			if toolResult.IsError {
				op = "call_fail"
			}
			if mcpEventBlocked {
				op = "blocked"
			}
			_ = al.autoEvent.HandleToolResult(
				ctx, "mcp."+mcpEventServer, op, toolResult.IsError,
				ts.sessionKey, ts.scope.turnID,
			)
		}
		if al.hooks != nil {
			toolResp, decision := al.hooks.AfterTool(turnCtx, &ToolResultHookResponse{
				Meta:      ts.eventMeta("runTurn", "turn.tool.after"),
				Context:   cloneTurnContext(ts.turnCtx),
				Tool:      toolName,
				Arguments: toolArgs,
				Result:    toolResult,
				Duration:  toolDuration,
			})
			switch decision.normalizedAction() {
			case HookActionContinue, HookActionModify:
				if toolResp != nil {
					if toolResp.Tool != "" {
						toolName = toolResp.Tool
					}
					if toolResp.Result != nil {
						toolResult = toolResp.Result
					}
				}
			case HookActionAbortTurn:
				exec.abortedByHook = true
				return ToolControlBreak
			case HookActionHardAbort:
				_ = ts.requestHardAbort()
				exec.abortedByHardAbort = true
				return ToolControlBreak
			}
		}

		if toolResult == nil {
			toolResult = tools.ErrorResult("hook returned nil tool result")
		}

		if len(toolResult.Media) > 0 && toolResult.ResponseHandled {
			parts := make([]bus.MediaPart, 0, len(toolResult.Media))
			for _, ref := range toolResult.Media {
				part := bus.MediaPart{Ref: ref}
				if al.mediaStore != nil {
					if _, meta, err := al.mediaStore.ResolveWithMeta(ref); err == nil {
						part.Filename = meta.Filename
						part.ContentType = meta.ContentType
						part.Type = inferMediaType(meta.Filename, meta.ContentType)
					}
				}
				parts = append(parts, part)
			}
			outboundMedia := bus.OutboundMediaMessage{
				Channel: ts.channel,
				ChatID:  ts.chatID,
				Context: outboundContextFromInbound(
					ts.opts.Dispatch.InboundContext,
					ts.channel,
					ts.chatID,
					ts.opts.Dispatch.ReplyToMessageID(),
				),
				AgentID:    ts.agent.ID,
				SessionKey: ts.sessionKey,
				Scope:      outboundScopeFromSessionScope(ts.opts.Dispatch.SessionScope),
				Parts:      parts,
			}
			if al.channelManager != nil && ts.channel != "" && !constants.IsInternalChannel(ts.channel) {
				if err := al.channelManager.SendMedia(ctx, outboundMedia); err != nil {
					logger.WarnCF("agent", "Failed to deliver handled tool media",
						map[string]any{
							"agent_id": ts.agent.ID,
							"tool":     toolName,
							"channel":  ts.channel,
							"chat_id":  ts.chatID,
							"error":    err.Error(),
						})
					toolResult = tools.ErrorResult(fmt.Sprintf("failed to deliver attachment: %v", err)).WithError(err)
				} else {
					handledAttachments = append(
						handledAttachments,
						buildProviderAttachments(al.mediaStore, toolResult.Media)...,
					)
				}
			} else if al.bus != nil {
				al.bus.PublishOutboundMedia(ctx, outboundMedia)
				toolResult.ResponseHandled = false
			}
		}

		if len(toolResult.Media) > 0 && !toolResult.ResponseHandled {
			toolResult.ArtifactTags = buildArtifactTags(al.mediaStore, toolResult.Media)
		}

		if !toolResult.ResponseHandled {
			exec.allResponsesHandled = false
		}

		shouldSendForUser := !toolResult.Silent &&
			toolResult.ForUser != "" &&
			(ts.opts.SendResponse || toolResult.ResponseHandled)
		if shouldSendForUser {
			al.bus.PublishOutbound(ctx, outboundMessageForTurn(ts, toolResult.ForUser))
			logger.DebugCF("agent", "Sent tool result to user",
				map[string]any{
					"tool":        toolName,
					"content_len": len(toolResult.ForUser),
				})
		}
		contentForLLM := toolResult.ContentForLLM()

		if al.cfg.Tools.IsFilterSensitiveDataEnabled() {
			contentForLLM = al.cfg.FilterSensitiveData(contentForLLM)
		}

		toolResultMsg := providers.Message{
			Role:       "tool",
			Content:    contentForLLM,
			ToolCallID: toolCallID,
		}
		if len(toolResult.Media) > 0 && !toolResult.ResponseHandled {
			toolResultMsg.Media = append(toolResultMsg.Media, toolResult.Media...)
		}
		// PIKA-V3: Record tool call for health sliding window (TZ-v2-9a block 2)
		if al.telemetry != nil {
			st := "ok"
			if toolResult.IsError {
				st = "error"
			}
			al.telemetry.RecordToolCall(pika.CallResult{
				ToolName:  toolName,
				LatencyMs: toolDuration.Milliseconds(),
				Status:    st,
				Timestamp: time.Now(),
			})
		}

		// PIKA-V3: связь «атомы подсказки → первый инструмент» (D-AUDIT-61).
		// Пишет Go, детерминированно; ошибка записи не роняет ход.
		if al.botmem != nil && toolResult != nil {
			toolOutcome := "success"
			if toolResult.IsError {
				toolOutcome = "failure"
			}
			if aErr := al.botmem.MarkAtomUsageToolAfter(
				ctx, ts.scope.turnID, toolName, toolOutcome,
			); aErr != nil {
				logger.WarnCF("agent", "atom_usage tool-after mark failed",
					map[string]any{"tool": toolName, "error": aErr.Error()})
			}
		}

		// PIKA-V3 (D-AUDIT-67): ошибка инструмента — триггер фонового
		// расследования Рефлексора (event-driven, троттлинг внутри).
		if toolResult.IsError && al.reflector != nil {
			al.reflector.TriggerInvestigation(
				ts.sessionKey, "tool_error:"+toolName,
			)
		}

		// PIKA-V3: TRAIL feed + loop detection safety net (D-136a, D-AUDIT-58).
		// Прямой вызов, не хук: safety net нельзя отключить конфигом.
		// Петля = тот же инструмент + те же аргументы + тот же результат
		// N раз подряд (урок Gemini CLI: та же команда с ДРУГИМ
		// результатом — прогресс, не петля; Result входит в сравнение).
		if adapter, ok := al.contextManager.(*pikaContextManagerAdapter); ok &&
			adapter != nil && adapter.cm != nil {
			if trail := adapter.cm.GetTrail(); trail != nil {
				operation, _ := toolArgs["operation"].(string)
				if operation == "" {
					operation = utils.Truncate(string(argsJSON), 40)
				}
				trail.Add(pika.TrailEntry{
					ToolName:   toolName,
					Operation:  operation,
					Result:     utils.Truncate(toolResult.ForLLM, 100),
					OK:         !toolResult.IsError,
					DurationMs: int(toolDuration.Milliseconds()),
					Timestamp:  time.Now(),
				})
				if pika.CheckLoopDetection(trail, pika.DefaultLoopDetectionThreshold) {
					logger.WarnCF("pipeline", "Loop detected, stopping turn",
						map[string]any{
							"agent_id":  ts.agent.ID,
							"tool":      toolName,
							"threshold": pika.DefaultLoopDetectionThreshold,
						})
					notice := fmt.Sprintf(
						"⚠️ Обнаружено зацикливание: %s вызван %d раза подряд "+
							"с одинаковым результатом. Ход остановлен (loop_detected).",
						toolName, pika.DefaultLoopDetectionThreshold,
					)
					_ = al.bus.PublishOutbound(ctx, outboundMessageForTurn(ts, notice))
					if sender := pika.NewManagerSender(al.bus, al.cfg); sender != nil {
						_, _ = sender.SendMessage(ctx, notice)
					}
					exec.allResponsesHandled = true
					return ToolControlBreak
				}
			}
		}
		al.emitEvent(
			EventKindToolExecEnd,
			ts.eventMeta("runTurn", "turn.tool.end"),
			ToolExecEndPayload{
				Tool:       toolName,
				Operation:  toolOperationArg(toolArgs),
				Duration:   toolDuration,
				ForLLMLen:  len(contentForLLM),
				ForUserLen: len(toolResult.ForUser),
				IsError:    toolResult.IsError,
				Async:      toolResult.Async,
			},
		)
		messages = append(messages, toolResultMsg)
		if !ts.opts.NoHistory {
			ts.agent.Sessions.AddFullMessage(ts.sessionKey, toolResultMsg)
			ts.recordPersistedMessage(toolResultMsg)
			ts.ingestMessage(turnCtx, al, toolResultMsg)
		}

		if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
			exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
		}

		skipReason := ""
		skipMessage := ""
		if len(exec.pendingMessages) > 0 {
			skipReason = "queued user steering message"
			skipMessage = "Skipped due to queued user message."
		} else if gracefulPending, _ := ts.gracefulInterruptRequested(); gracefulPending {
			skipReason = "graceful interrupt requested"
			skipMessage = "Skipped due to graceful interrupt."
		}

		if skipReason != "" {
			remaining := len(normalizedToolCalls) - i - 1
			if remaining > 0 {
				logger.InfoCF("agent", "Turn checkpoint: skipping remaining tools",
					map[string]any{
						"agent_id":  ts.agent.ID,
						"completed": i + 1,
						"skipped":   remaining,
						"reason":    skipReason,
					})
				for j := i + 1; j < len(normalizedToolCalls); j++ {
					skippedTC := normalizedToolCalls[j]
					al.emitEvent(
						EventKindToolExecSkipped,
						ts.eventMeta("runTurn", "turn.tool.skipped"),
						ToolExecSkippedPayload{
							Tool:   skippedTC.Name,
							Reason: skipReason,
						},
					)
					skippedMsg := providers.Message{
						Role:       "tool",
						Content:    skipMessage,
						ToolCallID: skippedTC.ID,
					}
					messages = append(messages, skippedMsg)
					if !ts.opts.NoHistory {
						ts.agent.Sessions.AddFullMessage(ts.sessionKey, skippedMsg)
						ts.recordPersistedMessage(skippedMsg)
					}
				}
			}
			break toolLoop
		}

		if ts.pendingResults != nil {
			select {
			case result, ok := <-ts.pendingResults:
				if ok && result != nil && result.ForLLM != "" {
					content := al.cfg.FilterSensitiveData(result.ForLLM)
					msg := subTurnResultPromptMessage(content)
					messages = append(messages, msg)
					ts.agent.Sessions.AddFullMessage(ts.sessionKey, msg)
				}
			default:
			}
		}
	}

	exec.messages = messages

	// Continue if pending steering exists (regardless of allResponsesHandled).
	// This covers the case where tools were partially executed and skipped due to steering,
	// but one tool had ResponseHandled=false (so allResponsesHandled=false).
	if len(exec.pendingMessages) > 0 {
		logger.InfoCF("agent", "Pending steering after partial tool execution; continuing turn",
			map[string]any{
				"agent_id":            ts.agent.ID,
				"pending_count":       len(exec.pendingMessages),
				"allResponsesHandled": exec.allResponsesHandled,
			})
		exec.allResponsesHandled = false
		return ToolControlContinue
	}

	// Poll for newly arrived steering
	if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
		logger.InfoCF("agent", "Steering arrived after tool delivery; continuing turn",
			map[string]any{
				"agent_id":       ts.agent.ID,
				"steering_count": len(steerMsgs),
			})
		exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
		exec.allResponsesHandled = false
		return ToolControlContinue
	}

	// No pending steering: finalize or break depending on allResponsesHandled
	if exec.allResponsesHandled {
		summaryMsg := providers.Message{
			Role:        "assistant",
			Content:     handledToolResponseSummary,
			Attachments: append([]providers.Attachment(nil), handledAttachments...),
		}
		if !ts.opts.NoHistory {
			ts.agent.Sessions.AddFullMessage(ts.sessionKey, summaryMsg)
			ts.recordPersistedMessage(summaryMsg)
			ts.ingestMessage(turnCtx, al, summaryMsg)
			if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
				logger.WarnCF("agent", "Failed to save session after tool delivery",
					map[string]any{
						"agent_id": ts.agent.ID,
						"error":    err.Error(),
					})
			}
		}
		if ts.opts.EnableSummary {
			al.contextManager.Compact(turnCtx, &CompactRequest{
				SessionKey: ts.sessionKey,
				Reason:     ContextCompressReasonSummarize,
				Budget:     ts.agent.ContextWindow,
			})
		}
		ts.setPhase(TurnPhaseCompleted)
		ts.setFinalContent("")
		if al.channelManager != nil && ts.channel != "" {
			al.channelManager.DismissToolFeedback(ctx, ts.channel, ts.chatID, ts.opts.InboundContext)
		}
		logger.InfoCF("agent", "Tool output satisfied delivery; ending turn without follow-up LLM",
			map[string]any{
				"agent_id":   ts.agent.ID,
				"iteration":  iteration,
				"tool_count": len(normalizedToolCalls),
			})
		return ToolControlBreak
	}

	// allResponsesHandled=false and no pending steering: continue so coordinator
	// makes another LLM call. The tool result is in messages and the LLM will
	// return it as finalContent in the next iteration.
	ts.agent.Tools.TickTTL()
	logger.DebugCF("agent", "TTL tick after tool execution", map[string]any{
		"agent_id": ts.agent.ID, "iteration": iteration,
	})
	return ToolControlContinue
}

// toolOperationArg возвращает args["operation"], если оно строковое.
// Базовые инструменты такого аргумента не имеют — вернёт "".
func toolOperationArg(args map[string]any) string {
	op, _ := args["operation"].(string)
	return op
}

// mcpServerFromToolName выделяет имя MCP-сервера из имени инструмента
// вида "<сервер>__<инструмент>" (та же конвенция, что ProcessToolOutput).
func mcpServerFromToolName(toolName string) (string, bool) {
	parts := strings.SplitN(toolName, "__", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}
