package main

// PIKA-V3 (волна 115): ensureSearXNG — автоподъём локального SearXNG при старте
// лаунчера. Идемпотентно, нефатально: любая ошибка → WARN в лог, старт
// лаунчера продолжается (принцип волны 113: периферия не роняет ядро).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/web/backend/utils"
)

const searxngContainerName = "searxng"

// Точки подмены для тестов (паттерн utils/onboard.go: var execCommand = exec.Command).
var (
	searxngLookPath = exec.LookPath
	searxngRun      = defaultSearxngRun
)

func defaultSearxngRun(bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return string(out), err
}

// ensureSearXNG поднимает контейнер searxng, если провайдер включён в конфиге
// и base_url указывает на localhost. Управляем только локальным инстансом.
func ensureSearXNG(configPath string) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logger.WarnC("web", fmt.Sprintf("wave115 searxng: config load failed: %v", err))
		return
	}
	sx := cfg.Tools.Web.SearXNG
	if !sx.Enabled {
		return
	}

	port, local := searxngLocalPort(sx.BaseURL)
	if !local {
		logger.InfoC("web", fmt.Sprintf(
			"wave115 searxng: base_url %q не localhost — внешний инстанс, автоподъём пропущен",
			sx.BaseURL,
		))
		return
	}

	dockerBin, err := searxngLookPath("docker")
	if err != nil {
		logger.WarnC("web",
			"wave115 searxng: провайдер включён, но docker не найден в PATH — "+
				"поиск уйдёт в фолбэк. Установи Docker или смени провайдера в Морда → Инструменты")
		return
	}

	if _, inspErr := searxngRun(dockerBin, "inspect", searxngContainerName); inspErr == nil {
		// Контейнер существует (создан нами или руками) — просто стартуем.
		if out, startErr := searxngRun(dockerBin, "start", searxngContainerName); startErr != nil {
			logger.WarnC("web", fmt.Sprintf("wave115 searxng: docker start: %v (%s)", startErr, out))
			return
		}
		logger.InfoC("web", "wave115 searxng: контейнер запущен (docker start)")
		return
	}

	settingsPath, err := ensureSearXNGSettings()
	if err != nil {
		logger.WarnC("web", fmt.Sprintf("wave115 searxng: settings.yml: %v", err))
		return
	}

	out, err := searxngRun(
		dockerBin,
		"run", "-d",
		"--name", searxngContainerName,
		"--restart", "unless-stopped",
		"-p", fmt.Sprintf("127.0.0.1:%s:8080", port),
		"-v", fmt.Sprintf("%s:/etc/searxng/settings.yml:ro", settingsPath),
		"searxng/searxng:latest",
	)
	if err != nil {
		logger.WarnC("web", fmt.Sprintf("wave115 searxng: docker run: %v (%s)", err, out))
		return
	}
	logger.InfoC("web", fmt.Sprintf(
		"wave115 searxng: контейнер создан и запущен на 127.0.0.1:%s", port))
}

// searxngLocalPort извлекает порт из base_url и проверяет, что хост локальный.
func searxngLocalPort(baseURL string) (string, bool) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", false
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return "", false
	}
	if p := u.Port(); p != "" {
		return p, true
	}
	return "4000", true
}

// ensureSearXNGSettings создаёт settings.yml при первом подъёме. Идемпотентно:
// существующий файл не перезаписывается (secret_key остаётся стабильным).
func ensureSearXNGSettings() (string, error) {
	dir := filepath.Join(utils.GetPicoclawHome(), "searxng")
	settingsPath := filepath.Join(dir, "settings.yml")
	if _, err := os.Stat(settingsPath); err == nil {
		return settingsPath, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("secret_key: %w", err)
	}
	content := fmt.Sprintf(
		"use_default_settings: true\nsearch:\n  formats: [html, json]\nserver:\n  secret_key: %q\n  limiter: false\n",
		hex.EncodeToString(secret),
	)
	if err := os.WriteFile(settingsPath, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	return settingsPath, nil
}
