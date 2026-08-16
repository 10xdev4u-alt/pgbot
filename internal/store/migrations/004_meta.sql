-- Key/value metadata for the store itself: scheme/version markers and one-time
-- upgrade notices. Kept separate from snapshots so it survives retention pruning.
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
