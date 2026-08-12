-- pg_stat_database for the current database. Counter fields (xact_*, blks_*,
-- tup_*, deadlocks, temp_*) are double-sampled for rates; numbackends and
-- stats_reset are read from the second sample as the current gauge/timestamp.
SELECT numbackends,
       xact_commit, xact_rollback,
       blks_read, blks_hit,
       tup_returned, tup_fetched, tup_inserted, tup_updated, tup_deleted,
       deadlocks, temp_bytes,
       stats_reset
FROM pg_stat_database
WHERE datname = current_database();
