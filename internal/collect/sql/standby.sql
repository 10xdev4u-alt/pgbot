-- Standby-side recovery-conflict counts for the current database. On a hot
-- standby, a query can be cancelled when recovery needs to apply a change it
-- conflicts with (a dropped tablespace, a snapshot too old, a lock, a pinned
-- buffer, a deadlock with recovery). Cumulative since stats_reset.
SELECT coalesce(confl_tablespace, 0) AS confl_tablespace,
       coalesce(confl_lock, 0)       AS confl_lock,
       coalesce(confl_snapshot, 0)   AS confl_snapshot,
       coalesce(confl_bufferpin, 0)  AS confl_bufferpin,
       coalesce(confl_deadlock, 0)   AS confl_deadlock
FROM pg_stat_database_conflicts
WHERE datname = current_database();
