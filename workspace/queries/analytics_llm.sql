-- Основные метрики LLM
SELECT
    COUNT(*)                                    AS total_requests,
    COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS total_tokens,
    COALESCE(SUM(cost_usd), 0.0)                AS total_cost_usd,
    COALESCE(AVG(response_ms), 0)               AS avg_response_ms,
    SUM(CASE WHEN error != '' THEN 1 ELSE 0 END) * 100.0
        / NULLIF(COUNT(*), 0)                   AS error_rate_pct,
    AVG(CAST(reasoning_tokens AS REAL)
        / NULLIF(completion_tokens, 0))          AS reasoning_ratio
FROM request_log
WHERE ts >= ? AND ts < ?;

-- Cost по компонентам (D-83)
SELECT component, COALESCE(SUM(cost_usd), 0.0) AS cost, COUNT(*) AS requests
FROM request_log
WHERE ts >= ? AND ts < ?
GROUP BY component;

-- P95 latency: Go получает отсортированный массив, вычисляет percentile
SELECT response_ms
FROM request_log
WHERE ts >= ? AND ts < ? AND response_ms IS NOT NULL
ORDER BY response_ms
