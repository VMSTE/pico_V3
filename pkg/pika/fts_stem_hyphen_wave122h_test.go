package pika

// Бенч v3 (2 окт): стем дефисного слова («MCP-серверы») ронял ВСЕ
// FTS-слои запроса («no such column: сервер»). Стем с не-буквами
// пропускаем; оригинальный терм в кавычках остаётся.

import (
	"strings"
	"testing"
)

func TestBuildFTSQuery_HyphenatedStemSafe(t *testing.T) {
	got := buildFTSQuery("MCP-серверы где крутятся")
	if !strings.Contains(got, `"mcp-серверы"`) {
		t.Fatalf("original quoted term missing: %q", got)
	}
	for _, term := range strings.Split(got, " OR ") {
		if !strings.HasPrefix(term, "\"") && strings.Contains(term, "-") {
			t.Fatalf("unquoted non-letter stem leaked: %q in %q", term, got)
		}
	}
}
