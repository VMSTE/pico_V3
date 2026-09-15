package tools

import (
	"context"
	"path/filepath"
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
