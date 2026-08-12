-- PG17+: checkpoint counters moved to pg_stat_checkpointer; buffers_written
-- comes from pg_stat_io (context='normal'). num_timed/num_requested are the
-- renamed checkpoint counters.
SELECT c.num_timed        AS checkpoints_timed,
       c.num_requested    AS checkpoints_req,
       coalesce((SELECT sum(writes) FROM pg_stat_io WHERE writes IS NOT NULL), 0) AS buffers_written,
       0::bigint          AS backend_fsyncs
FROM pg_stat_checkpointer c;
