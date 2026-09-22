package config

import "testing"

// PIKA-V3 (волна 115): свежая установка → веб-поиск из коробки:
// дефолт SearXNG (localhost:4000, поднимается лаунчером) + DDG немой фолбэк.
func TestDefaultConfig_WebSearchOutOfBox(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Tools.Web.SearXNG.Enabled {
		t.Error("SearXNG должен быть включён по умолчанию (волна 115)")
	}
	if cfg.Tools.Web.SearXNG.BaseURL != "http://localhost:4000" {
		t.Errorf(
			"SearXNG BaseURL = %q, want http://localhost:4000",
			cfg.Tools.Web.SearXNG.BaseURL,
		)
	}
	if !cfg.Tools.Web.DuckDuckGo.Enabled {
		t.Error("DuckDuckGo должен быть включён по умолчанию (немой фолбэк, волна 115)")
	}
}
