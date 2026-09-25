package mcp

// Волна 121 (срез Б): живые статусы серверов (failed тоже записывается).

import (
	"context"
	"errors"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestServerStatuses_FailureThenSuccess(t *testing.T) {
	orig := connectServerFunc
	defer func() { connectServerFunc = orig }()
	calls := 0
	connectServerFunc = func(ctx context.Context, name string, cfg config.MCPServerConfig) (*ServerConnection, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("Unauthorized")
		}
		fresh, _, err := newScriptedServerConnection("ok", nil, nil)
		if err != nil {
			return nil, err
		}
		fresh.Name = name
		fresh.Config = cfg
		return fresh, nil
	}

	mgr := NewManager()
	cfg := config.MCPServerConfig{Enabled: true, Type: "http", URL: "https://example.invalid/mcp"}

	if err := mgr.ConnectServer(context.Background(), "notion", cfg); err == nil {
		t.Fatal("first connect must fail")
	}
	st := mgr.ServerStatuses()
	if len(st) != 1 || st[0].Name != "notion" || st[0].Connected {
		t.Fatalf("after failure: %+v", st)
	}
	if st[0].LastError != "Unauthorized" {
		t.Errorf("LastError = %q, want Unauthorized", st[0].LastError)
	}

	if err := mgr.ConnectServer(context.Background(), "notion", cfg); err != nil {
		t.Fatalf("second connect: %v", err)
	}
	st = mgr.ServerStatuses()
	if len(st) != 1 || !st[0].Connected || st[0].LastError != "" {
		t.Fatalf("after success: %+v", st)
	}
	if st[0].Tools != 1 {
		t.Errorf("Tools = %d, want 1 (echo из scripted conn)", st[0].Tools)
	}
}
