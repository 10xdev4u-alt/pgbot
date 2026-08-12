-- User indexes with scan counts + size + definition, plus the flags the
-- unused-index rule needs to avoid recommending an outage:
--   backs_constraint : an index enforcing a PK / UNIQUE / EXCLUSION / FK
--                      constraint (pg_constraint.conindid) — cannot be dropped.
--   is_exclusion     : exclusion-constraint index.
--   is_partial       : has a WHERE predicate (serves a narrow path).
--   is_expression    : indexes an expression, not bare columns.
SELECT s.schemaname            AS schema,
       s.relname               AS "table",
       s.indexrelname          AS "index",
       coalesce(s.idx_scan, 0) AS scans,
       pg_relation_size(s.indexrelid) AS bytes,
       coalesce(i.indexdef, '') AS definition,
       ix.indisprimary         AS is_primary,
       ix.indisunique          AS is_unique,
       ix.indisexclusion       AS is_exclusion,
       (ix.indpred IS NOT NULL) AS is_partial,
       (ix.indexprs IS NOT NULL) AS is_expression,
       EXISTS (SELECT 1 FROM pg_constraint co WHERE co.conindid = s.indexrelid) AS backs_constraint
FROM pg_stat_user_indexes s
JOIN pg_index ix ON ix.indexrelid = s.indexrelid
LEFT JOIN pg_indexes i
  ON i.schemaname = s.schemaname AND i.tablename = s.relname AND i.indexname = s.indexrelname
ORDER BY bytes DESC
LIMIT 200;
