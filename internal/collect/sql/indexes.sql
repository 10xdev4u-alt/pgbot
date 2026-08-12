-- User indexes with scan counts + size + definition, and whether the index
-- backs a primary key or unique constraint (those can't be dropped, so they're
-- excluded from the "unused" finding even at zero scans).
SELECT s.schemaname            AS schema,
       s.relname               AS "table",
       s.indexrelname          AS "index",
       coalesce(s.idx_scan, 0) AS scans,
       pg_relation_size(s.indexrelid) AS bytes,
       coalesce(i.indexdef, '') AS definition,
       ix.indisprimary         AS is_primary,
       ix.indisunique          AS is_unique
FROM pg_stat_user_indexes s
JOIN pg_index ix ON ix.indexrelid = s.indexrelid
LEFT JOIN pg_indexes i
  ON i.schemaname = s.schemaname AND i.tablename = s.relname AND i.indexname = s.indexrelname
ORDER BY bytes DESC
LIMIT 200;
