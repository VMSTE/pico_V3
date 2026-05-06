-- Health субагентов по trace_spans
SELECT
    component,
    COUNT(*)                                         AS total_spans,
    SUM(CASE WHEN status = 'error'   THEN 1 ELSE 0 END) AS error_count,
    SUM(CASE WHEN status = 'timeout' THEN 1 ELSE 0 END) AS timeout_count,
    COALESCE(AVG(duration_ms), 0)                    AS avg_duration_ms
FROM trace_spans
WHERE started_at >= ? AND started_at < ?
  AND component IN ('archivarius','atomizer','reflexor','mcp_guard')
GROUP BY component;

-- P95 duration per component
SELECT component, duration_ms
FROM trace_spans
WHERE started_at >= ? AND started_at < ?
  AND component IN ('archivarius','atomizer','reflexor','mcp_guard')
  AND duration_ms IS NOT NULL
ORDER BY component, duration_ms
