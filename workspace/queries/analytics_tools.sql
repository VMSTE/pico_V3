-- Агрегаты tool calling
SELECT
    COALESCE(SUM(tool_calls_requested), 0) AS total_requested,
    COALESCE(SUM(tool_calls_success), 0)   AS total_success,
    COALESCE(SUM(tool_calls_failed), 0)    AS total_failed,
    SUM(tool_calls_success) * 100.0
        / NULLIF(SUM(tool_calls_requested), 0) AS success_rate_pct
FROM request_log
WHERE ts >= ? AND ts < ? AND tool_calls_requested > 0;

-- Top tools (json_each по tool_names JSON array)
SELECT value AS tool_name, COUNT(*) AS cnt
FROM request_log, json_each(tool_names)
WHERE ts >= ? AND ts < ? AND tool_names IS NOT NULL
GROUP BY value
ORDER BY cnt DESC
LIMIT 10
