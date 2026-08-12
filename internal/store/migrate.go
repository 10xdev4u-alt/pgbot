package store

import (
	"embed"
	"sort"
)

// Schema is small and forward-only; each migration is idempotent (IF NOT
// EXISTS). SQL lives in migrations/*.sql, applied in filename order.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

func (s *Store) migrate() error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		stmt, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(string(stmt)); err != nil {
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
