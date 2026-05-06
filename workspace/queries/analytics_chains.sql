-- Chain analysis (D-51: chain_id + chain_position)
SELECT
    COUNT(DISTINCT chain_id) AS total_chains,
    AVG(chain_len)           AS avg_chain_length,
    AVG(chain_cost)          AS avg_chain_cost
FROM (
    SELECT chain_id,
        MAX(chain_position)  AS chain_len,
        SUM(cost_usd)        AS chain_cost
    FROM request_log
    WHERE chain_id IS NOT NULL AND ts >= ? AND ts < ?
    GROUP BY chain_id
)
