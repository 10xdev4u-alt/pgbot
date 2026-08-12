-- Top of pg_stat_statements by total execution time. Cumulative since stats
-- reset — the temporal view comes from the baseline diff, not a short sample.
-- %s is the version-appropriate total-time column (total_exec_time / total_time),
-- substituted from a fixed allowlist in Go (never user input). query text is
-- normalized ($1) by pg_stat_statements and is safe to keep verbatim.
SELECT queryid,
       left(query, 4000)   AS query,
       calls,
       %[1]s               AS total_ms,
       %[1]s / nullif(calls, 0) AS mean_ms,
       max_exec_time       AS max_ms,
       rows,
       shared_blks_hit,
       shared_blks_read,
       wal_bytes
FROM pg_stat_statements
WHERE queryid IS NOT NULL AND calls > 0
  AND query NOT ILIKE '%%pg_stat_statements%%'
ORDER BY %[1]s DESC
LIMIT 20;
