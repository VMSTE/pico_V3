package pika

import "strings"

// dedupeMemoryBrief удаляет дубли строк секций брифа, сохраняя порядок.
// Волна 124 (бой 28 сен, trace_spans): одна строка AVOID пришла трижды
// подряд — промптное «без дублей» LLM не удержал, дедупит Go.
func dedupeMemoryBrief(b *MemoryBrief) {
	b.Avoid = dedupeStrings(b.Avoid)
	b.Constraints = dedupeStrings(b.Constraints)
	b.Prefer = dedupeStrings(b.Prefer)
	b.Context = dedupeStrings(b.Context)
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		k := strings.TrimSpace(s)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	return out
}
