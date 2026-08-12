-- Schema fingerprints (two most recent retained per target) + the full event
-- log (events are small and kept). See T7.
CREATE TABLE IF NOT EXISTS schema_objects (
    target_id       TEXT    NOT NULL,
    snapshot_id     INTEGER NOT NULL,
    kind            TEXT    NOT NULL,
    identity        TEXT    NOT NULL,
    definition_hash TEXT    NOT NULL,
    definition      TEXT,
    invalid         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (target_id, snapshot_id, kind, identity)
);

CREATE TABLE IF NOT EXISTS events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    target_id       TEXT    NOT NULL,
    observed_at     INTEGER NOT NULL,   -- unix seconds, UTC
    occurred_after  INTEGER,
    occurred_before INTEGER,
    kind            TEXT    NOT NULL,
    object          TEXT,
    before          TEXT,
    after           TEXT,
    confidence      REAL    NOT NULL
);
CREATE INDEX IF NOT EXISTS events_target_time ON events(target_id, observed_at);
