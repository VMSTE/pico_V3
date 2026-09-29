package pika

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Волна 124 (срез В, ТЗ-124): версии промптов.
// prompt_versions (component, version, hash, content) существовала
// в схеме с рождения, но писателя не было — 0 строк за всю историю.
// Писатель живёт в DiagnosticsEngine.BuildSubagentPrompt (единая
// воронка промптов спутников); снапшоты читают LatestPromptVersion.

// Контракт схемы (migrate.go §12, CHECK): component — перечисление
// ('CORE','CONTEXT','ATOMIZER','REFLEXOR','ARCHIVIST_BUILD','MCP_GUARD'),
// а не внутренние id спутников ("archivist" и т.п.). INSERT OR IGNORE
// глотает CHECK молча (бой среза В: версия "успешно" не записалась) —
// поэтому нормализация здесь, одним местом.
var promptVersionComponent = map[string]string{
	"archivist": "ARCHIVIST_BUILD",
	"atomizer":  "ATOMIZER",
	"reflexor":  "REFLEXOR",
	"mcp_guard": "MCP_GUARD",
}

func promptComponentName(component string) string {
	if c, ok := promptVersionComponent[component]; ok {
		return c
	}
	return strings.ToUpper(component)
}

// EnsurePromptVersion — версия промпта по содержимому. Тот же хеш →
// существующий prompt_id (0 новых строк); новый хеш → max(version)+1.
func (bm *BotMemory) EnsurePromptVersion(
	ctx context.Context, component, content string,
) (string, error) {
	component = promptComponentName(component)
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	var id string
	err := bm.db.QueryRowContext(ctx,
		`SELECT prompt_id FROM prompt_versions
		WHERE component=? AND hash=?`,
		component, hash,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("pika/botmemory: find prompt version: %w", err)
	}
	maxV := 0
	if err := bm.db.QueryRowContext(ctx,
		`SELECT coalesce(max(version),0) FROM prompt_versions
		WHERE component=?`,
		component,
	).Scan(&maxV); err != nil {
		return "", fmt.Errorf("pika/botmemory: max prompt version: %w", err)
	}
	return bm.UpsertPromptVersion(ctx, component, maxV+1, hash, content, "")
}

// LatestPromptVersion — prompt_id последней версии компонента
// («какой промпт действовал»). "" если версий ещё нет.
func (bm *BotMemory) LatestPromptVersion(
	ctx context.Context, component string,
) string {
	var id string
	if err := bm.db.QueryRowContext(ctx,
		`SELECT prompt_id FROM prompt_versions
		WHERE component=? ORDER BY version DESC LIMIT 1`,
		promptComponentName(component),
	).Scan(&id); err != nil {
		return ""
	}
	return id
}
