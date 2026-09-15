// Волна 108 (ТЗ-108, D-AUDIT-131): паспорта артефактов.
// Go пишет в artifact_passports автоматически после каждой успешной записи
// файла мутирующим тулом — на трубе ToolRegistry.ExecuteWithContext.
// Модель леджер НЕ пишет — только читает через search_memory.
// UPSERT по пути = паспорт показывает последнее состояние файла.
// Своя таблица (expand-only миграция v7): registry со своим CHECK
// не трогаем — жёсткость схемы это охрана, не баг.

package pika

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// artifactHashMaxBytes — выше этого хеш не считаем (файл огромный).
const artifactHashMaxBytes = 50 << 20

// ArtifactLedger пишет паспорта артефактов в artifact_passports.
// Реализует tools.ArtifactRecorder.
type ArtifactLedger struct {
	mem       *BotMemory
	workspace string
}

// NewArtifactLedger creates the ledger over BotMemory + workspace root.
func NewArtifactLedger(mem *BotMemory, workspace string) *ArtifactLedger {
	return &ArtifactLedger{mem: mem, workspace: workspace}
}

// Record вызывается с трубы ПОСЛЕ успешной записи. Все ошибки — debug-лог:
// тул уже отработал, леджер ничего не ломает.
func (l *ArtifactLedger) Record(
	ctx context.Context, toolName string, args map[string]any,
) {
	if l.mem == nil {
		return
	}
	path, _ := args["path"].(string)
	if path == "" {
		return
	}
	abs := path
	if !filepath.IsAbs(abs) && l.workspace != "" {
		abs = filepath.Join(l.workspace, path)
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return // не записался / удалён / директория — паспорта нет
	}

	var sum string
	if info.Size() <= artifactHashMaxBytes {
		// #nosec G304 -- путь прошёл валидацию fs-тула (sandboxFs);
		// леджер читает только что записанный этим тулом файл.
		data, err := os.ReadFile(abs)
		if err != nil {
			logger.DebugCF("artifact-ledger", "read failed",
				map[string]any{"error": err.Error()})
			return
		}
		h := sha256.Sum256(data)
		sum = hex.EncodeToString(h[:])
	}

	key := abs
	if l.workspace != "" {
		if rel, rerr := filepath.Rel(l.workspace, abs); rerr == nil &&
			!strings.HasPrefix(rel, "..") {
			key = rel
		}
	}

	_, err = l.mem.db.ExecContext(ctx,
		`INSERT INTO artifact_passports (path, tool, session, sha256, size)
		VALUES(?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET
			tool=excluded.tool, session=excluded.session,
			sha256=excluded.sha256, size=excluded.size,
			updated_at=CURRENT_TIMESTAMP`,
		key, toolName, toolshared.ToolSessionKey(ctx), sum, info.Size())
	if err != nil {
		logger.DebugCF("artifact-ledger", "upsert failed",
			map[string]any{"error": err.Error()})
	}
}
