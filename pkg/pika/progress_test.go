package pika

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTGSender records calls for test assertions.
type mockTGSender struct {
	mu          sync.Mutex
	sendCalls   []string
	editCalls   []tgEditCall
	deleteCalls []string

	sendErr   error
	editErr   error
	deleteErr error
	nextMsgID string
}

type tgEditCall struct {
	messageID string
	text      string
}

func newMockTGSender() *mockTGSender {
	return &mockTGSender{
		nextMsgID: "msg-1",
	}
}

func (m *mockTGSender) SendMessage(_ context.Context, text string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCalls = append(m.sendCalls, text)
	if m.sendErr != nil {
		return "", m.sendErr
	}
	return m.nextMsgID, nil
}

func (m *mockTGSender) EditMessage(_ context.Context, messageID, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.editCalls = append(m.editCalls, tgEditCall{messageID: messageID, text: text})
	return m.editErr
}

func (m *mockTGSender) DeleteMessage(_ context.Context, messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls = append(m.deleteCalls, messageID)
	return m.deleteErr
}

func (m *mockTGSender) SendConfirmation(_ context.Context, text string) (bool, error) {
	return true, nil
}

func (m *mockTGSender) getSendCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sendCalls)
}

func (m *mockTGSender) getEditCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.editCalls)
}

func (m *mockTGSender) getDeleteCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.deleteCalls)
}

func makeProgressConfig(throttleSec int, deleteOnComplete, showStepText bool) *config.Config {
	cfg := &config.Config{}
	cfg.Health.Progress = config.ProgressConfig{
		Enabled:          true,
		ThrottleSec:      throttleSec,
		DeleteOnComplete: deleteOnComplete,
		ShowStepText:     showStepText,
	}
	return cfg
}

// Test 1: EventToolExecStart → SendMessage called.
func TestProgress_ToolExecStart_SendMessage(t *testing.T) {
	mock := newMockTGSender()
	cfg := makeProgressConfig(2, true, true)
	po := ProgressObserverFactory(cfg, mock)

	evt := agent.Event{
		Kind:    agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{Tool: "read_file"},
	}

	err := po.OnEvent(context.Background(), evt)
	require.NoError(t, err)

	assert.Equal(t, 1, mock.getSendCount())
	mock.mu.Lock()
	assert.Equal(t, "⏳ read_file...", mock.sendCalls[0])
	mock.mu.Unlock()
}

// Test 2: EventToolExecEnd → EditMessage called.
func TestProgress_ToolExecEnd_EditMessage(t *testing.T) {
	mock := newMockTGSender()
	// Use throttle=0 so events are not throttled.
	cfg := makeProgressConfig(0, true, true)
	po := ProgressObserverFactory(cfg, mock)

	// First: tool start → sends message, gets activeMessageID.
	startEvt := agent.Event{
		Kind:    agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{Tool: "exec"},
	}
	_ = po.OnEvent(context.Background(), startEvt)
	assert.Equal(t, 1, mock.getSendCount())

	// Reset throttle to allow second event.
	po.mu.Lock()
	po.lastSendAt = time.Time{}
	po.mu.Unlock()

	// Second: tool end → edits the active message.
	endEvt := agent.Event{
		Kind: agent.EventKindToolExecEnd,
		Payload: agent.ToolExecEndPayload{
			Tool:     "exec",
			Duration: 150 * time.Millisecond,
		},
	}
	err := po.OnEvent(context.Background(), endEvt)
	require.NoError(t, err)

	assert.Equal(t, 1, mock.getEditCount())
	mock.mu.Lock()
	assert.Equal(t, "msg-1", mock.editCalls[0].messageID)
	assert.Equal(t, "✅ exec (150ms)", mock.editCalls[0].text)
	mock.mu.Unlock()
}

// Test 3: EventTurnEnd + deleteOnDone → DeleteMessage called.
func TestProgress_TurnEnd_DeleteOnDone(t *testing.T) {
	mock := newMockTGSender()
	cfg := makeProgressConfig(0, true, true)
	po := ProgressObserverFactory(cfg, mock)

	// First: tool start → creates active message.
	startEvt := agent.Event{
		Kind:    agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{Tool: "list_dir"},
	}
	_ = po.OnEvent(context.Background(), startEvt)
	assert.Equal(t, 1, mock.getSendCount())

	// Turn end → delete message.
	turnEndEvt := agent.Event{
		Kind:    agent.EventKindTurnEnd,
		Payload: agent.TurnEndPayload{},
	}
	err := po.OnEvent(context.Background(), turnEndEvt)
	require.NoError(t, err)

	assert.Equal(t, 1, mock.getDeleteCount())
	mock.mu.Lock()
	assert.Equal(t, "msg-1", mock.deleteCalls[0])
	mock.mu.Unlock()

	// activeMessageID should be cleared.
	po.mu.Lock()
	assert.Empty(t, po.activeMessageID)
	po.mu.Unlock()
}

// Test 4: Throttle: 3 events in 1 sec (throttle_sec=2) → 1 send + 0 updates.
func TestProgress_Throttle_MultipleEventsSkipped(t *testing.T) {
	mock := newMockTGSender()
	cfg := makeProgressConfig(2, true, true)
	po := ProgressObserverFactory(cfg, mock)

	ctx := context.Background()

	// Event 1: should send.
	evt1 := agent.Event{
		Kind:    agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{Tool: "tool1"},
	}
	_ = po.OnEvent(ctx, evt1)
	assert.Equal(t, 1, mock.getSendCount())

	// Event 2: within throttle window → skipped.
	evt2 := agent.Event{
		Kind:    agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{Tool: "tool2"},
	}
	_ = po.OnEvent(ctx, evt2)

	// Event 3: still within throttle window → skipped.
	evt3 := agent.Event{
		Kind:    agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{Tool: "tool3"},
	}
	_ = po.OnEvent(ctx, evt3)

	// Only 1 send, 0 edits.
	assert.Equal(t, 1, mock.getSendCount())
	assert.Equal(t, 0, mock.getEditCount())
}

// Test 5: NotifyDegradation → SendMessage called.
func TestProgress_NotifyDegradation_SendMessage(t *testing.T) {
	mock := newMockTGSender()
	cfg := makeProgressConfig(2, true, true)
	po := ProgressObserverFactory(cfg, mock)

	po.NotifyDegradation("llm_provider", "degraded")

	assert.Equal(t, 1, mock.getSendCount())
	mock.mu.Lock()
	assert.Contains(t, mock.sendCalls[0], "llm_provider")
	assert.Contains(t, mock.sendCalls[0], "деградирован")
	mock.mu.Unlock()
}

// Test 6: NotifyDegradation second time <5 min → throttled.
func TestProgress_NotifyDegradation_ThrottledWithin5Min(t *testing.T) {
	mock := newMockTGSender()
	cfg := makeProgressConfig(2, true, true)
	po := ProgressObserverFactory(cfg, mock)

	po.NotifyDegradation("llm_provider", "degraded")
	assert.Equal(t, 1, mock.getSendCount())

	// Second call within 5 min → throttled.
	po.NotifyDegradation("llm_provider", "offline")
	assert.Equal(t, 1, mock.getSendCount()) // still 1

	// Different component is NOT throttled.
	po.NotifyDegradation("tool_executor", "degraded")
	assert.Equal(t, 2, mock.getSendCount())
}

// Test 7: NotifyRecovery → SendMessage called (no throttle).
func TestProgress_NotifyRecovery_NoThrottle(t *testing.T) {
	mock := newMockTGSender()
	cfg := makeProgressConfig(2, true, true)
	po := ProgressObserverFactory(cfg, mock)

	po.NotifyRecovery("llm_provider")

	assert.Equal(t, 1, mock.getSendCount())
	mock.mu.Lock()
	assert.Contains(t, mock.sendCalls[0], "llm_provider")
	assert.Contains(t, mock.sendCalls[0], "восстановлен")
	mock.mu.Unlock()
}

// Test 8: Sender error → log warning, no panic.
func TestProgress_SenderError_NoPanic(t *testing.T) {
	mock := newMockTGSender()
	mock.sendErr = errors.New("telegram API error")
	cfg := makeProgressConfig(0, true, true)
	po := ProgressObserverFactory(cfg, mock)

	// Should not panic on sender error.
	evt := agent.Event{
		Kind:    agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{Tool: "read_file"},
	}
	err := po.OnEvent(context.Background(), evt)
	assert.NoError(t, err)

	// NotifyDegradation should not panic either.
	assert.NotPanics(t, func() {
		po.NotifyDegradation("comp", "degraded")
	})

	// NotifyRecovery should not panic.
	assert.NotPanics(t, func() {
		po.NotifyRecovery("comp")
	})
}
