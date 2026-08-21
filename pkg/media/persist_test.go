package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistInboundFile_WritesAndDedupes(t *testing.T) {
	dir := t.TempDir()
	data := []byte("hello world")

	p1, err := PersistInboundFile(dir, "photo.png", "image/png", data)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if !strings.Contains(p1, filepath.Join("files", "")) {
		t.Fatalf("path %q not under files/", p1)
	}
	info, err := os.Stat(p1)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600 (no exec bit)", info.Mode().Perm())
	}

	p2, err := PersistInboundFile(dir, "photo.png", "image/png", data)
	if err != nil {
		t.Fatalf("persist dup: %v", err)
	}
	if p1 != p2 {
		t.Fatalf("dedup miss: %q != %q", p1, p2)
	}
}

func TestPersistInboundFile_NoExecTraversal(t *testing.T) {
	dir := t.TempDir()
	p, err := PersistInboundFile(dir, "../../evil.sh", "application/octet-stream", []byte("x"))
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if strings.Contains(p, "..") {
		t.Fatalf("traversal survived: %q", p)
	}
	if _, err := PersistInboundFile("", "a.txt", "text/plain", []byte("x")); err == nil {
		t.Fatal("empty workspace must fail")
	}
	if _, err := PersistInboundFile(dir, "a.txt", "text/plain", nil); err == nil {
		t.Fatal("empty payload must fail")
	}
}
