// searchbench (ТЗ-122, срез А): стенд качества поиска по памяти.
// Прогоняет кейсы (запрос → ожидаемая подстрока) через НАСТОЯЩИЙ
// search_memory (все слои, over-fetch, дедуп, scoreResults — как в проде)
// поверх КОПИИ живой базы. Метрика: hit@5 на кейс + доля. Базовая линия
// обязательна до любых изменений поиска; каждый срез обязан её поднимать
// или объяснять, почему нет (гейт ТЗ-122).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/pika"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

type benchCase struct {
	Q      string `json:"q"`
	Expect string `json:"expect"`
	Note   string `json:"note,omitempty"`
	After  string `json:"after,omitempty"`
	Before string `json:"before,omitempty"`
}

func main() {
	dbPath := flag.String("db", "", "path to a COPY of bot_memory.db")
	casesPath := flag.String("cases", "cmd/searchbench/cases.json", "cases file")
	embed := flag.Bool(
		"embed",
		false,
		"backfill embeddings on the copy (OPENROUTER_API_KEY env required)",
	)
	vec := flag.Bool("vec", false, "enable vector leg (OPENROUTER_API_KEY env required)")
	vecWeight := flag.Float64("vecweight", 1.0, "vector leg weight multiplier (calibration grid)")
	vecProbe := flag.Bool(
		"vecprobe",
		false,
		"print raw KNN distances per case (needs OPENROUTER_API_KEY)",
	)
	rerank := flag.Bool(
		"rerank",
		false,
		"М3: LLM-реранк топ-20 после фьюжна (OPENROUTER_API_KEY; модель из PIKA_RERANK_MODEL)",
	)
	qgen := flag.Bool(
		"qgen",
		false,
		"М4: бэкфилл вопросной индексации (OPENROUTER_API_KEY; модель из PIKA_QGEN_MODEL)",
	)
	rounds := flag.Int("rounds", 1, "М5: поисковых раундов на кейс (агентный цикл, ReFind-style)")
	vecMaxDist := flag.Float64("vecmaxdist", 0, "vector leg L2 distance cutoff (0 = off)")
	reader := flag.Bool(
		"reader",
		false,
		"М6: Читатель — извлечение значения из топ-пула + голосование (OPENROUTER_API_KEY; модель PIKA_READER_MODEL, фолбэк PIKA_RERANK_MODEL)",
	)
	answer := flag.Bool(
		"answer",
		false,
		"М7: answer-level метрика — сильная модель отвечает по пулу, холодный судья сверяет с expect (OPENROUTER_API_KEY; модель PIKA_ANSWER_MODEL)",
	)
	hyde := flag.Bool(
		"hyde",
		false,
		"М7.2: query expansion — LLM пишет правдоподобный фрагмент ответа, он дописывается к запросу (OPENROUTER_API_KEY; модель PIKA_HYDE_MODEL, фолбэк PIKA_RERANK_MODEL)",
	)
	flag.Parse()
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "usage: searchbench -db /tmp/bench.db [-cases cases.json]")
		os.Exit(2)
	}

	data, err := os.ReadFile(*casesPath) // #nosec G304 -- dev tool, own repo
	if err != nil {
		fmt.Fprintf(os.Stderr, "read cases: %v\n", err)
		os.Exit(1)
	}
	var cases []benchCase
	if pErr := json.Unmarshal(data, &cases); pErr != nil {
		fmt.Fprintf(os.Stderr, "parse cases: %v\n", pErr)
		os.Exit(1)
	}

	db, err := pika.Migrate(*dbPath) // идемпотентно на копии
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	bm, err := pika.NewBotMemory(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "botmemory: %v\n", err)
		os.Exit(1)
	}
	defer bm.Close()

	ctx := context.Background()

	// ТЗ-122 (срез М1): векторный бэкфилл копии — готовит стенд к М2.
	if *embed {
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			fmt.Fprintln(os.Stderr, "-embed: OPENROUTER_API_KEY is not set")
			os.Exit(1)
		}
		emb := pika.NewEmbedder("", key, os.Getenv("PIKA_EMBED_MODEL"))
		for {
			n, eErr := bm.EmbedPending(ctx, emb, 64)
			if eErr != nil {
				fmt.Fprintf(os.Stderr, "embed: %v\n", eErr)
				os.Exit(1)
			}
			fmt.Printf("embedded batch: %d\n", n)
			if n == 0 {
				break
			}
		}
		for {
			n, eErr := bm.EmbedPendingReasoning(ctx, emb, 64)
			if eErr != nil {
				fmt.Fprintf(os.Stderr, "embed reasoning: %v\n", eErr)
				os.Exit(1)
			}
			fmt.Printf("embedded reasoning batch: %d\n", n)
			if n == 0 {
				break
			}
		}
		done, pend, cErr := bm.EmbeddingCoverage(ctx)
		if cErr == nil {
			fmt.Printf("embeddings coverage: %d embedded, %d pending\n", done, pend)
		}
	}
	// Стенд меряет всю базу, не один чат.
	if *qgen {
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			fmt.Fprintln(os.Stderr, "-qgen: OPENROUTER_API_KEY is not set")
			os.Exit(1)
		}
		caller := pika.NewOpenRouterQgenCaller("", key, os.Getenv("PIKA_QGEN_MODEL"))
		for {
			n, gErr := bm.GenerateQuestionsPending(ctx, caller, 0)
			if gErr != nil {
				fmt.Fprintf(os.Stderr, "qgen: %v\n", gErr)
				os.Exit(1)
			}
			fmt.Printf("questioned batch: %d\n", n)
			if n == 0 {
				break
			}
		}
		if dn, cErr := bm.QuestionCoverage(ctx); cErr == nil {
			fmt.Printf("questions coverage: %d docs\n", dn)
		}
	}

	if err := bm.SetMemoryScope(ctx, "bench", "all"); err != nil {
		fmt.Fprintf(os.Stderr, "scope: %v\n", err)
		os.Exit(1)
	}
	toolCtx := toolshared.WithToolSessionContext(ctx, "main", "bench", nil)
	ms := pika.NewMemorySearch(bm)
	if *rerank {
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			fmt.Fprintln(os.Stderr, "-rerank: OPENROUTER_API_KEY is not set")
			os.Exit(1)
		}
		ms.SetReranker(pika.NewOpenRouterRankCaller("", key, os.Getenv("PIKA_RERANK_MODEL")))
	}

	var readerCaller pika.RankCaller
	if *reader {
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			fmt.Fprintln(os.Stderr, "-reader: OPENROUTER_API_KEY is not set")
			os.Exit(1)
		}
		model := os.Getenv("PIKA_READER_MODEL")
		if model == "" {
			model = os.Getenv("PIKA_RERANK_MODEL")
		}
		readerCaller = pika.NewOpenRouterRankCaller("", key, model)
		fmt.Println("reader: ON")
	}

	var answerCaller pika.RankCaller
	var judgeCaller pika.RankCaller
	if *answer {
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			fmt.Fprintln(os.Stderr, "-answer: OPENROUTER_API_KEY is not set")
			os.Exit(1)
		}
		model := os.Getenv("PIKA_ANSWER_MODEL")
		if model == "" {
			model = os.Getenv("PIKA_RERANK_MODEL")
		}
		answerCaller = pika.NewOpenRouterRankCaller("", key, model)
		judgeModel := os.Getenv("PIKA_JUDGE_MODEL")
		if judgeModel == "" {
			judgeModel = model
		}
		judgeCaller = pika.NewOpenRouterRankCaller("", key, judgeModel)
		fmt.Printf("answer: ON (model %s, judge %s)\n", model, judgeModel)
	}

	var hydeCaller pika.RankCaller
	if *hyde {
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			fmt.Fprintln(os.Stderr, "-hyde: OPENROUTER_API_KEY is not set")
			os.Exit(1)
		}
		model := os.Getenv("PIKA_HYDE_MODEL")
		if model == "" {
			model = os.Getenv("PIKA_RERANK_MODEL")
		}
		hydeCaller = pika.NewOpenRouterRankCaller("", key, model)
		fmt.Printf("hyde: ON (model %s)\n", model)
	}
	// М2.1: белый список источников векторной ноги (PIKA_VEC_SOURCES=reasoning).
	if srcs := os.Getenv("PIKA_VEC_SOURCES"); srcs != "" {
		ms.SetVectorSources(strings.Split(srcs, ","))
	}
	// ТЗ-122 (срез М2): векторная нога на стенде + ручка веса для сетки.
	if *vec {
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			fmt.Fprintln(os.Stderr, "-vec: OPENROUTER_API_KEY is not set")
			os.Exit(1)
		}
		ms.SetEmbedder(pika.NewEmbedder("", key, os.Getenv("PIKA_EMBED_MODEL")))
		ms.SetVectorWeight(*vecWeight)
		ms.SetVectorMaxDist(*vecMaxDist)
		fmt.Printf("vector leg: ON (weight %.2f, maxdist %.2f)\n", *vecWeight, *vecMaxDist)
	}
	// ТЗ-122 (срез М2): проба дистанций — где живут цели vs мусор.
	if *vecProbe {
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			fmt.Fprintln(os.Stderr, "-vecprobe: OPENROUTER_API_KEY is not set")
			os.Exit(1)
		}
		ms.SetEmbedder(pika.NewEmbedder("", key, os.Getenv("PIKA_EMBED_MODEL")))
		for i, c := range cases {
			hits, pErr := ms.VectorProbe(ctx, c.Q)
			if pErr != nil {
				fmt.Printf("case %d: probe error: %v\n", i+1, pErr)
				continue
			}
			fmt.Printf("case %d [%s]:\n", i+1, trunc(c.Q, 50))
			for j, h := range hits {
				mark := ""
				if strings.Contains(strings.ToLower(h.Summary), strings.ToLower(c.Expect)) {
					mark = "   <<< EXPECT"
				}
				fmt.Printf("  #%02d d=%.4f [%s:%d] %s%s\n",
					j+1, h.Score, h.Source, h.MsgID, trunc(h.Summary, 60), mark)
			}
		}
		return
	}

	hits := 0
	readerHits := 0
	readerStrictHits := 0
	answerHits := 0
	answerStrictHits := 0
	fmt.Printf("%-4s %-60s %-6s %s\n", "#", "query", "hit@5", "rank/layer")
	for i, c := range cases {
		// М2-аудит: меряем документ целиком, а не 200-символьный сниппет
		ms.ResetSearchEpisode("bench")
		args := map[string]any{"query": c.Q, "limit": float64(30), "full": true}
		if c.After != "" {
			args["after"] = c.After
		}
		if c.Before != "" {
			args["before"] = c.Before
		}
		args["full"] = true // кейс 23: answer-стадии полный текст, не заголовок
		res := ms.ExecuteRounds(toolCtx, args, *rounds)
		if res == nil || res.IsError {
			fmt.Printf("%-4d %-60s %-6s ERROR: %s\n", i+1, trunc(c.Q, 60), "-", res.ForLLM)
			continue
		}
		var results []pika.SearchResult
		if err := json.Unmarshal([]byte(res.ForLLM), &results); err != nil {
			fmt.Printf("%-4d %-60s %-6s PARSE: %v\n", i+1, trunc(c.Q, 60), "-", err)
			continue
		}
		if hydeCaller != nil {
			frag, hErr := hydeCaller(ctx, hydeSysPrompt, c.Q)
			if hErr != nil {
				fmt.Printf("     hyde: ERROR %v\n", hErr)
			} else {
				ms.ResetSearchEpisode("bench-hyde")
				args2 := map[string]any{"query": strings.TrimSpace(frag), "limit": float64(30), "full": true}
				if c.After != "" {
					args2["after"] = c.After
				}
				if c.Before != "" {
					args2["before"] = c.Before
				}
				args2["full"] = true // кейс 23: answer-стадии полный текст, не заголовок
				res2 := ms.ExecuteRounds(toolCtx, args2, *rounds)
				if res2 != nil && !res2.IsError {
					var results2 []pika.SearchResult
					if err := json.Unmarshal([]byte(res2.ForLLM), &results2); err == nil {
						results = rrfMergeLists(results, results2, 0.4)
					}
				}
			}
		}
		rank, layer := -1, ""
		for j, r := range results {
			if strings.Contains(
				strings.ToLower(r.Summary), strings.ToLower(c.Expect)) {
				rank, layer = j+1, r.Type
				break
			}
		}
		mark := "MISS"
		if rank >= 1 && rank <= 5 {
			mark = "OK"
			hits++
		} else if rank > 5 {
			mark = "LOW" // нашлось, но ниже топ-5
		}
		types := map[string]int{}
		for _, r := range results {
			types[r.Type]++
		}
		fmt.Printf("     слои в выдаче: %v (всего %d)\n", types, len(results))
		if mark != "OK" {
			for j := 0; j < len(results); j++ {
				fmt.Printf(
					"     top%d [%s/%s %.3f] %s\n",
					j+1,
					results[j].Type,
					results[j].Source,
					results[j].Score,
					trunc(results[j].Summary, 90),
				)
			}
		}
		if readerCaller != nil {
			raw, rErr := readerCaller(ctx, readerSysPrompt, readerPrompt(c.Q, results))
			if rErr != nil {
				fmt.Printf("     reader: ERROR %v\n", rErr)
			} else {
				value := parseReaderAnswer(raw)
				if value == "" {
					fmt.Printf("     reader: NOVALUE (raw: %s)\n", trunc(raw, 80))
				} else {
					strict := strings.Contains(strings.ToLower(value), strings.ToLower(c.Expect))
					judged := judgeReaderValue(ctx, readerCaller, c.Q, c.Expect, value)
					if strict {
						readerStrictHits++
					}
					if judged {
						readerHits++
					}
					fmt.Printf("     reader: judge=%v strict=%v → %s\n", judged, strict, trunc(value, 80))
				}
			}
		}
		if answerCaller != nil {
			var raw string
			var aErr error
			for attempt := 0; attempt < 3; attempt++ {
				raw, aErr = answerCaller(ctx, answerSysPrompt, readerPrompt(c.Q, results))
				if aErr == nil {
					break
				}
				fmt.Printf("    answer err (попытка %d): %v\n", attempt+1, aErr)
				time.Sleep(time.Duration(5+attempt*7) * time.Second)
			}
			if aErr != nil {
				fmt.Printf("     answer: ERROR %v\n", aErr)
			} else {
				ans := strings.TrimSpace(raw)
				strict := strings.Contains(strings.ToLower(ans), strings.ToLower(c.Expect))
				judged := judgeReaderValue(ctx, judgeCaller, c.Q, c.Expect, ans)
				if strict {
					answerStrictHits++
				}
				if judged {
					answerHits++
				}
				fmt.Printf("     answer: judge=%v strict=%v → %s\n", judged, strict, trunc(ans, 1500))
			}
		}
		fmt.Printf("%-4d %-60s %-6s rank=%d layer=%s\n",
			i+1, trunc(c.Q, 60), mark, rank, layer)
	}
	fmt.Printf("\nhit@5: %d/%d (%.0f%%)\n",
		hits, len(cases), 100*float64(hits)/float64(len(cases)))
	if readerCaller != nil {
		fmt.Printf("value-hit(reader/judged): %d/%d (%.0f%%)\n",
			readerHits, len(cases), 100*float64(readerHits)/float64(len(cases)))
		fmt.Printf("value-hit(reader/strict): %d/%d (%.0f%%)\n",
			readerStrictHits, len(cases), 100*float64(readerStrictHits)/float64(len(cases)))
	}
	if answerCaller != nil {
		fmt.Printf("answer-hit(judged): %d/%d (%.0f%%)\n",
			answerHits, len(cases), 100*float64(answerHits)/float64(len(cases)))
		fmt.Printf("answer-hit(strict): %d/%d (%.0f%%)\n",
			answerStrictHits, len(cases), 100*float64(answerStrictHits)/float64(len(cases)))
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

const (
	readerTopK         = 30   // Читатель ест весь пул, который выдал пайплайн
	readerSnippetRunes = 1800 // зеркально rerankSnippetRunes
)

const readerSysPrompt = "Ты — Читатель памяти личного ассистента. Тебе дают " +
	"вопрос пользователя и записи из его истории. Извлеки ответ на вопрос, " +
	"даже если он размазан по нескольким записям. Правила: собери значения-" +
	"кандидатов из записей; если кандидатов несколько, проголосуй — побеждает " +
	"значение с наибольшей поддержкой (больше записей; при равенстве — более " +
	"свежее по дате). Если ответа нет ни в одной записи — верни пустой answer. " +
	"Отвечай короткой фразой: значение плюс суть; ключевые термины " +
	"(имена, числа с единицами, места) — дословно как в записях, не одним числом. " +
	"Верни ТОЛЬКО JSON без пояснений: " +
	"{\"answer\": \"<значение или пусто>\", \"candidates\": [{\"value\": \"...\", \"support\": <число записей>}]}"

func readerPrompt(q string, results []pika.SearchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Вопрос: %s\n\nЗаписи:\n", q)
	n := len(results)
	if n > readerTopK {
		n = readerTopK
	}
	for j := 0; j < n; j++ {
		r := results[j]
		fmt.Fprintf(&b, "[%d] (%s, %s) %s\n",
			j+1, r.Type, r.CreatedAt, trunc(r.Summary, readerSnippetRunes))
	}
	return b.String()
}

// parseReaderAnswer достаёт answer из JSON-ответа Читателя. Невалидный
// JSON = NOVALUE: подглядывать подстрокой expect в сырой текст нельзя —
// это надувало бы метрику.
// parseReaderAnswer достаёт answer из JSON-ответа Читателя: чистый JSON,
// фенсы ```json, наконец регэксп по полю. Подглядывать подстрокой expect
// в сырой текст нельзя — это надувало бы метрику.
func parseReaderAnswer(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	lo := strings.Index(s, "{")
	hi := strings.LastIndex(s, "}")
	if lo >= 0 && hi > lo {
		var v struct {
			Answer string `json:"answer"`
		}
		if err := json.Unmarshal([]byte(s[lo:hi+1]), &v); err == nil {
			return strings.TrimSpace(v.Answer)
		}
	}
	re := regexp.MustCompile(`"answer"\s*:\s*"((?:[^"\\]|\\.)*)`)
	m := re.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	unq, err := strconv.Unquote(`"` + m[1] + `"`)
	if err != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(unq)
}

const readerJudgeSysPrompt = "Ты — строгий проверяющий. Тебе дают вопрос, " +
	"эталонный ключевой факт и ответ. Засчитывай HIT, только если выполнены " +
	"ОБА условия: (1) ответ отвечает ИМЕННО на заданный вопрос — та же тема, " +
	"сущность и период; правдоподобный ответ про другое = MISS, даже если " +
	"эталонные слова в нём встречаются; (2) ответ содержит эталонный факт " +
	"по смыслу. Формулировка может отличаться; факт НЕ обязан быть " +
	"главной мыслью ответа — достаточно упоминания в любом месте; " +
	"Если точных данных в записях не было и ответ честно говорит об этом, " +
	"приводя факты по теме вопроса и упоминая эталонный факт, — это HIT. " +
	"дополнительные факты рядом с эталонным не мешают. " +
	"Верни одно слово HIT или MISS. Никаких пояснений."

// judgeReaderValue — холодный судья reader-метрики: смысловое совпадение
// извлечённого значения с эталоном кейса.
func judgeReaderValue(ctx context.Context, caller pika.RankCaller, q, expect, value string) bool {
	// majority-vote 2/3: одиночный судья флипает на пограничных ответах
	// (кейс 11: «credentials» дословно в ответе, verdict=false).
	votes := 0
	for i := 0; i < 3; i++ {
		raw, err := caller(ctx, readerJudgeSysPrompt,
			fmt.Sprintf("Вопрос: %s\nЭталон: %s\nОтвет: %s", q, expect, value))
		if err != nil {
			fmt.Printf("    judge vote err: %v\n", err)
			continue
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(raw)), "HIT") {
			votes++
		} else {
			fmt.Printf("    judge noHIT: %.80q\n", raw)
		}
	}
	return votes >= 2
}

const answerSysPrompt = "Ты — личный ассистент с памятью. Тебе дают вопрос " +
	"пользователя и записи из его памяти. Ответь на вопрос, опираясь ТОЛЬКО " +
	"на эти записи. Просмотри ВСЕ записи до конца: нужный факт может быть не в первых, а в середине или конце списка. Если ответ размазан по нескольким записям — синтезируй " +
	"его из них. Если в записях есть факты по теме (числа, названия, решения) — приведи их, даже если неполные; «нет данных» — только если записи вообще не о вопросе. " +
	"Отвечай кратко (1-3 предложения); ключевые значения (имена, числа, " +
	"названия, даты) приводи дословно, как в записях. Если в вопросе назван " +
	"период (месяц, «в начале», «до/после …») — отвечай ТОЛЬКО по записям этого " +
	"периода, ориентируясь на даты записей; записи вне периода игнорируй. " +
	"Если пользователь повторял одну и ту же просьбу в нескольких записях " +
	"— это сильный сигнал: обязательно назови, что именно он просил, дословно. Если тема или " +
	"заголовок записи прямо отвечает на вопрос — процитируй дословно, даже " +
	"если содержание пересказываешь своими словами. Подходящих фактов несколько " +
	"— перечисли все, а не один. В конце ответа укажи номера всех " +
	"использованных записей в квадратных скобках, например [2], [14]."

const hydeSysPrompt = "Пользователь спрашивает память личного ассистента. " +
	"Напиши короткий (2-3 предложения) правдоподобный фрагмент из истории " +
	"переписки, который мог бы содержать ответ на этот вопрос. Используй " +
	"конкретные слова, имена, термины и названия, которые вероятны в таком " +
	"фрагменте. Верни ТОЛЬКО фрагмент, без пояснений и кавычек."

// rrfMergeLists — probe C: мерж выдачи оригинального запроса (вес 1.0) и
// hyde-запроса (вес wb) ПОСЛЕ реранка. RRF k=60 зеркально движку.
// Документ входит в топ-5 только обосновавшись высоко в обоих списках.
func rrfMergeLists(a, b []pika.SearchResult, wb float64) []pika.SearchResult {
	type acc struct {
		score float64
		rep   pika.SearchResult
	}
	merged := map[string]*acc{}
	order := []string{}
	add := func(list []pika.SearchResult, w float64) {
		for i, r := range list {
			k := r.Source + "|" + r.Type + "|" + strconv.FormatInt(r.MsgID, 10) + "|" + r.AtomID
			if e, ok := merged[k]; ok {
				e.score += w / (60.0 + float64(i) + 1.0)
			} else {
				merged[k] = &acc{score: w / (60.0 + float64(i) + 1.0), rep: r}
				order = append(order, k)
			}
		}
	}
	add(a, 1.0)
	add(b, wb)
	sort.SliceStable(order, func(x, y int) bool {
		return merged[order[x]].score > merged[order[y]].score
	})
	out := make([]pika.SearchResult, 0, len(order))
	for _, k := range order {
		r := merged[k].rep
		r.Score = merged[k].score
		out = append(out, r)
	}
	return out
}
