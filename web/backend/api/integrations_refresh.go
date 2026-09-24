package api

// Волна 121 (ТЗ-121, срез А): фоновый рефреш OAuth-токенов интеграций.
// Корень боя 24 сен: refreshGitHubToken существовал, но триггерился только
// поллом карточки — токен умирал между поллами → окно авторизации в бою.
// Notion умирал в Unauthorized на initialize (errors.log 02:50/03:59).
// Single-writer: токены пишет только лаунчер (этот процесс), атомарно
// (SaveConfig → WriteFileAtomic). Гейтвар — читатель: подхватывает свежий
// токен через 401 → reload → reconnect → 1 ретрай (pkg/mcp).

import (
	"context"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	integrationRefreshInterval = 5 * time.Minute
	integrationRefreshBuffer   = 5 * time.Minute
)

// authErrorReconnectRequired — маркер для карточки: refresh-токен мёртв
// окончательно (invalid_grant), тик по провайдеру остановлен до ручного
// переподключения (connect сбрасывает AuthError).
const authErrorReconnectRequired = "reconnect_required"

// isInvalidGrantErr: refresh-токен отозван/протух окончательно.
func isInvalidGrantErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid_grant")
}

// shouldRefreshExpiresAt: пора ли рефрешить (буфер до истечения).
// Пустой/битый expiresAt → false: проактив невозможен, остаётся
// реактивный 401-путь гейтвара (дефолт-интервал не гадаем).
func shouldRefreshExpiresAt(expiresAt string, now time.Time) bool {
	if expiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return false
	}
	return !now.Before(t.Add(-integrationRefreshBuffer))
}

// refreshProviderIfDue — общий цикл «проверь → рефрешни → классифицируй».
// Возвращает (новый AuthError, изменилось ли что-то → SaveConfig).
func refreshProviderIfDue(
	provider string,
	connected bool,
	authError, refreshToken, expiresAt string,
	now time.Time,
	refresh func(ctx context.Context) error,
) (string, bool) {
	if !connected || authError != "" || refreshToken == "" || !shouldRefreshExpiresAt(expiresAt, now) {
		return authError, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := refresh(ctx)
	cancel()
	if err == nil {
		logger.InfoCF("integrations", provider+": token refreshed proactively", nil)
		return "", true
	}
	if isInvalidGrantErr(err) {
		logger.WarnCF("integrations", provider+": invalid_grant — reconnect required",
			map[string]any{"error": err.Error()})
		return authErrorReconnectRequired, true
	}
	logger.WarnCF("integrations", provider+": refresh failed, retry next tick",
		map[string]any{"error": err.Error()})
	return authError, false
}

// refreshIntegrationsOnce — одно тело тика (отдельно от тикера ради тестов).
// Сериализовано вызывающим: одна горутина, один тик = один проход.
func refreshIntegrationsOnce(configPath string, now time.Time) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logger.ErrorCF("integrations", "refresh tick: cannot load config",
			map[string]any{"error": err.Error()})
		return
	}
	changed := false

	gh := cfg.Integrations.GitHub
	authErr, ch := refreshProviderIfDue(
		"github", gh.Connected(), gh.AuthError, gh.RefreshToken.String(), gh.ExpiresAt, now,
		func(ctx context.Context) error { return refreshGitHubToken(ctx, &gh) },
	)
	gh.AuthError = authErr
	if ch {
		cfg.Integrations.GitHub = gh
		changed = true
	}

	n := cfg.Integrations.Notion
	authErr, ch = refreshProviderIfDue(
		"notion", n.Connected(), n.AuthError, n.RefreshToken.String(), n.ExpiresAt, now,
		func(ctx context.Context) error { return refreshNotionToken(ctx, &n) },
	)
	n.AuthError = authErr
	if ch {
		cfg.Integrations.Notion = n
		changed = true
	}

	if changed {
		if err := config.SaveConfig(configPath, cfg); err != nil {
			logger.ErrorCF("integrations", "refresh tick: cannot save config",
				map[string]any{"error": err.Error()})
		}
	}
}

// StartIntegrationRefresher — сериализованный тикер, живёт с процессом
// лаунчера. Первый прогон сразу: токен мог умереть, пока лаунчер был выключен.
func (h *Handler) StartIntegrationRefresher() {
	go func() {
		refreshIntegrationsOnce(h.configPath, time.Now())
		t := time.NewTicker(integrationRefreshInterval)
		defer t.Stop()
		for range t.C {
			refreshIntegrationsOnce(h.configPath, time.Now())
		}
	}()
}
