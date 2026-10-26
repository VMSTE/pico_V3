package pika

// М7.3: temporal window leg. Детерминированный парсер темпоральных
// ссылок в запросе («в мае», «в начале июня», «март 2026») + нога
// retrieval: сообщения И события окна по ts БЕЗ лексического матча.
// Ноль LLM. «Что мы делали» — про действия: events (type/outcome) —
// память дел, messages — память разговоров; окно несёт оба.

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// prioTemporal — половина веса messages: нога — сигнал корроборации,
// не замена лексике (урок flooding из ветки М7.2).
// W2: окно активно ТОЛЬКО при явном темпоральном маркере в запросе,
// значит период — заявленная релевантность, а не шум: полный вес.
var prioTemporal = prioMessages * 1.3 // явно названный период — сильнейший сигнал (кейс 10)

// temporalPool — константный пул ноги (как messages=100, knowledge=40).
const temporalPool = 30

// Русские месяцы во всех падежах (стемы, word boundary).
var monthPats = []struct {
	re *regexp.Regexp
	m  int
}{
	{regexp.MustCompile(`(?:^|[^\pL])январ[ьяею](?:[^\pL]|$)`), 1},
	{regexp.MustCompile(`(?:^|[^\pL])феврал[ьяею](?:[^\pL]|$)`), 2},
	{regexp.MustCompile(`(?:^|[^\pL])март[аеу]?(?:[^\pL]|$)`), 3},
	{regexp.MustCompile(`(?:^|[^\pL])апрел[ьяею](?:[^\pL]|$)`), 4},
	{regexp.MustCompile(`(?:^|[^\pL])ма[йяею](?:[^\pL]|$)`), 5},
	{regexp.MustCompile(`(?:^|[^\pL])июн[ьяею](?:[^\pL]|$)`), 6},
	{regexp.MustCompile(`(?:^|[^\pL])июл[ьяею](?:[^\pL]|$)`), 7},
	{regexp.MustCompile(`(?:^|[^\pL])август[аеу]?(?:[^\pL]|$)`), 8},
	{regexp.MustCompile(`(?:^|[^\pL])сентябр[ьяею](?:[^\pL]|$)`), 9},
	{regexp.MustCompile(`(?:^|[^\pL])октябр[ьяею](?:[^\pL]|$)`), 10},
	{regexp.MustCompile(`(?:^|[^\pL])ноябр[ьяею](?:[^\pL]|$)`), 11},
	{regexp.MustCompile(`(?:^|[^\pL])декабр[ьяею](?:[^\pL]|$)`), 12},
}
var yearRe = regexp.MustCompile(`\b(20\d\d)\b`)

// Маркеры направления: «в самом начале, первые» → ранние записи окна.
var ascRe = regexp.MustCompile(`начал|перв|ранн`)
var descRe = regexp.MustCompile(`конец|конц|последн|недавн|свеж`)

// ParseTemporalWindow — детерминированный парсер темпоральной ссылки.
// Год: явный из запроса, иначе ближайший не-будущий разворот месяца
// («май» в марте → прошлый май). Возвращает [after, before) YYYY-MM-DD,
// направление asc и флаг наличия маркера.
func ParseTemporalWindow(query string, now time.Time) (after, before string, asc, ok bool) {
	q := strings.ToLower(query)
	month := 0
	for _, mp := range monthPats {
		if mp.re.MatchString(q) {
			month = mp.m
			break
		}
	}
	if month == 0 {
		return "", "", false, false
	}
	year := now.Year()
	if m := yearRe.FindStringSubmatch(q); m != nil {
		if y, err := strconv.Atoi(m[1]); err == nil {
			year = y
		}
	} else if month > int(now.Month()) {
		year--
	}
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	if ascRe.MatchString(q) && !descRe.MatchString(q) {
		asc = true
	}
	return start.Format("2006-01-02"), end.Format("2006-01-02"), asc, true
}

// searchTemporal — нога М7.3: сообщения и события темпорального окна.
// Синтетический RawBM25 = позиция в общем ts-порядке: scoreResults для
// FTS-слоёв ранжирует «меньше = лучше», порядок доезжает до RRF как есть.
func (ms *MemorySearch) searchTemporal(
	ctx context.Context,
	after, before string,
	asc bool,
) ([]rawResult, error) {
	order := "DESC"
	if asc {
		order = "ASC"
	}
	var out []rawResult

	// Сообщения окна (role user/assistant; tool-эхо вне выдачи, волна 86).
	// #nosec G202 -- order из двух литералов; значения параметризованы
	qm := `SELECT m.id, m.chat_id, m.role, m.content, m.ts
		FROM messages m
		WHERE m.role IN ('user','assistant')
			AND datetime(m.ts) >= datetime(?)
			AND datetime(m.ts) < datetime(?)
		ORDER BY m.ts ` + order + `, m.id LIMIT ?`
	rows, err := ms.bm.db.QueryContext(ctx, qm, after, before, temporalPool)
	if err != nil {
		return nil, fmt.Errorf("pika/memory_tools: temporal messages: %w", err)
	}
	for rows.Next() {
		var id int64
		var chatID, role, ts string
		var content sql.NullString
		if scanErr := rows.Scan(&id, &chatID, &role, &content, &ts); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("pika/memory_tools: temporal scan: %w", scanErr)
		}
		out = append(out, rawResult{
			Type:        "session",
			Summary:     fmt.Sprintf("[%s] %s", role, truncateStr(content.String, 200)),
			Source:      "temporal",
			CreatedAt:   parseSQLiteTime(ts),
			IsFTS:       true,
			DedupKey:    fmt.Sprintf("messages:%d", id), // ключ messages-ноги: корроборация (срез М2)
			Role:        role,
			LayerPrio:   prioTemporal,
			MsgID:       id,
			ChatID:      chatID,
			FullContent: fmt.Sprintf("[%s] %s", role, content.String),
		})
	}
	rows.Close()

	// События окна — действия с исходом (память дел).
	qe := `SELECT e.id, e.type, e.outcome, e.summary, e.ts
		FROM events e
		WHERE datetime(e.ts) >= datetime(?)
			AND datetime(e.ts) < datetime(?)
		ORDER BY e.ts ` + order + `, e.id LIMIT ?`
	rows2, err := ms.bm.db.QueryContext(ctx, qe, after, before, temporalPool)
	if err != nil {
		return nil, fmt.Errorf("pika/memory_tools: temporal events: %w", err)
	}
	for rows2.Next() {
		var id int64
		var typ, summary, ts string
		var outcome sql.NullString
		if scanErr := rows2.Scan(&id, &typ, &outcome, &summary, &ts); scanErr != nil {
			rows2.Close()
			return nil, fmt.Errorf("pika/memory_tools: temporal events scan: %w", scanErr)
		}
		label := "[" + typ
		if outcome.Valid && outcome.String != "" {
			label += ":" + outcome.String
		}
		label += "] " + summary
		out = append(out, rawResult{
			Type:        "event",
			Summary:     label,
			Source:      "temporal",
			CreatedAt:   parseSQLiteTime(ts),
			IsFTS:       true,
			DedupKey:    fmt.Sprintf("events:%d", id), // ключ горячей events-ноги
			LayerPrio:   prioTemporal,
			FullContent: label,
		})
	}
	rows2.Close()

	// Единый ts-порядок окна; позиция → синтетический bm25.
	sort.SliceStable(out, func(i, j int) bool {
		if asc {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > temporalPool {
		out = out[:temporalPool]
	}
	for i := range out {
		out[i].RawBM25 = float64(i)
	}
	return out, nil
}

// applyTemporalWindow — W2: явный маркер периода = жёсткий приоритет пула:
// записи окна вперёд (вызов ПОСЛЕ реранка — иначе реранкер топит тонкие эхо
// окна среди богатых атомов; кейс 10). Единая точка для Execute и Search.
func (ms *MemorySearch) applyTemporalWindow(query string, scored []SearchResult) []SearchResult {
	a, b, _, ok := ParseTemporalWindow(query, time.Now())
	if !ok {
		return scored
	}
	inWin := make([]SearchResult, 0, len(scored))
	outWin := make([]SearchResult, 0, len(scored))
	for _, r := range scored {
		if r.CreatedAt >= a && r.CreatedAt < b {
			inWin = append(inWin, r)
		} else {
			outWin = append(outWin, r)
		}
	}
	return append(inWin, outWin...)
}
