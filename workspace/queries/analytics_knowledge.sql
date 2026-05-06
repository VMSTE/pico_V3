-- Knowledge quality snapshot
SELECT
    COUNT(*)                                              AS total_atoms,
    SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END)     AS new_in_period,
    SUM(CASE WHEN category = 'pattern'       THEN 1 ELSE 0 END) AS cat_pattern,
    SUM(CASE WHEN category = 'constraint'    THEN 1 ELSE 0 END) AS cat_constraint,
    SUM(CASE WHEN category = 'decision'      THEN 1 ELSE 0 END) AS cat_decision,
    SUM(CASE WHEN category = 'tool_pref'     THEN 1 ELSE 0 END) AS cat_tool_pref,
    SUM(CASE WHEN category = 'summary'       THEN 1 ELSE 0 END) AS cat_summary,
    SUM(CASE WHEN category = 'runbook_draft' THEN 1 ELSE 0 END) AS cat_runbook,
    SUM(CASE WHEN polarity = 'positive' THEN 1 ELSE 0 END) AS pol_positive,
    SUM(CASE WHEN polarity = 'negative' THEN 1 ELSE 0 END) AS pol_negative,
    SUM(CASE WHEN polarity = 'neutral'  THEN 1 ELSE 0 END) AS pol_neutral,
    SUM(CASE WHEN confidence >= 0.8                      THEN 1 ELSE 0 END) AS conf_high,
    SUM(CASE WHEN confidence >= 0.4 AND confidence < 0.8 THEN 1 ELSE 0 END) AS conf_medium,
    SUM(CASE WHEN confidence >= 0.2 AND confidence < 0.4 THEN 1 ELSE 0 END) AS conf_low,
    SUM(CASE WHEN confidence < 0.2                       THEN 1 ELSE 0 END) AS conf_stale
FROM knowledge_atoms
