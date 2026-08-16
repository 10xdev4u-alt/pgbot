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
       s.n_tup_upd                        AS updates,
       s.n_tup_hot_upd                    AS hot_updates,
       s.last_analyze,
       s.last_autoanalyze,
       s.last_vacuum,
       s.last_autovacuum,
       -- Per-table reloptions override the global autovacuum knobs and are a
       -- frequent source of surprise. n_live_tup (NOT reltuples, which is -1 for
       -- never-analyzed tables in PG14+) drives the analyze-threshold formula.
       (SELECT option_value FROM pg_options_to_table(c.reloptions)
          WHERE option_name = 'autovacuum_analyze_scale_factor')::float8 AS rel_analyze_scale,
       (SELECT option_value FROM pg_options_to_table(c.reloptions)
          WHERE option_name = 'autovacuum_analyze_threshold')::float8    AS rel_analyze_threshold
FROM pg_stat_user_tables s
JOIN pg_class c ON c.oid = s.relid
ORDER BY total_bytes DESC
LIMIT 30;
