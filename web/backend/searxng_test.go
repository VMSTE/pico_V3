package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// PIKA-V3 (волна 115): тесты ensureSearXNG.

func setupSearXNGTest(t *testing.T, enabled bool, baseURL string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIKA_HOME", home)
	t.Setenv("PICOCLAW_HOME", home)
	cfg := config.DefaultConfig()
	cfg.Tools.Web.SearXNG.Enabled = enabled
	cfg.Tools.Web.SearXNG.BaseURL = baseURL
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	return cfgPath
}

func stubDocker(t *testing.T, inspectFails bool, lookPathFails bool) *[]string {
	t.Helper()
	calls := &[]string{}
	searxngLookPath = func(name string) (string, error) {
		if lookPathFails {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/docker", nil
	}
	searxngRun = func(bin string, args ...string) (string, error) {
		*calls = append(*calls, strings.Join(args, " "))
		if len(args) > 0 && args[0] == "inspect" && inspectFails {
			return "no such container", os.ErrNotExist
		}
		return "ok", nil
	}
	t.Cleanup(func() {
		searxngLookPath = exec.LookPath
		searxngRun = defaultSearxngRun
	})
	return calls
}

func TestEnsureSearXNG_DisabledSkipsDocker(t *testing.T) {
	cfgPath := setupSearXNGTest(t, false, "http://localhost:4000")
	calls := stubDocker(t, false, false)
	ensureSearXNG(cfgPath)
	if len(*calls) != 0 {
		t.Fatalf("docker не должен вызываться при выключенном searxng: %v", *calls)
	}
}

func TestEnsureSearXNG_NoDockerNoPanic(t *testing.T) {
	cfgPath := setupSearXNGTest(t, true, "http://localhost:4000")
	calls := stubDocker(t, false, true)
	ensureSearXNG(cfgPath) // не должно паниковать
	if len(*calls) != 0 {
		t.Fatalf("без docker не должно быть вызовов: %v", *calls)
	}
}

func TestEnsureSearXNG_StartsExistingContainer(t *testing.T) {
	cfgPath := setupSearXNGTest(t, true, "http://localhost:4000")
	calls := stubDocker(t, false, false)
	ensureSearXNG(cfgPath)
	want := []string{"inspect searxng", "start searxng"}
	if strings.Join(*calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %v, want %v", *calls, want)
	}
}

func TestEnsureSearXNG_CreatesContainerAndSettings(t *testing.T) {
	cfgPath := setupSearXNGTest(t, true, "http://localhost:4000")
	calls := stubDocker(t, true, false)
	ensureSearXNG(cfgPath)

	if len(*calls) != 2 || !strings.HasPrefix((*calls)[1], "run -d") {
		t.Fatalf("ожидался inspect + run, получено: %v", *calls)
	}
	runCall := (*calls)[1]
	for _, want := range []string{
		"--name searxng",
		"--restart unless-stopped",
		"-p 127.0.0.1:4000:8080",
		"searxng/searxng:latest",
	} {
		if !strings.Contains(runCall, want) {
			t.Errorf("docker run не содержит %q: %s", want, runCall)
		}
	}

	settingsPath := filepath.Join(os.Getenv("PIKA_HOME"), "searxng", "settings.yml")
	body, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.yml не создан: %v", err)
	}
	if !strings.Contains(string(body), "secret_key") ||
		!strings.Contains(string(body), "json") {
		t.Errorf("settings.yml без secret_key/json формата:\n%s", body)
	}

	// Идемпотентность: повторный прогон с «существующим» контейнером
	// не перезаписывает settings.yml.
	before, _ := os.ReadFile(settingsPath)
	stubDocker(t, false, false)
	ensureSearXNG(cfgPath)
	after, _ := os.ReadFile(settingsPath)
	if string(before) != string(after) {
		t.Error("settings.yml перезаписан при повторном запуске — secret_key потерян")
	}
}

func TestEnsureSearXNG_RemoteInstanceSkipped(t *testing.T) {
	cfgPath := setupSearXNGTest(t, true, "http://192.168.1.50:8888")
	calls := stubDocker(t, false, false)
	ensureSearXNG(cfgPath)
	if len(*calls) != 0 {
		t.Fatalf("внешний инстанс не трогаем, docker calls: %v", *calls)
	}
}

func TestSearXNGLocalPort(t *testing.T) {
	cases := []struct {
		baseURL string
		port    string
		local   bool
	}{
		{"http://localhost:4000", "4000", true},
		{"http://localhost:4000/", "4000", true},
		{"http://127.0.0.1:8888", "8888", true},
		{"http://localhost", "4000", true},
		{"http://192.168.1.50:8888", "", false},
		{"https://searx.example.org", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		port, local := searxngLocalPort(c.baseURL)
		if port != c.port || local != c.local {
			t.Errorf("searxngLocalPort(%q) = (%q, %v), want (%q, %v)",
				c.baseURL, port, local, c.port, c.local)
		}
	}
}
