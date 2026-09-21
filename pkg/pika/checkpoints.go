// Волна 109 (ТЗ-109, D-AUDIT-131 фаза 2): машина времени — чекпоинты
// workspace в shadow git store (.vault/store) + откат с защитой ручных
// правок через паспорта артефактов (волна 108). Паттерн — Hermes
// checkpoint_manager.py (прочитан глазами); у нас один периметр
// (workspace), поэтому проще. Это НЕ тул — модель его не видит.

package pika

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	vaultDirName         = ".vault"
	checkpointStoreDir   = "store"
	checkpointMaxFileMB  = 10
	checkpointCmdTimeout = 30 * time.Second
)

// checkpointExcludes — что НЕ попадает в чекпоинты (шум и гиганты):
// база мельтешит каждый ход, логи/аплоады не ценность отката.
const checkpointExcludes = ".git/\n.vault/\nmemory/\nlogs/\nfiles/\nnode_modules/\n"

var checkpointHashRe = regexp.MustCompile(`^[0-9a-fA-F]{6,64}$`)

// CheckpointInfo — одна запись истории чекпоинтов.
type CheckpointInfo struct {
	Hash   string
	Time   string
	Reason string
}

// CheckpointManager — прозрачные снапшоты workspace перед мутациями.
// Все ошибки нефатальны (debug-лог): чекпоинт — страховка, не блокер.
type CheckpointManager struct {
	workspace string
	storeDir  string
	excludeFl string
	mu        sync.Mutex
	disabled  string // непусто = причина, почему выключен
}

// NewCheckpointManager создаёт менеджер; nil не возвращает никогда.
// Нет git на PATH или пустой workspace → выключен молча.
func NewCheckpointManager(workspace string) *CheckpointManager {
	m := &CheckpointManager{workspace: workspace}
	m.storeDir = filepath.Join(workspace, vaultDirName, checkpointStoreDir)
	m.excludeFl = filepath.Join(m.storeDir, "exclude")
	switch {
	case strings.TrimSpace(workspace) == "":
		m.disabled = "workspace is empty"
	default:
		if _, err := exec.LookPath("git"); err != nil {
			m.disabled = "git not found on PATH"
		}
	}
	return m
}

// Enabled — чекпоинты активны.
func (m *CheckpointManager) Enabled() bool { return m.disabled == "" }

// BeforeMutation — интерфейс tools.CheckpointTaker: снапшот ДО записи.
func (m *CheckpointManager) BeforeMutation(toolName string, _ map[string]any) {
	m.MaybeCheckpoint("before " + toolName)
}

// MaybeCheckpoint делает снапшот, если что-то изменилось с прошлого раза.
func (m *CheckpointManager) MaybeCheckpoint(reason string) {
	if !m.Enabled() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureStore(); err != nil {
		logger.DebugCF("checkpoints", "store init failed",
			map[string]any{"error": err.Error()})
		return
	}
	if _, err := m.runGit("add", "-A"); err != nil {
		logger.DebugCF("checkpoints", "git add failed",
			map[string]any{"error": err.Error()})
		return
	}
	m.dropOversizeLocked()

	hasHead := false
	if _, err := m.runGit("rev-parse", "--verify", "HEAD"); err == nil {
		hasHead = true
	}
	if hasHead {
		if _, err := m.runGit("diff", "--cached", "--quiet", "HEAD"); err == nil {
			return // нет изменений — чекпоинт не нужен
		}
	} else {
		out, _ := m.runGit("diff", "--cached", "--name-only")
		if strings.TrimSpace(out) == "" {
			return
		}
	}

	if strings.TrimSpace(reason) == "" {
		reason = "checkpoint"
	}
	if _, err := m.runGit("commit", "--quiet", "--no-gpg-sign", "-m", reason); err != nil {
		logger.DebugCF("checkpoints", "commit failed",
			map[string]any{"error": err.Error()})
		return
	}
	logger.DebugCF("checkpoints", "checkpoint taken",
		map[string]any{"reason": reason})
}

// List — история чекпоинтов, новейший первым (1-based для /rollback N).
func (m *CheckpointManager) List() []CheckpointInfo {
	if !m.Enabled() {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := os.Stat(filepath.Join(m.storeDir, "HEAD")); err != nil {
		return nil
	}
	out, err := m.runGit("log", "--format=%H%x09%aI%x09%s")
	if err != nil {
		return nil
	}
	var res []CheckpointInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) == 3 {
			res = append(res, CheckpointInfo{
				Hash: parts[0], Time: parts[1], Reason: parts[2],
			})
		}
	}
	return res
}

// Rollback восстанавливает workspace на чекпоинт. target — номер из List
// (1-based, новейший первый) или хеш. Без force откатываются только файлы,
// чей текущий хеш совпадает с паспортом (писала Пика, founder не трогал) —
// ручные правки переживают откат и попадают в отчёт skipped.
func (m *CheckpointManager) Rollback(
	ctx context.Context, target string, mem *BotMemory, force bool,
) (restored []string, skipped []string, err error) {
	if !m.Enabled() {
		return nil, nil, fmt.Errorf("checkpoints disabled: %s", m.disabled)
	}
	hash, err := m.resolveTarget(target)
	if err != nil {
		return nil, nil, err
	}
	if _, verr := m.runGit("rev-parse", "--verify", hash+"^{commit}"); verr != nil {
		return nil, nil, fmt.Errorf("checkpoint %q not found", target)
	}

	// «отмена отмены»: снапшот текущего состояния ДО отката
	m.MaybeCheckpoint("pre-rollback (restoring to " + hash[:8] + ")")

	m.mu.Lock()
	defer m.mu.Unlock()

	out, derr := m.runGit("diff", "--name-status", "-z", hash)
	if derr != nil {
		return nil, nil, fmt.Errorf("diff: %w", derr)
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		status := fields[i]
		rel := fields[i+1]
		if rel == "" {
			continue
		}
		// D (файл чекпоинта удалён сейчас) — восстанавливаем всегда:
		// откат и существует, чтобы вернуть удалённое.
		keep := !force && status != "D" && !m.passportIntact(ctx, mem, rel)
		if keep {
			skipped = append(skipped, rel)
			continue
		}
		switch status {
		case "A":
			// создан ПОСЛЕ чекпоинта (паспорт совпал = писала Пика) — убрать
			if rerr := os.Remove(filepath.Join(m.workspace, rel)); rerr == nil {
				restored = append(restored, rel+" (removed)")
			}
		default:
			if _, cerr := m.runGit("checkout", hash, "--", rel); cerr == nil {
				restored = append(restored, rel)
			}
		}
	}
	return restored, skipped, nil
}

// resolveTarget — номер (1-based из List) или хеш → полный хеш коммита.
func (m *CheckpointManager) resolveTarget(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("usage: /rollback <N|hash>")
	}
	if checkpointHashRe.MatchString(target) {
		return strings.ToLower(target), nil
	}
	n, err := strconv.Atoi(target)
	if err != nil || n < 1 {
		return "", fmt.Errorf(
			"invalid checkpoint %q: use a number from /rollback or a hash", target)
	}
	list := m.List()
	if n > len(list) {
		return "", fmt.Errorf("checkpoint %d not found (%d total)", n, len(list))
	}
	return list[n-1].Hash, nil
}

// passportIntact — true, если файл на диске в точности равен тому, что
// записала Пика (паспорт волны 108). Нет паспорта или хеш разошёлся →
// файл трогали руками → откат его НЕ затирает.
func (m *CheckpointManager) passportIntact(
	ctx context.Context, mem *BotMemory, rel string,
) bool {
	if mem == nil {
		return false
	}
	var sum string
	err := mem.db.QueryRowContext(ctx,
		`SELECT sha256 FROM artifact_passports WHERE path=?`, rel).Scan(&sum)
	if err != nil || sum == "" {
		return false
	}
	// #nosec G304 -- путь из git diff нашего shadow-стора, не от модели.
	data, err := os.ReadFile(filepath.Join(m.workspace, rel))
	if err != nil {
		return false
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]) == sum
}

// ensureStore — ленивый init: .vault/store как bare-репо + файл исключений.
// Ноль git-следов в самом workspace (всё через --git-dir/--work-tree).
func (m *CheckpointManager) ensureStore() error {
	if err := os.MkdirAll(m.storeDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(m.excludeFl, []byte(checkpointExcludes), 0o600); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(m.storeDir, "HEAD")); err == nil {
		return nil
	}
	// init идёт БЕЗ --git-dir/--work-tree: git запрещает --work-tree
	// для init (fatal: GIT_WORK_TREE not allowed without GIT_DIR) —
	// поймано репродукцией в бою. Остальные команды — через runGit.
	return m.initStore()
}

// dropOversizeLocked — выкидывает из индекса файлы больше лимита
// (датасеты, модели, сгенерированное медиа).
func (m *CheckpointManager) dropOversizeLocked() {
	out, err := m.runGit("diff", "--cached", "--name-only", "-z")
	if err != nil || out == "" {
		return
	}
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		info, serr := os.Stat(filepath.Join(m.workspace, p))
		if serr != nil || info.Size() <= checkpointMaxFileMB<<20 {
			continue
		}
		_, _ = m.runGit("reset", "--quiet", "--", p)
	}
}

// initStore — единственная команда вне runGit: git init не принимает
// глобальный --work-tree (см. ensureStore).
func (m *CheckpointManager) initStore() error {
	ctx, cancel := context.WithTimeout(context.Background(), checkpointCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", // #nosec G204 -- фиксированный бинарь git
		"init", "--bare", "--quiet", m.storeDir)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runGit — git с изолированной конфигурацией: чужие ~/.gitconfig и
// pinentry-подсказки не ломают фоновые снапшоты.
func (m *CheckpointManager) runGit(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), checkpointCmdTimeout)
	defer cancel()
	full := append([]string{
		"--git-dir=" + m.storeDir,
		"--work-tree=" + m.workspace,
		"-c", "core.excludesFile=" + m.excludeFl,
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) // #nosec G204 -- фиксированный бинарь git, аргументы наши
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=AtoMinD",
		"GIT_AUTHOR_EMAIL=checkpoint@atomind.local",
		"GIT_COMMITTER_NAME=AtoMinD",
		"GIT_COMMITTER_EMAIL=checkpoint@atomind.local",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
