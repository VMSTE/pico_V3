package pika

// ТЗ-122 (срез М5): агентный цикл поиска по мотивам ReFind
// (arXiv 2608.12888 — сырая история + итеративный лексический цикл
// обошёл все структурные памяти; one-search контроль −8.5…−20.4 п.п.).
// Tool-сторона цикла: seen-set эпизода (повторные раунды скрывают уже
// показанное), ExecuteRounds для стенда, augmentQuery (термины из
// найденного → следующий запрос).
//
// Граница эпизода — по ЗАПРОСУ, не по времени: дословный повтор
// (пользователь переспросил) или новая тема → новый эпизод, фильтра
// НЕТ. Только переформулировка в той же теме → фильтр показанного.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	episodeTTL        = 15 * time.Minute
	episodeDupReset   = 0.85 // почти дословный повтор → новый эпизод
	episodeTopicReset = 0.30 // мало общих токенов → новая тема
	episodeAugmentTop = 3    // терминов дописываем в раунд 2+
	episodeRoundsCap  = 20
)

type searchEpisode struct {
	seed map[string]bool
	seen map[string]bool
	last time.Time
}

// episodeTokens — нормализованные токены (нижний регистр, ≥3 руны).
func episodeTokens(q string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(q)) {
		w = strings.Trim(w, `"'*?!.,:;()[]{}«»—–-`)
		if len([]rune(w)) >= 3 {
			out[w] = true
		}
	}
	return out
}

// episodeSim — два сходства: по max-множеству (ловит дословный повтор)
// и по min (ловит смену темы). Пустое множество → 0.
func episodeSim(a, b map[string]bool) (simMax, simMin float64) {
	if len(a) == 0 || len(b) == 0 {
		return 0, 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	mx, mn := len(a), len(b)
	if mn > mx {
		mx, mn = mn, mx
	}
	return float64(inter) / float64(mx), float64(inter) / float64(mn)
}

// episodeKey — стабильная идентичность показанного документа.
func episodeKey(r SearchResult) string {
	return fmt.Sprintf("%s|%d|%s|%.80s", r.Source, r.MsgID, r.AtomID, r.Summary)
}

// episodeFilter — граница эпизода + скрытие показанного. Зовётся ДО
// среза лимита, чтобы место доставалось новым документам.
func (ms *MemorySearch) episodeFilter(
	sessionID, query string, scored []SearchResult,
) []SearchResult {
	if sessionID == "" || len(scored) == 0 {
		return scored
	}
	tokens := episodeTokens(query)
	ms.episodesMu.Lock()
	defer ms.episodesMu.Unlock()
	if ms.episodes == nil {
		ms.episodes = map[string]*searchEpisode{}
	}
	ep := ms.episodes[sessionID]
	if ep != nil && time.Since(ep.last) <= episodeTTL {
		simMax, simMin := episodeSim(tokens, ep.seed)
		if simMax > episodeDupReset || simMin < episodeTopicReset {
			ep = nil // дословный повтор или новая тема → новый эпизод
		}
	} else {
		ep = nil
	}
	if ep == nil {
		ms.episodes[sessionID] = &searchEpisode{
			seed: tokens, seen: map[string]bool{}, last: time.Now(),
		}
		return scored // первый раунд эпизода — фильтра нет
	}
	out := make([]SearchResult, 0, len(scored))
	for _, r := range scored {
		if !ep.seen[episodeKey(r)] {
			out = append(out, r)
		}
	}
	for t := range tokens { // словарь эпизода растёт с переформулировками
		ep.seed[t] = true
	}
	ep.last = time.Now()
	return out
}

// episodeMark — отметить фактически показанное (ПОСЛЕ среза лимита:
// отрезанное не считается показанным и выживет в следующем раунде).
func (ms *MemorySearch) episodeMark(sessionID string, shown []SearchResult) {
	if sessionID == "" {
		return
	}
	ms.episodesMu.Lock()
	defer ms.episodesMu.Unlock()
	ep := ms.episodes[sessionID]
	if ep == nil {
		return
	}
	for _, r := range shown {
		ep.seen[episodeKey(r)] = true
	}
	ep.last = time.Now()
}

// ResetSearchEpisode — сброс эпизода (стенд: изоляция кейсов).
func (ms *MemorySearch) ResetSearchEpisode(sessionID string) {
	ms.episodesMu.Lock()
	defer ms.episodesMu.Unlock()
	delete(ms.episodes, sessionID)
}

// ExecuteRounds — симуляция агентного цикла на стенде: раунд 1 как
// обычно; раунды 2+ — запрос, дополненный частотными терминами из уже
// найденного. EpisodeFilter внутри Execute сам скрывает показанное.
// Слияние раундов: union по episodeKey, порядок по Score.
func (ms *MemorySearch) ExecuteRounds(
	ctx context.Context, args map[string]any, rounds int,
) *toolshared.ToolResult {
	res := ms.Execute(ctx, args)
	if rounds < 2 {
		return res
	}
	q0, _ := args["query"].(string)
	seen := map[string]bool{}
	var merged []SearchResult
	var r1 []SearchResult
	if err := json.Unmarshal([]byte(res.ForLLM), &r1); err == nil {
		for _, r := range r1 {
			k := episodeKey(r)
			if !seen[k] {
				seen[k] = true
				merged = append(merged, r)
			}
		}
	}
	for round := 2; round <= rounds; round++ {
		args2 := make(map[string]any, len(args)+1)
		for k, v := range args {
			args2[k] = v
		}
		args2["query"] = augmentQuery(q0, merged)
		resN := ms.Execute(ctx, args2)
		var rN []SearchResult
		if err := json.Unmarshal([]byte(resN.ForLLM), &rN); err != nil {
			continue
		}
		for _, r := range rN {
			k := episodeKey(r)
			if !seen[k] {
				seen[k] = true
				merged = append(merged, r)
			}
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})
	if len(merged) > episodeRoundsCap {
		merged = merged[:episodeRoundsCap]
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return res
	}
	return toolshared.SilentResult(string(out))
}

// augmentQuery — запрос + топ-N частотных терминов из найденного,
// которых не было в исходном запросе (детерминированно: частота,
// при равенстве — алфавит).
func augmentQuery(query string, results []SearchResult) string {
	base := episodeTokens(query)
	freq := map[string]int{}
	n := 5
	if len(results) < n {
		n = len(results)
	}
	for _, r := range results[:n] {
		for t := range episodeTokens(r.Summary) {
			if !base[t] && len([]rune(t)) >= 4 {
				freq[t]++
			}
		}
	}
	type tf struct {
		t string
		f int
	}
	tops := make([]tf, 0, len(freq))
	for t, f := range freq {
		tops = append(tops, tf{t, f})
	}
	sort.Slice(tops, func(i, j int) bool {
		if tops[i].f != tops[j].f {
			return tops[i].f > tops[j].f
		}
		return tops[i].t < tops[j].t
	})
	var add []string
	for i := 0; i < len(tops) && i < episodeAugmentTop; i++ {
		add = append(add, tops[i].t)
	}
	if len(add) == 0 {
		return query
	}
	return query + " " + strings.Join(add, " ")
}
