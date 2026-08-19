package store

import (
	"database/sql"
	"time"
)

// IndexVerdict is one recorded code-search result for an index (index/code
// correlation). It is the compounding half of the feature: a one-off grep is a
// one-off, but a verdict stored against a growing stats window gets stronger.
type IndexVerdict struct {
	IndexID         string    // schema.index
	Verdict         string    // not_found_in_code | found_in_code | inconclusive
	Source          string    // e.g. agent_repo_search
	RepoRef         string    // optional commit sha
	CheckedAt       time.Time // when the search was run
	StatsWindowDays *float64  // window length at the time, for "evidence strengthened"
}

// SaveIndexVerdict upserts one verdict for (fingerprint, index). Re-recording the
// same index replaces the prior verdict — the latest code search wins.
func (s *Store) SaveIndexVerdict(fingerprint string, v IndexVerdict) error {
	var win sql.NullFloat64
	if v.StatsWindowDays != nil {
		win = sql.NullFloat64{Float64: *v.StatsWindowDays, Valid: true}
	}
	_, err := s.db.Exec(`
		INSERT INTO index_verdicts (fingerprint, index_id, verdict, source, repo_ref, checked_at, stats_window_days)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(fingerprint, index_id) DO UPDATE SET
		  verdict = excluded.verdict, source = excluded.source, repo_ref = excluded.repo_ref,
		  checked_at = excluded.checked_at, stats_window_days = excluded.stats_window_days`,
		fingerprint, v.IndexID, v.Verdict, v.Source, v.RepoRef, v.CheckedAt.UTC().Unix(), win)
	return err
}

// LoadIndexVerdicts returns every stored verdict for a database, keyed by
// index_id (schema.index).
func (s *Store) LoadIndexVerdicts(fingerprint string) (map[string]IndexVerdict, error) {
	rows, err := s.db.Query(`
		SELECT index_id, verdict, source, repo_ref, checked_at, stats_window_days
		FROM index_verdicts WHERE fingerprint = ?`, fingerprint)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]IndexVerdict{}
	for rows.Next() {
		var (
			v      IndexVerdict
			source sql.NullString
			ref    sql.NullString
			ts     int64
			win    sql.NullFloat64
		)
		if err := rows.Scan(&v.IndexID, &v.Verdict, &source, &ref, &ts, &win); err != nil {
			return nil, err
		}
		v.Source, v.RepoRef = source.String, ref.String
		v.CheckedAt = time.Unix(ts, 0).UTC()
		if win.Valid {
			w := win.Float64
			v.StatsWindowDays = &w
		}
		out[v.IndexID] = v
	}
	return out, rows.Err()
}
