package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Волна 125 (срез Б): сервер без вызовов дольше idle_timeout уходит
// спать (тулы в кэше, статус idle, не «упал»), CallTool будит со
// свежим конфигом из рефрешера.
func TestReapIdleAndWakeWithFreshConfig(t *testing.T) {
	okRes := &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}},
	}
	conn, _, err := newScriptedServerConnection("s1", okRes, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.Name = "github"
	conn.Config = config.MCPServerConfig{
		Enabled: true, Type: "stdio", Command: "x",
		Env: map[string]string{"T": "old"},
	}
	conn.connectedAt = time.Now().Add(-20 * time.Minute)

	mgr := NewManager()
	mgr.servers["github"] = conn

	// Ни одного вызова, 20 минут с поднятия > 15 мин дефолта → реап.
	mgr.reapIdle(time.Now())
	if _, ok := mgr.servers["github"]; ok {
		t.Fatal("server still in servers after reap")
	}
	if _, ok := mgr.sleeping["github"]; !ok {
		t.Fatal("server not in sleeping after reap")
	}
	st := mgr.ServerStatuses()
	if len(st) != 1 || st[0].State != "idle" || !st[0].Connected {
		t.Fatalf("status after reap = %+v, want idle+connected", st)
	}

	// Пробуждение: конфиг обязан пройти через рефрешер (свежий токен).
	orig := connectServerFunc
	defer func() { connectServerFunc = orig }()
	connectCalls := 0
	connectServerFunc = func(
		ctx context.Context, name string, cfg config.MCPServerConfig,
	) (*ServerConnection, error) {
		connectCalls++
		if got := cfg.Env["T"]; got != "new" {
			t.Errorf("wake env = %q, want refreshed", got)
		}
		fresh, _, cerr := newScriptedServerConnection("s2", okRes, nil)
		if cerr != nil {
			return nil, cerr
		}
		fresh.Config = cfg
		return fresh, nil
	}
	mgr.SetServerConfigRefresher(func(
		name string, cur config.MCPServerConfig,
	) (config.MCPServerConfig, error) {
		cur.Env = map[string]string{"T": "new"}
		return cur, nil
	})

	res, err := mgr.CallTool(context.Background(), "github", "echo", nil)
	if err != nil {
		t.Fatalf("CallTool on sleeping server: %v", err)
	}
	if res == nil {
		t.Fatal("nil result after wake")
	}
	if connectCalls != 1 {
		t.Fatalf("connectCalls = %d, want 1", connectCalls)
	}
	if _, ok := mgr.sleeping["github"]; ok {
		t.Fatal("server still sleeping after wake")
	}
	st = mgr.ServerStatuses()
	if st[0].State == "idle" || !st[0].Connected {
		t.Fatalf("status after wake = %+v, want connected", st)
	}

	// Повторный вызов — по живому коннекту, без нового спавна.
	if _, err := mgr.CallTool(context.Background(), "github", "echo", nil); err != nil {
		t.Fatal(err)
	}
	if connectCalls != 1 {
		t.Fatalf("connectCalls = %d after second call, want 1", connectCalls)
	}
}

// Волна 125 (срез В): фейл пробуждения → бэкофф с честной ошибкой,
// без долбёжки; после истечения окна — снова можно.
func TestWakeBackoff(t *testing.T) {
	conn, _, err := newScriptedServerConnection("s1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.Name = "github"
	conn.Config = config.MCPServerConfig{Enabled: true, Type: "stdio", Command: "x"}

	mgr := NewManager()
	mgr.sleeping["github"] = conn

	orig := connectServerFunc
	defer func() { connectServerFunc = orig }()
	connectCalls := 0
	connectServerFunc = func(
		ctx context.Context, name string, cfg config.MCPServerConfig,
	) (*ServerConnection, error) {
		connectCalls++
		return nil, errors.New("spawn failed")
	}

	_, err = mgr.CallTool(context.Background(), "github", "echo", nil)
	if err == nil || !strings.Contains(err.Error(), "wake") {
		t.Fatalf("first wake err = %v", err)
	}
	if connectCalls != 1 {
		t.Fatalf("connectCalls = %d, want 1", connectCalls)
	}

	// Немедленный повтор — бэкофф, без новой попытки.
	_, err = mgr.CallTool(context.Background(), "github", "echo", nil)
	if err == nil || !strings.Contains(err.Error(), "восстанавливается") {
		t.Fatalf("second call err = %v, want backoff message", err)
	}
	if connectCalls != 1 {
		t.Fatalf("connectCalls = %d under backoff, want 1", connectCalls)
	}

	// Окно истекло — попытка снова разрешена.
	mgr.backoffMu.Lock()
	mgr.nextRetryAt["github"] = time.Now().Add(-time.Second)
	mgr.backoffMu.Unlock()
	_, _ = mgr.CallTool(context.Background(), "github", "echo", nil)
	if connectCalls != 2 {
		t.Fatalf("connectCalls = %d after backoff expired, want 2", connectCalls)
	}
}

// Волна 125 (срез В): классификация auth-ошибок += 403 / bad credentials.
func TestIsAuthCallError_Extended(t *testing.T) {
	for _, msg := range []string{
		"401 Bad credentials", "403 Forbidden", "unauthorized", "invalid_token",
	} {
		if !isAuthCallError(errors.New(msg)) {
			t.Errorf("isAuthCallError(%q) = false, want true", msg)
		}
	}
	if isAuthCallError(errors.New("boom")) {
		t.Error("boom is not auth")
	}
}
