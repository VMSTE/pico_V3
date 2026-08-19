//go:build windows

package api

// Windows has no execve: the UI falls back to launcher_restart_required
// (manual restart), so self-relaunch reports unsupported here.
func selfRelaunchSupported() bool { return false }

func selfRelaunch() {}
