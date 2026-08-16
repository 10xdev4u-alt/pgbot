-- Primary-side view of connected standbys: byte lag at each stage, plus the
-- replay_lag INTERVAL (the failover-relevant "seconds of writes lost if I promote
-- now") and sync-rep fields. replay_lag is only updated while WAL is being
-- generated, so it can be null/stale on an idle primary — the finding gates on
-- observed WAL generation rather than reporting a reassuring zero.
SELECT coalesce(client_addr::text, 'local')                      AS client_addr,
       coalesce(application_name, '')                            AS application_name,
       coalesce(state, '')                                       AS state,
       coalesce(sync_state, '')                                  AS sync_state,
       coalesce(sync_priority, 0)                                AS sync_priority,
       extract(epoch FROM replay_lag)::float8                    AS replay_lag_sec,
       coalesce(pg_wal_lsn_diff(sent_lsn, write_lsn), 0)::bigint  AS write_lag_bytes,
       coalesce(pg_wal_lsn_diff(sent_lsn, flush_lsn), 0)::bigint  AS flush_lag_bytes,
       coalesce(pg_wal_lsn_diff(sent_lsn, replay_lsn), 0)::bigint AS replay_lag_bytes
FROM pg_stat_replication;
