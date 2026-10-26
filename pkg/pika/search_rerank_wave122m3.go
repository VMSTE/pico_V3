package pika

// ТЗ-122 (срез М3): реранкер топ-20 после document-level фьюжна.
// Эмбеддинг/BM25 отвечают «похоже ли», реранкер — «содержит ли ответ»:
// LLM читает пары (вопрос, запись) и отличает эхо-вопрос от записи-факта.
// Проба 15 окт: цели кейсов 12/20 → ранг 1 у судьи при провале всех
// векторных манипуляций (3 модели × 3 промта × гигиена × HyDE × QuOTE).
// Любой сбой → исходный порядок: поиск не зависит от живости модели.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// rerankTopK — сколько лучших хитов уходит судье: вся зона, где
	// физически лежат цели-мишени стенда (ранги 6..18).
	rerankTopK = 30
	// rerankSnippetRunes — обрезка текста кандидата в промпте.
	rerankSnippetRunes  = 600
	rerankTimeout       = 25 * time.Second // окно 30: промпт x1.5, 8s не хватает (таймауты 10/23, бой 20 окт)
	rerankDefaultModel  = "google/gemini-2.5-flash"
	rerankMinAcceptable = 3
)

// RankCaller вызывает LLM-судью. Отдельный тип: прод втыкает background-
// модель, стенд — HTTP в OpenRouter, тесты — мок.
type RankCaller func(ctx context.Context, sysPrompt, userContent string) (string, error)

// SetReranker подключает реранкер. nil = выключен (дефолт).
func (ms *MemorySearch) SetReranker(c RankCaller) { ms.reranker = c }

// NewOpenRouterRankCaller собирает RankCaller поверх OpenRouter-
// совместимого chat/completions. Пустой ключ → nil (слой молча выкл).
func NewOpenRouterRankCaller(apiBase, apiKey, model string) RankCaller {
	return NewOpenRouterLLMCaller(apiBase, apiKey, model, rerankTimeout)
}

// NewOpenRouterLLMCaller — то же с явным таймаутом.
func NewOpenRouterLLMCaller(
	apiBase, apiKey, model string, timeout time.Duration,
) RankCaller {
	if apiKey == "" {
		return nil
	}
	if apiBase == "" {
		apiBase = "https://openrouter.ai/api/v1"
	}
	if model == "" {
		model = rerankDefaultModel
	}
	cli := &http.Client{Timeout: timeout}
	return func(ctx context.Context, sysPrompt, userContent string) (string, error) {
		body, _ := json.Marshal(map[string]any{
			"model":       model,
			"temperature": 0, // судья холодный: на дефолтной t=1.0 флипы 18↔20/23 (бой 20 окт)
			"messages": []map[string]string{
				{"role": "system", "content": sysPrompt},
				{"role": "user", "content": userContent},
			},
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			apiBase+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := cli.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return "", err
		}
		if resp.StatusCode/100 != 2 {
			return "", fmt.Errorf("rerank LLM: status %d: %.200s", resp.StatusCode, raw)
		}
		var parsed struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return "", err
		}
		if len(parsed.Choices) == 0 {
			return "", fmt.Errorf("rerank LLM: пустой ответ")
		}
		return parsed.Choices[0].Message.Content, nil
	}
}

// NewOpenRouterQgenCaller — для генератора вопросов (М4): пачка из
// qgenBatchDocs документов генерится дольше судейских 8s.
func NewOpenRouterQgenCaller(apiBase, apiKey, model string) RankCaller {
	return NewOpenRouterLLMCaller(apiBase, apiKey, model, 3*time.Minute)
}

const rerankSysPrompt = "Ты — реранкер памяти личного ассистента. " +
	"Тебе дают вопрос пользователя и записи из его истории. Отранжируй " +
	"записи по тому, насколько запись СОДЕРЖИТ ОТВЕТ на вопрос: конкретный " +
	"факт, решение, значение. Записи, которые сами являются вопросами или " +
	"лишь повторяют тему без ответа, ставь НИЖЕ записей с фактами. Если в " +
	"вопросе назван период (месяц, даты, «до/после»), записи с датой ВНУТРИ " +
	"этого периода ставь выше записей вне его. Верни " +
	"ТОЛЬКО номера записей через запятую, от самой отвечающей к наименее."

// maybeRerank переставляет топ выдачи по мнению LLM-судьи. Никогда не
// портит выдачу: nil-реранкер, мало хитов, сбой вызова, мусор в ответе
// → исходный порядок.
func (ms *MemorySearch) maybeRerank(
	ctx context.Context, query string, scored []SearchResult,
) []SearchResult {
	if ms.reranker == nil || len(scored) <= 5 || strings.TrimSpace(query) == "" {
		return scored
	}
	head := scored
	var tail []SearchResult
	if len(head) > rerankTopK {
		head, tail = head[:rerankTopK], scored[rerankTopK:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Вопрос пользователя: %s\n\nЗаписи:\n", query)
	for i, r := range head {
		snippet := r.Summary
		// М7.4-fix: реранкер получает ядро документа БЕЗ контекст-суффикса
		// «↕ контекст» — паттерн compressed representation for reranking
		// (arXiv:2505.15394): разбавленный длинный вход деградирует
		// ранжирование (arXiv:2512.14313; бой кейса 5). Полный контекст
		// остаётся в финальной выдаче и у answer-читателя.
		if k := strings.Index(snippet, "\n↕ контекст:"); k >= 0 {
			snippet = snippet[:k]
		}
		if rr := []rune(snippet); len(rr) > rerankSnippetRunes {
			snippet = string(rr[:rerankSnippetRunes]) + "…"
		}
		date := r.CreatedAt
		if len(date) > 10 {
			date = date[:10]
		}
		fmt.Fprintf(&b, "\n[%d] (%s/%s, %s) %s\n", i+1, r.Type, r.Source, date, snippet)
	}
	sys := rerankSysPrompt
	if wAfter, wBefore, _, tok := ParseTemporalWindow(query, time.Now()); tok {
		sys += " Вопрос привязан к периоду " + wAfter + ".." + wBefore +
			": записи ИЗ ЭТОГО ПЕРИОДА — кандидаты в ответ про этот период, " +
			"оценивай их содержание в контексте периода даже при слабом " +
			"лексическом пересечении; записи вне периода без явного ответа " +
			"ставь НИЖЕ."
	}
	raw, err := ms.reranker(ctx, sys, b.String())
	if err != nil {
		log.Printf("pika rerank: %v (исходный порядок)", err)
		return scored
	}
	order := parseRerankOrder(raw, len(head))
	if order == nil {
		log.Printf("pika rerank: нераспарсенный ответ %.80q (исходный порядок)", raw)
		return scored
	}
	out := make([]SearchResult, 0, len(scored))
	seen := make([]bool, len(head))
	for _, idx := range order {
		out = append(out, head[idx])
		seen[idx] = true
	}
	for i := range head { // неназванные — в хвост в исходном порядке
		if !seen[i] {
			out = append(out, head[i])
		}
	}
	return append(out, tail...)
}

// parseRerankOrder извлекает перестановку индексов 0..n-1 из ответа
// судьи. Меньше rerankMinAcceptable валидных номеров → nil (мусор).
func parseRerankOrder(raw string, n int) []int {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r < '0' || r > '9'
	})
	seen := make(map[int]bool, n)
	var order []int
	for _, f := range fields {
		v, err := strconv.Atoi(f)
		if err != nil || v < 1 || v > n || seen[v-1] {
			continue
		}
		seen[v-1] = true
		order = append(order, v-1)
	}
	if len(order) < rerankMinAcceptable {
		return nil
	}
	return order
}
