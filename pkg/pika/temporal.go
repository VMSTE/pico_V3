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
var prioTemporal = prioMessages * 0.5

// temporalPool — константный пул ноги (как messages=100, knowledge=40).
const temporalPool = 30

// Русские месяцы во всех падежах (стемы, word boundary).
var monthPats = []struct {
	re *regexp.Regexp
	m  int
}{
	{regexp.MustCompile(`\bянвар[ьяею]\b`), 1},
	{regexp.MustCompile(`\bфеврал[ьяею]\b`), 2},
	{regexp.MustCompile(`\bмарт[аеу]?\b`), 3},
	{regexp.MustCompile(`\bапрел[ьяею]\b`), 4},
	{regexp.MustCompile(`\bма[йяею]\b`), 5},
	{regexp.MustCompile(`\bиюн[ьяею]\b`), 6},
	{regexp.MustCompile(`\bиюл[ьяею]\b`), 7},
	{regexp.MustCompile(`\bавгуст[аеу]?\b`), 8},
	{regexp.MustCompile(`\bсентябр[ьяею]\b`), 9},
	{regexp.MustCompile(`\bоктябр[ьяею]\b`), 10},
	{regexp.MustCompile(`\bноябр[ьяею]\b`), 11},
	{regexp.MustCompile(`\bдекабр[ьяею]\b`), 12},
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
