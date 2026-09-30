package mcp

import (
	"context"
	"errors"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Волна 125 (срез А, бой 30 сен): stdio-сервер держит токен в Env
// (GITHUB_PERSONAL_ACCESS_TOKEN). 401 → менеджер обязан реконнектить
// со свежим env из рефрешера и повторить вызов. До фикса ветка смотрела
// только Headers — для stdio не срабатывала никогда.
func TestCallTool_AuthError_ReconnectsOnEnvChange(t *testing.T) {
	stale, _, err := newScriptedServerConnection(
		"stale", nil, errors.New("401 Bad credentials"))
	if err != nil {
		t.Fatal(err)
	}
	stale.Name = "github"
	stale.Config = config.MCPServerConfig{
		Enabled: true, Type: "stdio", Command: "github-mcp-server",
		Env: map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "old"},
	}

	mgr := NewManager()
	mgr.servers["github"] = stale

	orig := connectServerFunc
	defer func() { connectServerFunc = orig }()
	connectServerFunc = func(
		ctx context.Context, name string, cfg config.MCPServerConfig,
	) (*ServerConnection, error) {
		if got := cfg.Env["GITHUB_PERSONAL_ACCESS_TOKEN"]; got != "new" {
			t.Errorf("reconnect env token = %q, want refreshed", got)
		}
		fresh, _, connErr := newScriptedServerConnection("fresh",
			&sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}},
			}, nil)
		if connErr != nil {
			return nil, connErr
		}
		fresh.Config = cfg
		return fresh, nil
	}

	mgr.SetServerConfigRefresher(func(
		serverName string, current config.MCPServerConfig,
	) (config.MCPServerConfig, error) {
		current.Env = map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "new"}
		return current, nil
	})

	res, err := mgr.CallTool(context.Background(), "github", "echo", nil)
	if err != nil {
		t.Fatalf("CallTool after env refresh: %v", err)
	}
	if res == nil {
		t.Fatal("nil result after env refresh")
	}
}

// Тот же env → реконнекта нет (поведение «тот же Bearer» волны 121).
func TestCallTool_AuthError_SameEnvNoReconnect(t *testing.T) {
	stale, _, err := newScriptedServerConnection(
		"stale", nil, errors.New("401 Bad credentials"))
	if err != nil {
		t.Fatal(err)
	}
	stale.Name = "github"
	stale.Config = config.MCPServerConfig{
		Enabled: true, Type: "stdio", Command: "github-mcp-server",
		Env: map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": "same"},
	}

	mgr := NewManager()
	mgr.servers["github"] = stale

	orig := connectServerFunc
	defer func() { connectServerFunc = orig }()
	connectCalls := 0
	connectServerFunc = func(
		ctx context.Context, name string, cfg config.MCPServerConfig,
	) (*ServerConnection, error) {
		connectCalls++
		return nil, errors.New("must not be called")
	}
	mgr.SetServerConfigRefresher(func(
		serverName string, current config.MCPServerConfig,
	) (config.MCPServerConfig, error) {
		return current, nil // тот же env
	})

	_, err = mgr.CallTool(context.Background(), "github", "echo", nil)
	if err == nil {
		t.Fatal("expected error to propagate when env unchanged")
	}
	if connectCalls != 0 {
		t.Fatalf("connectCalls = %d, want 0 (env не изменился)", connectCalls)
	}
}
