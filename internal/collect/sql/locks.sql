-- Blocking chains via pg_blocking_pids (PG9.6+). blocked_query is RAW SQL and
-- MUST be scrubbed in Go before entering the Context.
SELECT a.pid                                            AS blocked_pid,
       pg_blocking_pids(a.pid)                          AS blocking_pids,
       coalesce(a.wait_event_type, '')                  AS wait_event_type,
       coalesce(extract(epoch FROM now() - a.query_start), 0) AS blocked_wait_s,
       left(coalesce(a.query, ''), 300)                 AS blocked_query
FROM pg_stat_activity a
WHERE cardinality(pg_blocking_pids(a.pid)) > 0;
