package agent

import (
	"testing"
)

func TestAutoEventAdapter_ImplementsEventObserver(t *testing.T) {
	// Compile-time interface check.
	var _ EventObserver = (*autoEventAdapter)(nil)
}

func TestAutoEventAdapter_NilHandler(t *testing.T) {
	adapter := &autoEventAdapter{}
	// Should not panic with nil handler.
	if adapter.handler != nil {
		t.Error("expected nil handler")
	}
}
