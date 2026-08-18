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
)

var sourceUpdateMu sync.Mutex

type sourceUpdateResponse struct {
	Status                  string `json:"status"`
	Message                 string `json:"message,omitempty"`
	Log                     string `json:"log,omitempty"`
	LauncherRestartRequired bool   `json:"launcher_restart_required"`
}

// registerSourceUpdateRoutes registers self-update from a git checkout
// (D-AUDIT-105). Behind dashboard auth like every other API route.
func (h *Handler) registerSourceUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/update-from-source", h.handleUpdateFromSource)
}

// handleUpdateFromSource pulls main, rebuilds core + launcher (and the
// frontend bundle when web/frontend changed), then restarts the gateway.
// The running launcher process cannot restart itself — when its own code
// changed we report launcher_restart_required so the UI can ask the user.
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
	frontendChanged := hasChangedPrefix(changed, "web/frontend/")

	if frontendChanged && !run("make", "build-frontend") {
		fail("frontend build failed")
		return
	}
	if !run("make", "build") {
		fail("core build failed")
		return
	}
	if !run("make", "build-launcher") {
		fail("launcher build failed")
		return
	}

	gwMsg := "gateway was not running; start it from the header"
	if pid, rErr := h.RestartGateway(); rErr == nil {
		gwMsg = fmt.Sprintf("gateway restarted (pid %d)", pid)
	} else {
		gwMsg = "gateway restart skipped: " + rErr.Error()
	}
	fmt.Fprintf(&log, "%s\n", gwMsg)

	_ = json.NewEncoder(w).Encode(sourceUpdateResponse{
		Status:                  "ok",
		Message:                 gwMsg,
		Log:                     tailLog(log.String(), 4000),
		LauncherRestartRequired: webChanged,
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
