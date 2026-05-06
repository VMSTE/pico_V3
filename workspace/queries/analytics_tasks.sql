-- Task efficiency: top-N по расходу
SELECT
    task_tag,
    COUNT(*)                     AS request_count,
    AVG(prompt_tokens + completion_tokens) AS avg_tokens,
    AVG(tool_calls_requested)    AS avg_tools,
    COALESCE(SUM(cost_usd), 0.0) AS total_cost,
    COALESCE(AVG(cost_usd), 0.0) AS avg_cost
FROM request_log
WHERE ts >= ? AND ts < ? AND task_tag IS NOT NULL AND task_tag != ''
GROUP BY task_tag
ORDER BY total_cost DESC
LIMIT 5
