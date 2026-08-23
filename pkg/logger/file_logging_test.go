package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupWorkspaceLogging(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	prevLevel := GetLevel()
	prevMax := wsLogMaxSize
	if err := EnableWorkspaceFileLogging(ws); err != nil {
		t.Fatalf("EnableWorkspaceFileLogging: %v", err)
	}
	t.Cleanup(func() {
		DisableWorkspaceFileLogging()
		SetLevel(prevLevel)
		wsLogMaxSize = prevMax
	})
	return ws
}

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// Ключевой инвариант волны 106: файл пишет ВСЁ, даже когда консольный уровень
// задран до WARN (дефолт gateway.log_level) ПОСЛЕ включения — как gateway.Run.
func TestWorkspaceFileLogging_FileGetsFullStream(t *testing.T) {
	ws := setupWorkspaceLogging(t)

	SetLevel(WARN) // симулируем применение конфига после включения

	Debug("ws106-debug-line")
	Info("ws106-info-line")
	Warn("ws106-warn-line")

	gatewayLog := readLogFile(t, filepath.Join(ws, "logs", "gateway.log"))
	for _, want := range []string{"ws106-debug-line", "ws106-info-line", "ws106-warn-line"} {
		if !strings.Contains(gatewayLog, want) {
			t.Errorf("gateway.log missing %q (file must get the full stream)", want)
		}
	}

	errorsLog := readLogFile(t, filepath.Join(ws, "logs", "errors.log"))
	if !strings.Contains(errorsLog, "ws106-warn-line") {
		t.Error("errors.log missing warn line")
	}
	if strings.Contains(errorsLog, "ws106-info-line") ||
		strings.Contains(errorsLog, "ws106-debug-line") {
		t.Error("errors.log must not contain INFO/DEBUG")
	}
}

func TestWorkspaceFileLogging_RotatesBySize(t *testing.T) {
	ws := setupWorkspaceLogging(t)
	wsLogMaxSize = 512

	payload := strings.Repeat("x", 400)
	for i := 0; i < 5; i++ {
		Info("ws106-rotate-" + payload)
	}

	if _, err := os.Stat(filepath.Join(ws, "logs", "gateway.log.1")); err != nil {
		t.Fatalf("expected rotated gateway.log.1: %v", err)
	}
}

func TestWorkspaceFileLogging_MasksSecrets(t *testing.T) {
	ws := setupWorkspaceLogging(t)
	Info("token: bot123456:AAAAbbbbCCCCddddEEEEffff1234")

	gatewayLog := readLogFile(t, filepath.Join(ws, "logs", "gateway.log"))
	if strings.Contains(gatewayLog, "AAAAbbbbCCCCddddEEEEffff1234") {
		t.Error("bot token leaked to file log")
	}
	if !strings.Contains(gatewayLog, "****") {
		t.Error("expected masked token in file log")
	}
}

func TestWorkspaceFileLogging_DisableStopsWrites(t *testing.T) {
	ws := setupWorkspaceLogging(t)
	Info("ws106-before-disable")
	DisableWorkspaceFileLogging()
	Info("ws106-after-disable")

	gatewayLog := readLogFile(t, filepath.Join(ws, "logs", "gateway.log"))
	if !strings.Contains(gatewayLog, "ws106-before-disable") {
		t.Error("pre-disable line missing")
	}
	if strings.Contains(gatewayLog, "ws106-after-disable") {
		t.Error("write landed after disable")
	}
}
