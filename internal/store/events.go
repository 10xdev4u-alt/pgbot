package store

import (
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

// SaveSchema records the current schema fingerprint under a snapshot id, then
// prunes to the two most recent fingerprints per target (fingerprints are large;
// events are small and kept).
func (s *Store) SaveSchema(fingerprint string, snapshotID int64, fp *model.SchemaFingerprint) error {
	if fp == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO schema_objects
		(target_id, snapshot_id, kind, identity, definition_hash, definition, invalid)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, o := range fp.Objects {
		if _, err := stmt.Exec(fingerprint, snapshotID, o.Kind, o.Identity, o.DefinitionHash, o.Definition, boolToInt(o.Invalid)); err != nil {
			return err
		}
	}
	// Keep only the two most recent fingerprint snapshots for this target.
	if _, err := tx.Exec(`
		DELETE FROM schema_objects
		WHERE target_id = ? AND snapshot_id NOT IN (
			SELECT DISTINCT snapshot_id FROM schema_objects WHERE target_id = ?
			ORDER BY snapshot_id DESC LIMIT 2
		)`, fingerprint, fingerprint); err != nil {
		return err
	}
	return tx.Commit()
}

// LoadLatestSchema returns the most recently stored fingerprint for a target
// (its objects), or nil when none exists yet.
func (s *Store) LoadLatestSchema(fingerprint string) ([]model.SchemaObject, error) {
	rows, err := s.db.Query(`
		SELECT kind, identity, definition_hash, definition, invalid
		FROM schema_objects
		WHERE target_id = ? AND snapshot_id = (
			SELECT max(snapshot_id) FROM schema_objects WHERE target_id = ?
		)`, fingerprint, fingerprint)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SchemaObject
	for rows.Next() {
		var o model.SchemaObject
		var inv int
		if err := rows.Scan(&o.Kind, &o.Identity, &o.DefinitionHash, &o.Definition, &inv); err != nil {
			return nil, err
		}
		o.Invalid = inv != 0
		out = append(out, o)
	}
	return out, rows.Err()
}

// AppendEvents writes the derived events for this run.
func (s *Store) AppendEvents(fingerprint string, observedAt time.Time, evs []model.Event) error {
	if len(evs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	stmt, err := tx.Prepare(`INSERT INTO events
		(target_id, observed_at, occurred_after, occurred_before, kind, object, before, after, confidence)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range evs {
		if _, err := stmt.Exec(fingerprint, observedAt.UTC().Unix(),
			unixPtr(e.OccurredAfter), unixPtr(e.OccurredBefore), e.Kind, e.Object, e.Before, e.After, e.Confidence); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecentEvents returns the last n events for a target, newest first.
func (s *Store) RecentEvents(fingerprint string, n int) ([]model.Event, error) {
	rows, err := s.db.Query(`
		SELECT observed_at, occurred_after, occurred_before, kind, object, before, after, confidence
		FROM events WHERE target_id = ? ORDER BY observed_at DESC, id DESC LIMIT ?`, fingerprint, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		var oaft, obef *int64
		if err := rows.Scan(new(int64), &oaft, &obef, &e.Kind, &e.Object, &e.Before, &e.After, &e.Confidence); err != nil {
			return nil, err
		}
		e.OccurredAfter = fromUnix(oaft)
		e.OccurredBefore = fromUnix(obef)
		out = append(out, e)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func unixPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	u := t.UTC().Unix()
	return &u
}

func fromUnix(u *int64) *time.Time {
	if u == nil {
		return nil
	}
	t := time.Unix(*u, 0).UTC()
	return &t
}
