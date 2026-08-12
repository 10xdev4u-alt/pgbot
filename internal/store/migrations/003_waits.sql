-- Wait-event (ASH) rollups. We never store raw per-sample rows — only aggregate
-- counts per time bucket: minute granularity for the recent 7 days, folded to
-- hourly out to 90 days. wait_event is '' for the synthetic CPU bucket. See T8.
CREATE TABLE IF NOT EXISTS wait_rollups (
    target_id   TEXT    NOT NULL,
    bucket_ts   INTEGER NOT NULL,   -- unix seconds, truncated to the bucket start
    granularity TEXT    NOT NULL,   -- 'minute' | 'hour'
    wait_type   TEXT    NOT NULL,   -- Lock, LWLock, IO, Client, CPU, ...
    wait_event  TEXT    NOT NULL,   -- specific wait_event; '' for CPU
    samples     INTEGER NOT NULL,
    PRIMARY KEY (target_id, bucket_ts, granularity, wait_type, wait_event)
);
CREATE INDEX IF NOT EXISTS wait_rollups_target_time ON wait_rollups(target_id, bucket_ts);
