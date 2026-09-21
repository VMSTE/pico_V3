package tools

import (
	"context"
	"testing"
)

// Волна 109 (ТЗ-109): машина времени на трубе — чекпоинт ДО мутации.

type fakeCheckpointTaker struct{ calls []string }

func (f *fakeCheckpointTaker) BeforeMutation(toolName string, _ map[string]any) {
	f.calls = append(f.calls, toolName)
}

func TestCheckpointTaker_FiresOnMutations(t *testing.T) {
	executed := false
	taker := &fakeCheckpointTaker{}
	r := NewToolRegistry()
	r.SetCheckpointTaker(taker)
	r.Register(&fakeArtifactTool{name: "write_file", executed: &executed})
	r.Register(&fakeArtifactTool{name: "read_file", executed: &executed})
	r.Register(&fakeArtifactTool{name: "exec", executed: &executed})

	r.Execute(context.Background(), "write_file", map[string]any{"path": "a"})
	r.Execute(context.Background(), "read_file", map[string]any{"path": "a"})
	r.Execute(context.Background(), "exec", map[string]any{"command": "ls -la"})
	r.Execute(context.Background(), "exec", map[string]any{"command": "rm -rf x"})
	r.Execute(context.Background(), "exec", map[string]any{"command": "echo hi > f.txt"})

	want := []string{"write_file", "exec", "exec"}
	if len(taker.calls) != len(want) {
		t.Fatalf("taker calls = %v, want %v", taker.calls, want)
	}
	for i := range want {
		if taker.calls[i] != want[i] {
			t.Fatalf("taker calls = %v, want %v", taker.calls, want)
		}
	}
}

func TestExecDestructive_Detection(t *testing.T) {
	for _, c := range []string{
		"rm x", "mv a b", "sed -i s/a/b/ f", "echo x > f", "echo x >> f",
		"git reset --hard", "git checkout .", "dd if=/dev/zero of=f",
	} {
		if !execDestructive("exec", map[string]any{"command": c}) {
			t.Errorf("must be destructive: %q", c)
		}
	}
	for _, c := range []string{"ls", "cat f", "git status", "echo hello"} {
		if execDestructive("exec", map[string]any{"command": c}) {
			t.Errorf("must NOT be destructive: %q", c)
		}
	}
	if execDestructive("write_file", map[string]any{"command": "rm x"}) {
		t.Error("non-exec tool must not match")
	}
}
