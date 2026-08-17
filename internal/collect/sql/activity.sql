-- Point-in-time connection breakdown. Grouped maxes let us derive the global
-- longest transaction / active query by taking max across rows in Go. Client
-- backends only.
--
-- Excludes ALL of pgbot's own connections, not just the one running this query.
-- pgbot samples through a small pool, each connection in a short READ ONLY
-- transaction; pg_backend_pid() only drops the querying backend, so sibling pool
-- connections caught mid-sample would otherwise be counted as "idle in
-- transaction" (and pad the connection/xact-age stats) — a flaky false positive
-- on an otherwise-quiet database. Every pgbot connection sets
-- application_name = 'pgbot', so filter on that.
SELECT coalesce(state, 'unknown')          AS state,
       coalesce(wait_event_type, '')       AS wait_event_type,
       count(*)                            AS n,
       coalesce(max(extract(epoch FROM now() - xact_start)), 0)                          AS max_xact_age_s,
       coalesce(max(extract(epoch FROM now() - query_start)) FILTER (WHERE state = 'active'), 0) AS max_active_age_s
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
  AND backend_type = 'client backend'
  AND application_name IS DISTINCT FROM 'pgbot'
GROUP BY 1, 2;
