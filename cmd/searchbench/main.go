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
	"strings"

	"github.com/sipeed/picoclaw/pkg/pika"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

type benchCase struct {
	Q      string `json:"q"`
	Expect string `json:"expect"`
	Note   string `json:"note,omitempty"`
}

func main() {
	dbPath := flag.String("db", "", "path to a COPY of bot_memory.db")
	casesPath := flag.String("cases", "cmd/searchbench/cases.json", "cases file")
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
	// Стенд меряет всю базу, не один чат.
	if err := bm.SetMemoryScope(ctx, "bench", "all"); err != nil {
		fmt.Fprintf(os.Stderr, "scope: %v\n", err)
		os.Exit(1)
	}
	toolCtx := toolshared.WithToolSessionContext(ctx, "main", "bench", nil)
	ms := pika.NewMemorySearch(bm)

	hits := 0
	fmt.Printf("%-4s %-60s %-6s %s\n", "#", "query", "hit@5", "rank/layer")
	for i, c := range cases {
		res := ms.Execute(toolCtx, map[string]any{
			"query": c.Q, "limit": float64(10),
		})
		if res == nil || res.IsError {
			fmt.Printf("%-4d %-60s %-6s ERROR: %s\n", i+1, trunc(c.Q, 60), "-", res.ForLLM)
			continue
		}
		var results []pika.SearchResult
		if err := json.Unmarshal([]byte(res.ForLLM), &results); err != nil {
			fmt.Printf("%-4d %-60s %-6s PARSE: %v\n", i+1, trunc(c.Q, 60), "-", err)
			continue
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
		if mark != "OK" {
			for j := 0; j < len(results) && j < 5; j++ {
				fmt.Printf("     top%d [%s %.3f] %s\n",
					j+1, results[j].Type, results[j].Score, trunc(results[j].Summary, 90))
			}
		}
		fmt.Printf("%-4d %-60s %-6s rank=%d layer=%s\n",
			i+1, trunc(c.Q, 60), mark, rank, layer)
	}
	fmt.Printf("\nhit@5: %d/%d (%.0f%%)\n",
		hits, len(cases), 100*float64(hits)/float64(len(cases)))
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
