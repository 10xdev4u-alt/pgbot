package store

import "time"

// SuppressionUnusedAfter is the number of consecutive runs a rule can match
// nothing before pgbot flags it as probably dead (B2-3).
const SuppressionUnusedAfter = 5

// RecordSuppressionUsage updates per-rule match bookkeeping for one run and
// returns the rules that have now matched nothing for SuppressionUnusedAfter
// consecutive runs. active is every ignore rule currently in the config (by
// IgnoreRule.String()); matched is the subset that matched a finding this run.
//
// Rules no longer in the config are pruned, so editing the ignore list clears
// their history rather than leaving orphan rows that could re-fire later.
func (s *Store) RecordSuppressionUsage(fingerprint string, active []string, matched map[string]bool, now time.Time) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	// Prune rows for rules that are no longer configured.
	keep := make(map[string]bool, len(active))
	for _, r := range active {
		keep[r] = true
	}
	rows, err := tx.Query(`SELECT rule FROM suppression_rules WHERE fingerprint = ?`, fingerprint)
	if err != nil {
		return nil, err
	}
	var stale []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return nil, err
		}
		if !keep[r] {
			stale = append(stale, r)
		}
	}
	rows.Close()
	for _, r := range stale {
		if _, err := tx.Exec(`DELETE FROM suppression_rules WHERE fingerprint = ? AND rule = ?`, fingerprint, r); err != nil {
			return nil, err
		}
	}

	var unused []string
	ts := now.UTC().Unix()
	for _, r := range active {
		if matched[r] {
			if _, err := tx.Exec(`
				INSERT INTO suppression_rules (fingerprint, rule, last_matched_at, misses)
				VALUES (?, ?, ?, 0)
				ON CONFLICT(fingerprint, rule) DO UPDATE SET last_matched_at = excluded.last_matched_at, misses = 0`,
				fingerprint, r, ts); err != nil {
				return nil, err
			}
			continue
		}
		// Missed this run: bump the counter (creating the row at misses=1 if new).
		if _, err := tx.Exec(`
			INSERT INTO suppression_rules (fingerprint, rule, last_matched_at, misses)
			VALUES (?, ?, NULL, 1)
			ON CONFLICT(fingerprint, rule) DO UPDATE SET misses = misses + 1`,
			fingerprint, r); err != nil {
			return nil, err
		}
		var misses int
		if err := tx.QueryRow(`SELECT misses FROM suppression_rules WHERE fingerprint = ? AND rule = ?`, fingerprint, r).Scan(&misses); err != nil {
			return nil, err
		}
		if misses >= SuppressionUnusedAfter {
			unused = append(unused, r)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return unused, nil
}
