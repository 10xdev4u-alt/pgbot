-- The full Context lives as JSON; the scalar columns are extracted copies for
-- fast trend queries and retention decisions.
CREATE TABLE IF NOT EXISTS snapshots (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    fingerprint    TEXT    NOT NULL,
    collected_at   INTEGER NOT NULL,      -- unix seconds, UTC
    schema_version TEXT    NOT NULL,
    context_json   TEXT    NOT NULL,
    tps            REAL,
    cache_hit      REAL,
    connections    INTEGER,
    db_size_bytes  INTEGER,
    dead_ratio_max REAL,
    longest_xact_s REAL
);
CREATE INDEX IF NOT EXISTS idx_snap_fp_time ON snapshots(fingerprint, collected_at);
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
