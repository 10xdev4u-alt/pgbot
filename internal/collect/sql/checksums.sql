-- Data-checksum failures across the WHOLE cluster (PG12+). A nonzero count is
-- Postgres reporting a page whose checksum didn't match — storage corruption, bad
-- RAM, or a filesystem lying about writes. Corruption in another database in the
-- cluster is equally the operator's problem, so this is cluster-wide with a
-- per-database breakdown, not scoped to current_database(). Shared catalogs
-- (datname NULL) are included as '(shared objects)'.
SELECT coalesce(datname, '(shared objects)') AS database,
       checksum_failures,
       checksum_last_failure
FROM pg_stat_database
WHERE checksum_failures > 0
ORDER BY checksum_failures DESC;
