package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Волна 120 (срез 1): спутники получают дефолтный таймаут 600s,
// явный request_timeout из конфига имеет приоритет, оригинал не мутирует.
func TestWithSatelliteTimeout_DefaultsWhenUnset(t *testing.T) {
	mc := &config.ModelConfig{ModelName: "background"}
	got := withSatelliteTimeout(mc)
	if got.RequestTimeout != satelliteRequestTimeoutSec {
		t.Errorf("RequestTimeout = %d, want %d",
			got.RequestTimeout, satelliteRequestTimeoutSec)
	}
	if mc.RequestTimeout != 0 {
		t.Error("original ModelConfig must not be mutated")
	}
}

func TestWithSatelliteTimeout_RespectsExplicit(t *testing.T) {
	mc := &config.ModelConfig{ModelName: "background", RequestTimeout: 300}
	got := withSatelliteTimeout(mc)
	if got.RequestTimeout != 300 {
		t.Errorf("RequestTimeout = %d, want 300 (explicit config)", got.RequestTimeout)
	}
}

func TestWithSatelliteTimeout_Nil(t *testing.T) {
	if got := withSatelliteTimeout(nil); got != nil {
		t.Errorf("nil input should return nil, got %+v", got)
	}
}
