package mcp

// Волна 125 (срезы Б+В, ТЗ-125): жизненный цикл MCP-серверов.
// Idle reap (процессы не висят часами со старым токеном — бой 30 сен:
// github-mcp-server жил 16ч при TTL токена ~8ч) + пробуждение на вызове
// со свежим конфигом + бэкофф реконнекта со сбросом после успеха.
// Индустрия: mcp-gateway (lazy spawn + idle reaping), oh-my-pi RFC
// (lifecycle + idleTimeout), gemini-cli (getValidToken при создании
// транспорта), MCP-006 (cooldown против thundering herd).

import (
	"context"
	"fmt"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// defaultIdleTimeoutMin — простой по умолчанию, после которого сервер
// глушится. 0 в конфиге = этот дефолт; отрицательное = никогда.
const defaultIdleTimeoutMin = 15

// closeServerConn — полный teardown: сессия И процесс (урок Claude Code
// #74329: lazy respawn без kill утекает процессом к init).
func closeServerConn(conn *ServerConnection) {
	if conn == nil {
		return
	}
	if conn.Session != nil {
		_ = conn.Session.Close()
	}
	if conn.proc != nil && conn.proc.Process != nil {
		_ = conn.proc.Process.Kill()
		_ = conn.proc.Wait()
	}
}

// touchServer — отметка активности (idle-счёт). Зовётся из CallTool.
func (m *Manager) touchServer(name string) {
	m.mu.Lock()
	m.lastUsed[name] = time.Now()
	m.mu.Unlock()
}

// reapIdle глушит серверы без вызовов дольше их таймаута. Вызывается
// janitor'ом раз в минуту; в тестах — напрямую с контролируемым now.
// Сервер уходит в sleeping с кэшем тулов: CallTool разбудит (wakeServer).
func (m *Manager) reapIdle(now time.Time) {
	var victims []*ServerConnection
	m.mu.Lock()
	for name, conn := range m.servers {
		timeoutMin := conn.Config.IdleTimeoutMin
		if timeoutMin == 0 {
			timeoutMin = defaultIdleTimeoutMin
		}
		if timeoutMin < 0 {
			continue // «никогда не глушить»
		}
		since := m.lastUsed[name]
		if since.IsZero() {
			since = conn.connectedAt // ни одного вызова — от поднятия
		}
		if now.Sub(since) < time.Duration(timeoutMin)*time.Minute {
			continue
		}
		delete(m.servers, name)
		m.sleeping[name] = conn
		// idle — НЕ «упал»: тулы доступны из кэша, сервер проснётся на вызове.
		m.statuses[name] = &ServerStatus{
			Name: name, Connected: true, Tools: len(conn.Tools), State: "idle",
		}
		victims = append(victims, conn)
	}
	m.mu.Unlock()

	for _, conn := range victims {
		logger.InfoCF("mcp", "MCP server idle-reaped (wakes on next call)",
			map[string]any{"server": conn.Name})
		closeServerConn(conn)
	}
}

// startJanitorLocked запускает реапер один раз на менеджер (первый
// успешный ConnectServer). m.mu уже держит вызывающий.
func (m *Manager) startJanitorLocked() {
	m.janitorOnce.Do(func() {
		m.janitorStop = make(chan struct{})
		go func() {
			t := time.NewTicker(time.Minute)
			defer t.Stop()
			for {
				select {
				case <-m.janitorStop:
					return
				case now := <-t.C:
					m.reapIdle(now)
				}
			}
		}()
	})
}

// wakeServer будит спящий (idle-reaped) сервер: свежий конфиг из
// рефрешера ДО спавна (токен актуальный by construction — getValidToken
// при создании транспорта, индустрия), коннект, возврат в servers.
func (m *Manager) wakeServer(
	ctx context.Context, name string,
) (*ServerConnection, error) {
	m.mu.RLock()
	sleeper, ok := m.sleeping[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("server %s not found", name)
	}
	if wait := m.reconnectBlocked(name); wait > 0 {
		return nil, fmt.Errorf(
			"MCP server %q восстанавливается, повтори через %s",
			name, wait.Round(time.Second))
	}
	cfg := sleeper.Config
	if fresh, ok2 := m.freshServerConfig(name, cfg); ok2 {
		cfg = fresh
	}
	conn, err := connectServerFunc(ctx, name, cfg)
	if err != nil {
		m.recordReconnectFail(name)
		m.setStatus(name, false, len(sleeper.Tools), err.Error())
		return nil, fmt.Errorf("failed to wake MCP server %s: %w", name, err)
	}
	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		closeServerConn(conn)
		return nil, fmt.Errorf("manager is closed")
	}
	delete(m.sleeping, name)
	m.servers[name] = conn
	m.lastUsed[name] = time.Now()
	m.setStatusLocked(name, true, len(conn.Tools), "")
	m.mu.Unlock()
	m.recordReconnectSuccess(name)
	logger.InfoCF("mcp", "MCP server woke after idle reap",
		map[string]any{"server": name})
	return conn, nil
}

// --- Бэкофф реконнекта (срез В) ---

// reconnectBlocked: сколько ещё ждать перед попыткой (0 = можно).
func (m *Manager) reconnectBlocked(name string) time.Duration {
	m.backoffMu.Lock()
	defer m.backoffMu.Unlock()
	if d := time.Until(m.nextRetryAt[name]); d > 0 {
		return d
	}
	return 0
}

// recordReconnectFail: экспонента 5s→…→cap 5m (анти-thundering herd).
func (m *Manager) recordReconnectFail(name string) {
	m.backoffMu.Lock()
	defer m.backoffMu.Unlock()
	m.failCount[name]++
	n := m.failCount[name]
	d := 5 * time.Second << min(n-1, 6)
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	m.nextRetryAt[name] = time.Now().Add(d)
}

// recordReconnectSuccess: СБРОС после успеха — урок hermes-agent
// (счётчик без сброса = вечная смерть на длинном аптайме).
func (m *Manager) recordReconnectSuccess(name string) {
	m.backoffMu.Lock()
	defer m.backoffMu.Unlock()
	delete(m.failCount, name)
	delete(m.nextRetryAt, name)
}
