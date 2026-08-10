package updater

import (
	"fmt"
	"strings"
)

// ValidateReleaseSource отклоняет чужие источники обновления (D-AUDIT-76,
// класс GO-2026-5187). /api/update принимает URL и имя бинаря из запроса —
// без проверки это накат произвольного бинаря с чужого хоста через selfupdate.
//
// Правила: URL пустой (дефолт) ИЛИ начинается с нашего prod release API;
// имя бинаря без path separators и traversal.
func ValidateReleaseSource(releaseURL, binary string) error {
	if releaseURL != "" && !strings.HasPrefix(releaseURL, GetProdReleaseAPIURL()) {
		return fmt.Errorf(
			"release URL must point to the official release source (%s)",
			GetProdReleaseAPIURL(),
		)
	}
	if binary == "" {
		return fmt.Errorf("binary name is required")
	}
	if strings.ContainsAny(binary, "/\\") || strings.Contains(binary, "..") {
		return fmt.Errorf("binary name must not contain path separators or '..'")
	}
	return nil
}
