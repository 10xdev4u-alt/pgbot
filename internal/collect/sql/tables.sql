-- Largest user tables with vacuum/scan/bloat signals. Sizes and dead-tuple
-- counts are gauges; seq/idx scan counts are cumulative (trended via baseline).
SELECT s.schemaname                       AS schema,
       s.relname                          AS "table",
       pg_total_relation_size(s.relid)    AS total_bytes,
       s.n_live_tup                       AS live_tuples,
       s.n_dead_tup                       AS dead_tuples,
       s.seq_scan                         AS seq_scans,
       coalesce(s.idx_scan, 0)            AS index_scans,
       s.n_mod_since_analyze              AS mods_since_analyze,
       s.last_vacuum,
       s.last_autovacuum
FROM pg_stat_user_tables s
ORDER BY total_bytes DESC
LIMIT 30;
