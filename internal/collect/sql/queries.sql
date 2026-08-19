-- Top of pg_stat_statements by total execution time. Cumulative since stats
-- reset — the temporal view comes from the baseline diff, not a short sample.
-- %[1]s is the version-appropriate total-time column (total_exec_time / total_time),
-- substituted from a fixed allowlist in Go (never user input). %[2]s is the
-- schema-qualified, quoted pg_stat_statements view (the extension's namespace,
-- discovered from pg_extension — Supabase installs it in "extensions", not
-- public, so an unqualified name fails for a read-only role). query text is
-- normalized ($1) for DML — but pgss stores UTILITY statements verbatim (with
-- literals), so the collector runs it through conn.ScrubQueryText, not verbatim.
SELECT queryid,
       left(query, 4000)   AS query,
       calls,
       %[1]s               AS total_ms,
       %[1]s / nullif(calls, 0) AS mean_ms,
       max_exec_time       AS max_ms,
       rows,
       shared_blks_hit,
       shared_blks_read,
       wal_bytes,
       -- window sum runs before LIMIT, so this is total exec time across ALL
       -- statements (not just the top 20) — the denominator for prop_exec_time.
       sum(%[1]s) OVER ()  AS total_exec_all
FROM %[2]s
WHERE queryid IS NOT NULL AND calls > 0
  AND query NOT ILIKE '%%pg_stat_statements%%'
  -- Drop transaction-control and session GUC statements. They dominate a quiet
  -- database's counters (SET/BEGIN/COMMIT run on every connection) but are never
  -- "the query eating your database" — the question this view answers. \M is an
  -- end-of-word boundary so SET matches but SELECT/SETseq-named tables do not.
  AND query !~* '^\s*(set|show|reset|begin|commit|rollback|discard|deallocate|start)\M'
ORDER BY %[1]s DESC
LIMIT 20;
