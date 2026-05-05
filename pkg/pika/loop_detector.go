// PIKA-V3: loop_detector.go — Loop detection safety net (D-136a).
// Safety net: cannot be disabled via config. Checks TRAIL ring
// buffer for consecutive identical tool calls.
// Called directly from pipeline, NOT as a hook.

package pika

// CheckLoopDetection checks the last N entries in TRAIL.
// If N consecutive entries have identical tool call hashes
// then a loop is detected.
// threshold is typically cfg.LoopDetectionThreshold (default 3).
//
// This is a safety net (D-136a) — always active, not disableable.
// Called directly from pipeline after recording tool result.
func CheckLoopDetection(trail *Trail, threshold int) bool {
	if trail == nil || threshold <= 0 {
		return false
	}
	return trail.HasLoopDetection(threshold)
}
