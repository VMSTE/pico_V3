package pico

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestHandleMessageSend_PersistsInlineImage(t *testing.T) {
	mb := bus.NewMessageBus()
	bc := &config.Channel{Type: "pico", Enabled: true}
	ch, err := NewPicoChannel(bc, &config.PicoSettings{
		Token: *config.NewSecureString("test-token"),
	}, mb)
	if err != nil {
		t.Fatalf("NewPicoChannel() error = %v", err)
	}
	ws := t.TempDir()
	ch.SetWorkspace(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(ctx)

	pc := &picoConn{id: "conn-1", sessionID: "sess-1"}
	ch.handleMessageSend(pc, PicoMessage{
		ID: "msg-1",
		Payload: map[string]any{
			"media": []any{
				"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII=",
			},
		},
	})

	select {
	case msg := <-mb.InboundChan():
		if !strings.Contains(msg.Content, "[image: ") {
			t.Fatalf("msg.Content = %q, want path tag", msg.Content)
		}
		if len(msg.Media) != 1 || !strings.HasPrefix(msg.Media[0], "data:image/png;base64,") {
			t.Fatalf("msg.Media = %#v, want original data URL kept for vision", msg.Media)
		}
		files, err := filepath.Glob(filepath.Join(ws, "files", "*", "*.png"))
		if err != nil || len(files) != 1 {
			t.Fatalf("persisted files = %v, err = %v", files, err)
		}
		info, err := os.Stat(files[0])
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for inbound message")
	}
}

func TestHandleMessageSend_PersistsAnyFileAttachment(t *testing.T) {
	mb := bus.NewMessageBus()
	bc := &config.Channel{Type: "pico", Enabled: true}
	ch, err := NewPicoChannel(bc, &config.PicoSettings{
		Token: *config.NewSecureString("test-token"),
	}, mb)
	if err != nil {
		t.Fatalf("NewPicoChannel() error = %v", err)
	}
	ws := t.TempDir()
	ch.SetWorkspace(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ch.Stop(ctx)

	pc := &picoConn{id: "conn-1", sessionID: "sess-1"}
	ch.handleMessageSend(pc, PicoMessage{
		ID: "msg-1",
		Payload: map[string]any{
			"content": "вот документ",
			"attachments": []any{
				map[string]any{
					"filename":     "spec.pdf",
					"content_type": "application/pdf",
					"data":         "data:application/pdf;base64,JVBERi0xLjQ=",
				},
			},
		},
	})

	select {
	case msg := <-mb.InboundChan():
		if !strings.Contains(msg.Content, "[file: spec.pdf -> ") {
			t.Fatalf("msg.Content = %q, want file tag with path", msg.Content)
		}
		files, err := filepath.Glob(filepath.Join(ws, "files", "*", "*-spec.pdf"))
		if err != nil || len(files) != 1 {
			t.Fatalf("persisted files = %v, err = %v", files, err)
		}
		info, _ := os.Stat(files[0])
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for inbound message")
	}
}
