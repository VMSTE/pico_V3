package tools

import (
	"context"
	"strings"
	"testing"
)

// Волна 108 (ТЗ-108): рекордер на трубе + страж .vault.

type fakeArtifactTool struct {
	name     string
	fail     bool
	executed *bool
}

func (f *fakeArtifactTool) Name() string        { return f.name }
func (f *fakeArtifactTool) Description() string { return "fake" }
func (f *fakeArtifactTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}

func (f *fakeArtifactTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	*f.executed = true
	if f.fail {
		return ErrorResult("boom")
	}
	return SilentResult("ok")
}

type fakeArtifactRecorder struct{ calls []string }

func (f *fakeArtifactRecorder) Record(
	_ context.Context, toolName string, args map[string]any,
) {
	p, _ := args["path"].(string)
	f.calls = append(f.calls, toolName+":"+p)
}

func TestArtifactRecorder_CalledOnMutatingSuccess(t *testing.T) {
	executed := false
	rec := &fakeArtifactRecorder{}
	r := NewToolRegistry()
	r.SetArtifactRecorder(rec)
	r.Register(&fakeArtifactTool{name: "write_file", executed: &executed})

	res := r.Execute(context.Background(), "write_file",
		map[string]any{"path": "docs/a.txt"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if len(rec.calls) != 1 || rec.calls[0] != "write_file:docs/a.txt" {
		t.Fatalf("recorder calls = %v, want [write_file:docs/a.txt]", rec.calls)
	}
}

func TestArtifactRecorder_NotCalledOnReadOrError(t *testing.T) {
	executed := false
	rec := &fakeArtifactRecorder{}
	r := NewToolRegistry()
	r.SetArtifactRecorder(rec)
	r.Register(&fakeArtifactTool{name: "read_file", executed: &executed})
	r.Register(&fakeArtifactTool{name: "write_file", fail: true, executed: &executed})

	r.Execute(context.Background(), "read_file", map[string]any{"path": "x"})
	r.Execute(context.Background(), "write_file", map[string]any{"path": "x"})
	if len(rec.calls) != 0 {
		t.Fatalf("recorder calls = %v, want none (read + failed write)", rec.calls)
	}
}

func TestVaultWriteBlockedAtFunnel(t *testing.T) {
	executed := false
	r := NewToolRegistry()
	r.Register(&fakeArtifactTool{name: "write_file", executed: &executed})

	res := r.Execute(context.Background(), "write_file",
		map[string]any{"path": ".vault/store/x"})
	if !res.IsError {
		t.Fatal("write into .vault must be blocked")
	}
	if !strings.Contains(res.ForLLM, ".vault") {
		t.Errorf("error must mention .vault: %s", res.ForLLM)
	}
	if executed {
		t.Error("tool must NOT execute for a vault path")
	}
}

func TestVaultReadAllowed(t *testing.T) {
	executed := false
	r := NewToolRegistry()
	r.Register(&fakeArtifactTool{name: "read_file", executed: &executed})

	res := r.Execute(context.Background(), "read_file",
		map[string]any{"path": ".vault/store/x"})
	if res.IsError {
		t.Fatalf("read from .vault must be allowed (diagnostics): %s", res.ForLLM)
	}
	if !executed {
		t.Error("read tool should have executed")
	}
}
