package mcp

import "testing"

// D-AUDIT-73: per-server RPM — burst проходит, превышение отклоняется,
// снятие лимита работает, сервер без лимита не затронут.
func TestManager_SetServerRPM(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()

	if !m.allowCall("free") {
		t.Fatal("server without limit should pass")
	}

	m.SetServerRPM("srv", 2)
	if !m.allowCall("srv") || !m.allowCall("srv") {
		t.Fatal("first two calls should pass (burst)")
	}
	if m.allowCall("srv") {
		t.Fatal("third call should be rejected")
	}

	m.SetServerRPM("srv", 0)
	if !m.allowCall("srv") {
		t.Fatal("after removing the limit, calls should pass")
	}
}
