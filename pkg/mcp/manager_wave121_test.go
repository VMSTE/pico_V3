package mcp

// Волна 121 (срез А): 401-контур — освежение конфига сервера перед reconnect.

import (
	"context"
	"errors"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestIsAuthCallError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"session missing is not auth", sdkmcp.ErrSessionMissing, false},
		{"unauthorized", errors.New(`calling "initialize": Unauthorized`), true},
		{"401", errors.New("POST https://mcp.notion.com/mcp: 401"), true},
		{"invalid_token", errors.New("invalid_token: token expired"), true},
		{"other", errors.New("connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAuthCallError(tc.err); got != tc.want {
				t.Errorf("isAuthCallError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestCallTool_AuthError_ReconnectsWithRefreshedConfig(t *testing.T) {
	stale, _, err := newScriptedServerConnection("stale", nil, errors.New("401 Unauthorized"))
	if err != nil {
		t.Fatal(err)
	}
	stale.Name = "notion"
	stale.Config.Headers = map[string]string{"Authorization": "Bearer old"}

	mgr := NewManager()
	mgr.servers["notion"] = stale

	orig := connectServerFunc
	defer func() { connectServerFunc = orig }()
	connectServerFunc = func(ctx context.Context, name string, cfg config.MCPServerConfig) (*ServerConnection, error) {
		if got := cfg.Headers["Authorization"]; got != "Bearer new" {
			t.Errorf("reconnect Authorization = %q, want refreshed", got)
		}
		fresh, _, connErr := newScriptedServerConnection("fresh", &sdkmcp.CallToolResult{
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
		current.Headers = map[string]string{"Authorization": "Bearer new"}
		return current, nil
	})

	res, err := mgr.CallTool(context.Background(), "notion", "echo", nil)
	if err != nil {
		t.Fatalf("CallTool after auth refresh: %v", err)
	}
	if res == nil {
		t.Fatal("nil result after auth refresh")
	}
}

func TestCallTool_AuthError_SameTokenNoReconnect(t *testing.T) {
	stale, _, err := newScriptedServerConnection("stale", nil, errors.New("401 Unauthorized"))
	if err != nil {
		t.Fatal(err)
	}
	stale.Name = "notion"
	stale.Config.Headers = map[string]string{"Authorization": "Bearer old"}

	mgr := NewManager()
	mgr.servers["notion"] = stale

	connectCalled := false
	orig := connectServerFunc
	defer func() { connectServerFunc = orig }()
	connectServerFunc = func(ctx context.Context, name string, cfg config.MCPServerConfig) (*ServerConnection, error) {
		connectCalled = true
		return nil, errors.New("must not be called")
	}

	mgr.SetServerConfigRefresher(func(
		serverName string, current config.MCPServerConfig,
	) (config.MCPServerConfig, error) {
		return current, nil // токен не изменился
	})

	_, err = mgr.CallTool(context.Background(), "notion", "echo", nil)
	if err == nil {
		t.Fatal("expected error to surface unchanged")
	}
	if connectCalled {
		t.Error("reconnect must not run when Bearer is unchanged")
	}
}
