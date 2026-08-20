package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var sourceUpdateMu sync.Mutex

// selfRelaunchFn is indirected so tests can stub process re-exec.
var selfRelaunchFn = selfRelaunch

type sourceUpdateResponse struct {
	Status                  string `json:"status"`
	Message                 string `json:"message,omitempty"`
	Log                     string `json:"log,omitempty"`
	LauncherRestartRequired bool   `json:"launcher_restart_required"`
	Relaunching             bool   `json:"relaunching,omitempty"`
}

// registerSourceUpdateRoutes registers self-update from a git checkout
// (D-AUDIT-105). Behind dashboard auth like every other API route.
func (h *Handler) registerSourceUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/update-from-source", h.handleUpdateFromSource)
}

// handleUpdateFromSource pulls main, rebuilds core + launcher (and the
// frontend bundle when web/frontend changed), then restarts the gateway.
// После успешного обновления лаунчер ВСЕГДА re-exec'ится в свежий бинарь
// (D-AUDIT-110, POSIX; волна 95 — не только при изменениях web/, см. ниже);
// on Windows we report launcher_restart_required for a manual restart.
func (h *Handler) handleUpdateFromSource(
	w http.ResponseWriter, r *http.Request,
) {
	if !sourceUpdateMu.TryLock() {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(sourceUpdateResponse{
			Status: "error", Message: "update already running",
		})
		return
	}
	defer sourceUpdateMu.Unlock()

	repo, err := findSourceRepoRoot()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(sourceUpdateResponse{
			Status: "error", Message: err.Error(),
		})
		return
	}

	var log strings.Builder
	run := func(name string, args ...string) bool {
		fmt.Fprintf(&log, "$ %s %s\n", name, strings.Join(args, " "))
		cmd := exec.Command(name, args...)
		cmd.Dir = repo
		out, runErr := cmd.CombinedOutput()
		log.Write(out)
		if runErr != nil {
			fmt.Fprintf(&log, "FAILED: %v\n", runErr)
			return false
		}
		return true
	}
	fail := func(msg string) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(sourceUpdateResponse{
			Status: "error", Message: msg, Log: tailLog(log.String(), 4000),
		})
	}

	beforeBytes, _ := exec.Command(
		"git", "-C", repo, "rev-parse", "HEAD",
	).Output()
	before := strings.TrimSpace(string(beforeBytes))

	if !run("git", "pull", "--ff-only") {
		fail("git pull failed (local changes or non-ff); resolve in terminal")
		return
	}

	changed := ""
	if afterBytes, aErr := exec.Command(
		"git", "-C", repo, "rev-parse", "HEAD",
	).Output(); aErr == nil && before != "" {
		if after := strings.TrimSpace(string(afterBytes)); after != before {
			diff, _ := exec.Command(
				"git", "-C", repo, "diff", "--name-only", before, after,
			).Output()
			changed = string(diff)
		}
	}
	webChanged := hasChangedPrefix(changed, "web/")

	// Отдельного шага фронта нет: make build-launcher сам пересобирает
	// встраиваемый фронт (vite). Цель build-frontend живёт только в
	// web/Makefile и из корня недоступна — бой 18 авг (500 на кнопке).
	if !run("make", "build") {
		fail("core build failed")
		return
	}
	if !run("make", "build-launcher") {
		fail("launcher build failed")
		return
	}

	var gwMsg string
	if pid, rErr := h.RestartGateway(); rErr == nil {
		gwMsg = fmt.Sprintf("gateway restarted (pid %d)", pid)
	} else {
		gwMsg = "gateway restart skipped: " + rErr.Error()
	}
	fmt.Fprintf(&log, "%s\n", gwMsg)

	fmt.Fprintf(&log, "changed files since %s:\n%s\nweb changed: %v\n",
		before, changed, webChanged)

	relaunching := false
	if selfRelaunchSupported() {
		// Волна 95 (бой 21 авг 01:05): relaunch ВСЕГДА, не только при
		// изменениях web/. Иначе после core-only обновления процесс
		// лаунчера оставался старым → чип версии ложно краснел (ядро на
		// диске новое ≠ процесс лаунчера старый → stale). Кнопка
		// «Обновить из main» по ожиданию founder'а = полный рестарт.
		// D-AUDIT-110: re-exec via exec. The delay lets this HTTP response
		// reach the UI first; the new process image keeps the same
		// PID/terminal and attaches to the gateway restarted above.
		relaunching = true
		go func() {
			time.Sleep(1500 * time.Millisecond)
			selfRelaunchFn()
		}()
	}

	_ = json.NewEncoder(w).Encode(sourceUpdateResponse{
		Status:                  "ok",
		Message:                 gwMsg,
		Log:                     tailLog(log.String(), 4000),
		LauncherRestartRequired: !relaunching,
		Relaunching:             relaunching,
	})
}

// findSourceRepoRoot walks up from the running executable looking for a
// directory that has both Makefile and .git — i.e. a source checkout.
func findSourceRepoRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	for range 6 {
		mk, mkErr := os.Stat(filepath.Join(dir, "Makefile"))
		gi, giErr := os.Stat(filepath.Join(dir, ".git"))
		if mkErr == nil && !mk.IsDir() && giErr == nil && gi.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf(
		"source checkout (Makefile + .git) not found above %s; "+
			"source update is only for git installs", exe,
	)
}

func hasChangedPrefix(changed, prefix string) bool {
	for line := range strings.Lines(changed) {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}

func tailLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return "…\n" + s[len(s)-maxLen:]
}
