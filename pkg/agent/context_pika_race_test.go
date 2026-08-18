package agent

import (
	"sync"
	"testing"
)

// D-AUDIT-101: lastSessionKey must be safe under parallel turns.
// Run with -race: concurrent set/get must not trigger a data race.
func TestPikaAdapterSessionKeyConcurrentAccess(t *testing.T) {
	a := &pikaContextManagerAdapter{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				a.setLastSessionKey("sess-writer")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = a.sessionKey()
			}
		}()
	}
	wg.Wait()
	if got := a.sessionKey(); got != "sess-writer" {
		t.Fatalf("sessionKey() = %q, want sess-writer", got)
	}
}
