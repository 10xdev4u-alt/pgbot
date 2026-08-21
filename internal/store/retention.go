package store

import (
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

// Retention policy: keep every snapshot for 7 days, then thin to one per hour
// out to 90 days, then drop. A hard 100 MB cap evicts oldest-first as a
// backstop. Users can see and delete what's stored with `pgbot baselines`.
const (
	keepAllFor    = 7 * 24 * time.Hour
	keepRollupFor = 90 * 24 * time.Hour
)

// maxBytes is a var (not const) only so the eviction test can lower the cap and
// drive enforceSizeCap without writing 100 MB of snapshots.
var maxBytes int64 = 100 << 20

// prune enforces the policy for one fingerprint (called after each Save).
func (s *Store) prune(fingerprint string) error {
	now := time.Now().UTC()

	// 1. Drop everything older than the rollup horizon.
	if _, err := s.db.Exec(
		`DELETE FROM snapshots WHERE fingerprint = ? AND collected_at < ?`,
		fingerprint, now.Add(-keepRollupFor).Unix()); err != nil {
		return err
	}

	// 2. Thin 7d..90d to one row per hour bucket (keep the newest in each).
	rollupCutoff := now.Add(-keepAllFor).Unix()
	if _, err := s.db.Exec(`
		DELETE FROM snapshots
		WHERE fingerprint = ? AND collected_at < ?
		  AND id NOT IN (
			SELECT max(id) FROM snapshots
			WHERE fingerprint = ? AND collected_at < ?
			GROUP BY collected_at / 3600
		  )`, fingerprint, rollupCutoff, fingerprint, rollupCutoff); err != nil {
		return err
	}

	// 3. Size backstop: if the DB file exceeds the cap, evict oldest globally.
	return s.enforceSizeCap()
}

func (s *Store) enforceSizeCap() error {
	// DELETE alone never shrinks a WAL-mode database file — freed pages stay
	// in the file until a VACUUM rewrites it. Without the VACUUM below, the
	// page count stays above the cap forever and every later run evicts
	// another 10%, eating the whole history. So each eviction round ends
	// with a VACUUM that actually reclaims the space.
	for i := 0; i < 20; i++ {
		var pageCount, pageSize int64
		if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
			return err
		}
		if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
			return err
		}
		if pageCount*pageSize <= maxBytes {
			return nil
		}
		res, err := s.db.Exec(`
			DELETE FROM snapshots WHERE id IN (
				SELECT id FROM snapshots ORDER BY collected_at ASC
				LIMIT (SELECT max(1, count(*)/10) FROM snapshots)
			)`)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // nothing left to evict; file is irreducible
		}
		if _, err := s.db.Exec(`VACUUM`); err != nil {
			return err
		}
	}
	return nil
}

// scalars are the extracted trend columns.
type scalars struct {
	tps, cacheHit, deadRatioMax, longestXact *float64
	connections, dbSize                      *int64
}

func extractScalars(c *model.Context) scalars {
	var sc scalars
	if c.Health != nil {
		sc.tps = c.Health.TPS
		sc.cacheHit = c.Health.CacheHitRatio
		if c.Health.Connections > 0 {
			n := int64(c.Health.Connections)
			sc.connections = &n
		}
	}
	if c.Tables != nil {
		sc.dbSize = &c.Tables.DBSizeBytes
		var maxDead float64
		for _, t := range c.Tables.Top {
			if t.DeadRatio > maxDead {
				maxDead = t.DeadRatio
			}
		}
		sc.deadRatioMax = &maxDead
	}
	if c.Activity != nil {
		v := c.Activity.LongestXactSec
		sc.longestXact = &v
	}
	return sc
}
