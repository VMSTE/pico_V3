package tools

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
)

// Волна 108 (ТЗ-108, D-AUDIT-131): паспорта артефактов + защита .vault.

// ArtifactRecorder получает уведомление после успешной записи файла
// мутирующим тулом. Писатель — Go (не модель): леджер не врёт.
type ArtifactRecorder interface {
	Record(ctx context.Context, toolName string, args map[string]any)
}

// artifactMutatingTools — тулы, пишущие файлы (v1: только fs-тулы).
var artifactMutatingTools = map[string]bool{
	"write_file":  true,
	"edit_file":   true,
	"append_file": true,
}

// SetArtifactRecorder подключает писателя паспортов (pika.ArtifactLedger).
func (r *ToolRegistry) SetArtifactRecorder(rec ArtifactRecorder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.artifactRecorder = rec
}

// CheckpointTaker получает вызов ДО мутации (машина времени, волна 109).
// Реализация обязана быть нефатальной: ошибки чекпоинта не ломают тул.
type CheckpointTaker interface {
	BeforeMutation(toolName string, args map[string]any)
}

// SetCheckpointTaker подключает машину времени (pika.CheckpointManager).
func (r *ToolRegistry) SetCheckpointTaker(t CheckpointTaker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkpointTaker = t
}

// execDestructiveRe — exec-команды, трогающие файлы (чекпоинт ДО них).
var execDestructiveRe = regexp.MustCompile(
	`(?i)\b(rm|mv|cp|sed\s+-i|truncate|dd|shred|install|git\s+(reset|clean|checkout|restore))\b|>`)

// execDestructive — true, если exec-команда может изменить файлы.
func execDestructive(name string, args map[string]any) bool {
	if name != "exec" {
		return false
	}
	cmd, _ := args["command"].(string)
	return execDestructiveRe.MatchString(cmd)
}

// isVaultToolPath — true, если путь из args ведёт в закрытую зону .vault.
// Сегментная проверка работает и для относительных, и для абсолютных путей.
func isVaultToolPath(args map[string]any) bool {
	p, _ := args["path"].(string)
	if p == "" {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(p))
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == ".vault" {
			return true
		}
	}
	return false
}
