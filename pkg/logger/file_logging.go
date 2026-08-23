package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog"
)

// Волна 106 (ТЗ-106, D-AUDIT-128): каноничные файловые логи в workspace.
//
// gateway.log — ПОЛНЫЙ поток от DEBUG («логи = всё, что творится», решение
// founder'а 23 авг 2026); errors.log — WARN+. Ротация по размеру, права 0600,
// секреты маскируются (maskSecrets в logMessage). Консоль продолжает следовать
// настроенному gateway.log_level (SetLevel) — файл от него не зависит.
//
// Бонус расположения: workspace внутри RestrictToWorkspace — агент читает
// свои же логи через read_file/exec без новых дыр в гардах (как files/ в 98).

const (
	wsLogDirName     = "logs"
	wsGatewayLogName = "gateway.log"
	wsErrorsLogName  = "errors.log"
	wsLogKeepBackups = 2 // .1 и .2
)

var wsLogMaxSize = int64(10 << 20) // 10 МБ; var — тесты уменьшают порог

var (
	wsLogging      bool
	wsConsoleOff   bool
	wsConsoleLevel = INFO
	wsGateway      *rotatingFile
	wsErrors       *rotatingFile
)

// EnableWorkspaceFileLogging включает tee в <workspace>/logs/. Идемпотентен.
// Вызывается из gateway.Run после загрузки конфига. Ошибка не фатальна для
// вызывающего — консоль продолжает работать в любом случае.
func EnableWorkspaceFileLogging(workspace string) error {
	mu.Lock()
	defer mu.Unlock()

	if wsLogging {
		return nil
	}
	if workspace == "" {
		return fmt.Errorf("logger: workspace is empty")
	}

	dir := filepath.Join(workspace, wsLogDirName)
	gw, err := openRotatingFile(filepath.Join(dir, wsGatewayLogName))
	if err != nil {
		return err
	}
	er, err := openRotatingFile(filepath.Join(dir, wsErrorsLogName))
	if err != nil {
		_ = gw.Close()
		return err
	}

	wsGateway = gw
	wsErrors = er
	wsConsoleLevel = currentLevel
	wsConsoleOff = false
	wsLogging = true

	// Файл должен видеть всё: порог эмиссии опускаем до DEBUG; консольный
	// уровень живёт в фильтре писателя (rebuild), а не в глобальном пороге.
	currentLevel = DEBUG
	zerolog.SetGlobalLevel(DEBUG)

	rebuildWorkspaceLoggerLocked()
	return nil
}

// DisableWorkspaceFileLogging выключает tee и возвращает консольный режим
// (для тестов и shutdown).
func DisableWorkspaceFileLogging() {
	mu.Lock()
	defer mu.Unlock()
	if !wsLogging {
		return
	}
	if wsGateway != nil {
		_ = wsGateway.Close()
		wsGateway = nil
	}
	if wsErrors != nil {
		_ = wsErrors.Close()
		wsErrors = nil
	}
	wsLogging = false
	currentLevel = wsConsoleLevel
	zerolog.SetGlobalLevel(wsConsoleLevel)
	writers = []io.Writer{consoleWriter}
	logger = zerolog.New(io.MultiWriter(writers...)).With().Timestamp().Caller().Logger()
}

// rebuildWorkspaceLoggerLocked пересобирает писателей: консоль по
// wsConsoleLevel, gateway.log от DEBUG, errors.log от WARN, плюс legacy-файл
// из EnableFileLogging, если тот был открыт (PICOCLAW_LOG_FILE). mu держится.
func rebuildWorkspaceLoggerLocked() {
	var lw []io.Writer
	if !wsConsoleOff {
		lw = append(lw, &zerolog.FilteredLevelWriter{
			Writer: zerolog.LevelWriterAdapter{Writer: consoleWriter},
			Level:  wsConsoleLevel,
		})
	}
	lw = append(lw, &zerolog.FilteredLevelWriter{
		Writer: zerolog.LevelWriterAdapter{Writer: wsGateway},
		Level:  DEBUG,
	})
	lw = append(lw, &zerolog.FilteredLevelWriter{
		Writer: zerolog.LevelWriterAdapter{Writer: wsErrors},
		Level:  WARN,
	})
	if logFile != nil {
		lw = append(lw, zerolog.LevelWriterAdapter{Writer: logFile})
	}
	logger = logger.Output(zerolog.MultiLevelWriter(lw...))
}

// rotatingFile — файл с ротацией по размеру (path → path.1 → path.2).
// Потокобезопасен: zerolog пишет из многих горутин.
type rotatingFile struct {
	mu   sync.Mutex
	f    *os.File
	path string
	size int64
}

func openRotatingFile(path string) (*rotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("logger: mkdir %s: %w", filepath.Dir(path), err)
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= wsLogMaxSize {
		rotateLogFiles(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("logger: open %s: %w", path, err)
	}
	var size int64
	if st, err := f.Stat(); err == nil {
		size = st.Size()
	}
	return &rotatingFile{f: f, path: path, size: size}, nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size+int64(len(p)) >= wsLogMaxSize {
		_ = r.f.Close()
		rotateLogFiles(r.path)
		f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return 0, fmt.Errorf("logger: reopen %s: %w", r.path, err)
		}
		r.f = f
		r.size = 0
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// rotateLogFiles сдвигает path → path.1 → path.2 (старейший удаляется).
func rotateLogFiles(path string) {
	for i := wsLogKeepBackups; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", path, i)
		if i == wsLogKeepBackups {
			_ = os.Remove(old)
		}
		if i > 1 {
			_ = os.Rename(fmt.Sprintf("%s.%d", path, i-1), old)
		}
	}
	_ = os.Rename(path, path+".1")
}
