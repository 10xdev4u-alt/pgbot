-- Primary-side view of connected standbys with byte lag at each stage.
SELECT coalesce(client_addr::text, 'local')                     AS client_addr,
       coalesce(state, '')                                       AS state,
       coalesce(sync_state, '')                                  AS sync_state,
       coalesce(pg_wal_lsn_diff(sent_lsn, write_lsn), 0)::bigint  AS write_lag_bytes,
       coalesce(pg_wal_lsn_diff(sent_lsn, flush_lsn), 0)::bigint  AS flush_lag_bytes,
       coalesce(pg_wal_lsn_diff(sent_lsn, replay_lsn), 0)::bigint AS replay_lag_bytes
FROM pg_stat_replication;
