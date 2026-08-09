package pika

import (
	"strings"
	"testing"
)

// D-AUDIT-65: реальное состояние телеметрии доезжает до блока
// DEGRADATION с инструкцией-перенаправлением (D-92, ТЗ-v2-2b).
func TestDegradationBlock_FromTelemetry(t *testing.T) {
	tel := NewTelemetry(defaultTelemetryCfg(), nil, nil)

	// Здоровая система — блок пустой
	cm := NewPikaContextManager(t.TempDir(), NewTrail(), NewMeta(), tel, nil)
	if got := cm.BuildDegradationBlock(); got != "" {
		t.Fatalf("healthy system should produce empty block, got %q", got)
	}

	// Архивариус деградировал — блок с перенаправлением на search_memory
	tel.ReportComponentFailure("archivist", "degraded")
	block := cm.BuildDegradationBlock()
	if !strings.Contains(block, "DEGRADATION") {
		t.Fatal("expected DEGRADATION block")
	}
	if !strings.Contains(block, "search_memory") {
		t.Error("expected redirect instruction to search_memory")
	}

	// Восстановился — блок снова пустой
	tel.ReportComponentSuccess("archivist")
	if got := cm.BuildDegradationBlock(); got != "" {
		t.Error("expected empty block after recovery")
	}
}
