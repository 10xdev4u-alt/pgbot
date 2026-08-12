-- Fallback for PG < 16: pg_stat_bgwriter carries checkpoint + buffer counters.
SELECT checkpoints_timed,
       checkpoints_req,
       buffers_checkpoint + buffers_clean + buffers_backend AS buffers_written,
       buffers_backend_fsync AS backend_fsyncs
FROM pg_stat_bgwriter;
