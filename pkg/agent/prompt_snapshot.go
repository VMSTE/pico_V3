package agent

import (
	"regexp"
	"strings"
)

// Волна 124 (срез В, ТЗ-124): честный расклад токенов снапшота.
// Секции собранного промпта помечены заголовками "--- NAME ---"
// (контрибьюторы). Всё до первого маркера — core; MEMORY BRIEF и
// RECOMMENDED * — brief; TRAIL — trail; ACTIVE_PLAN — plan;
// неизвестные секции — context. Оценка len/4, как везде в пайплайне.
// Нет маркеров → весь промпт core (поведение волны 92).
var promptSectionRe = regexp.MustCompile(`---\s*([A-Z][A-Z0-9_ ]*?)\s*---`)

func promptTokenBreakdown(sp string) map[string]int {
	tokens := map[string]int{
		"core": 0, "context": 0, "brief": 0, "trail": 0, "plan": 0,
	}
	locs := promptSectionRe.FindAllStringSubmatchIndex(sp, -1)
	if len(locs) == 0 {
		tokens["core"] = len(sp) / 4
		return tokens
	}
	tokens["core"] = len(sp[:locs[0][0]]) / 4
	for i, loc := range locs {
		end := len(sp)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		name := sp[loc[2]:loc[3]]
		bucket := "context"
		switch {
		case strings.Contains(name, "MEMORY BRIEF"),
			strings.Contains(name, "RECOMMENDED"):
			bucket = "brief"
		case strings.Contains(name, "TRAIL"):
			bucket = "trail"
		case strings.Contains(name, "ACTIVE_PLAN"),
			strings.Contains(name, "ACTIVE PLAN"):
			bucket = "plan"
		}
		tokens[bucket] += (end - loc[0]) / 4
	}
	return tokens
}
