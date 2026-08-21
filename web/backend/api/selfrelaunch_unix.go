//go:build !windows

package api

import (
	"log"
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
	// Волна 96 (бой 21 авг 01:35): после кнопки «Обновить из main»
	// relaunch молча не случился — оба пути ошибки были глухими,
	// и root cause было не увидеть. Логируем ДО exec: после успешного
	// exec старый процесс перестаёт существовать, писать будет некому.
	exe, err := os.Executable()
	if err != nil {
		log.Printf("WARN source-update: self-relaunch: executable path: %v", err)
		return
	}
	log.Printf("INFO source-update: self-relaunch: exec into %s", exe)
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		log.Printf("ERROR source-update: self-relaunch exec failed: %v", err)
	}
}
