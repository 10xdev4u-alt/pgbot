package store

// Schema is small and forward-only; each migration is idempotent. The full
// Context lives as JSON; the scalar columns are extracted copies for fast
// trend queries and retention decisions.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS snapshots (
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
	)`,
	`CREATE INDEX IF NOT EXISTS idx_snap_fp_time ON snapshots(fingerprint, collected_at)`,
	`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)`,
}

func (s *Store) migrate() error {
	for _, stmt := range migrations {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// allowedScalar guards the one place a column name is interpolated into SQL
// (Trend): only these names are ever accepted.
var allowedScalar = map[string]bool{
	"tps": true, "cache_hit": true, "connections": true,
	"db_size_bytes": true, "dead_ratio_max": true, "longest_xact_s": true,
}
