-- Point-in-time connection breakdown. Grouped maxes let us derive the global
-- longest transaction / active query by taking max across rows in Go. Client
-- backends only.
--
-- The `pid <> pg_backend_pid()` filter is rewritten at runtime by Target.
-- ExcludeSelf to drop ALL of pgbot's own pool connections, not just the one
-- running this query — otherwise sibling pool connections caught mid-sample
-- (briefly idle-in-transaction between short READ ONLY samples) would be counted
-- as "idle in transaction" and pad the xact-age stats, a flaky false positive on
-- a quiet database.
SELECT coalesce(state, 'unknown')          AS state,
       coalesce(wait_event_type, '')       AS wait_event_type,
       count(*)                            AS n,
       coalesce(max(extract(epoch FROM now() - xact_start)), 0)                          AS max_xact_age_s,
       coalesce(max(extract(epoch FROM now() - query_start)) FILTER (WHERE state = 'active'), 0) AS max_active_age_s
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
  AND backend_type = 'client backend'
GROUP BY 1, 2;
