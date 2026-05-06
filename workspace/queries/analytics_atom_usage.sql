-- Atom usage агрегаты
SELECT
    COUNT(*)                          AS total_usages,
    COUNT(DISTINCT atom_id)           AS unique_atoms_used,
    SUM(CASE WHEN invoked_tool_result = 'success' THEN 1 ELSE 0 END) * 100.0
        / NULLIF(COUNT(*), 0)         AS effectiveness_pct
FROM atom_usage
WHERE created_at >= ? AND created_at < ?;

-- Top atoms by usage count
SELECT atom_id, COUNT(*) AS uses
FROM atom_usage
WHERE created_at >= ? AND created_at < ?
GROUP BY atom_id
ORDER BY uses DESC
LIMIT 10;

-- Unused atoms
SELECT COUNT(*) AS unused_count
FROM knowledge_atoms ka
WHERE NOT EXISTS (
    SELECT 1 FROM atom_usage au
    WHERE au.atom_id = ka.atom_id AND au.created_at >= ?
)
