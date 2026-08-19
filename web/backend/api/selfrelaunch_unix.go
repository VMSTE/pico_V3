//go:build !windows

package api

import (
	"os"
	"syscall"
)

// selfRelaunchSupported reports whether in-place self-restart is available
// (POSIX only — Windows has no execve semantics).
func selfRelaunchSupported() bool { return true }

// selfRelaunch replaces the running launcher process image with the freshly
// built binary (D-AUDIT-110). POSIX allows exec-ing a path whose file was
// replaced while running: the new process keeps the same PID and terminal,
// then attaches to the already-restarted gateway via its PID file.
func selfRelaunch() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = syscall.Exec(exe, os.Args, os.Environ())
}
