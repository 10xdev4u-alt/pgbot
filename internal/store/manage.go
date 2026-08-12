package store

import (
	"encoding/json"
	"io"
	"time"
)

// ListItem is a row for `pgbot baselines list`.
type ListItem struct {
	Fingerprint string
	Database    string
	Count       int
	Oldest      time.Time
	Newest      time.Time
}

// List summarises what's stored, grouped by target database. The rows are read
// to completion FIRST, then per-fingerprint names are looked up — issuing a
// nested query while the outer rows are still open would deadlock against the
// single-connection pool.
func (s *Store) List() ([]ListItem, error) {
	rows, err := s.db.Query(`
		SELECT fingerprint, count(*), min(collected_at), max(collected_at)
		FROM snapshots GROUP BY fingerprint ORDER BY max(collected_at) DESC`)
	if err != nil {
		return nil, err
	}
	var out []ListItem
	for rows.Next() {
		var it ListItem
		var oldest, newest int64
		if err := rows.Scan(&it.Fingerprint, &it.Count, &oldest, &newest); err != nil {
			rows.Close()
			return nil, err
		}
		it.Oldest = time.Unix(oldest, 0).UTC()
		it.Newest = time.Unix(newest, 0).UTC()
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close() // release the connection before the per-fingerprint lookups

	for i := range out {
		out[i].Database = s.latestDatabaseName(out[i].Fingerprint)
	}
	return out, nil
}

func (s *Store) latestDatabaseName(fp string) string {
	var raw string
	if err := s.db.QueryRow(
		`SELECT context_json FROM snapshots WHERE fingerprint = ? ORDER BY collected_at DESC LIMIT 1`, fp,
	).Scan(&raw); err != nil {
		return ""
	}
	var probe struct {
		Server struct {
			Database string `json:"database"`
		} `json:"server"`
	}
	_ = json.Unmarshal([]byte(raw), &probe)
	return probe.Server.Database
}

// Prune deletes all snapshots for a fingerprint (user-initiated). Returns rows
// removed.
func (s *Store) Prune(fingerprint string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM snapshots WHERE fingerprint = ?`, fingerprint)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	_, _ = s.db.Exec(`VACUUM`)
	return n, nil
}

// Export writes every stored Context for a fingerprint as a JSON array to w.
func (s *Store) Export(fingerprint string, w io.Writer) error {
	rows, err := s.db.Query(
		`SELECT context_json FROM snapshots WHERE fingerprint = ? ORDER BY collected_at ASC`, fingerprint)
	if err != nil {
		return err
	}
	defer rows.Close()
	if _, err := io.WriteString(w, "[\n"); err != nil {
		return err
	}
	first := true
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if !first {
			if _, err := io.WriteString(w, ",\n"); err != nil {
				return err
			}
		}
		first = false
		if _, err := io.WriteString(w, raw); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n]\n")
	return err
}
