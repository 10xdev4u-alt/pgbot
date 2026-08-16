package findings

import (
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestWalArchiving(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	recent := time.Now().Add(-time.Minute)

	// Most recent attempt was a failure → critical (self-managed).
	failing := &model.Context{Archiver: &model.Archiver{LastArchivedTime: &past, LastFailedTime: &recent, FailedCount: 5}}
	f := has(Compute(failing), "archiving_failing")
	if f == nil || f.Severity != model.SeverityCritical {
		t.Fatalf("expected critical archiving_failing, got %+v", f)
	}

	// Same on RDS → downgraded to info + a can't-see-backups caveat (A15-0 rule 2).
	rds := &model.Context{Server: model.ServerInfo{Provider: "rds"}, Archiver: &model.Archiver{LastArchivedTime: &past, LastFailedTime: &recent, FailedCount: 5}}
	f = has(Compute(rds), "archiving_failing")
	if f == nil || f.Severity != model.SeverityInfo || len(f.Caveats) == 0 {
		t.Errorf("on rds, archiving_failing must be info + caveat, got %+v", f)
	}

	// A stale failure OLDER than the last success must not fire (no baseline needed).
	stale := &model.Context{Archiver: &model.Archiver{LastArchivedTime: &recent, LastFailedTime: &past, FailedCount: 5}}
	if has(Compute(stale), "archiving_failing") != nil {
		t.Error("a failure older than the last success must not fire critical")
	}

	// Compound: broken archiving + large pg_wal → extra evidence line.
	big := int64(20 << 30)
	compound := &model.Context{Archiver: &model.Archiver{LastArchivedTime: &past, LastFailedTime: &recent}, WAL: &model.WAL{DirBytes: &big}}
	f = has(Compute(compound), "archiving_failing")
	joined := ""
	for _, e := range f.Evidence {
		joined += e
	}
	if !strings.Contains(joined, "pg_wal is now") {
		t.Errorf("compound archiving+WAL should add a pg_wal evidence line: %v", f.Evidence)
	}

	// archive_mode=off, wal_level=replica, no replication → archiving_disabled (warn).
	off := &model.Context{Archiver: &model.Archiver{}, Settings: &model.Settings{Params: map[string]string{"archive_mode": "off", "wal_level": "replica"}}}
	if d := has(Compute(off), "archiving_disabled"); d == nil || d.Severity != model.SeverityWarn {
		t.Errorf("archive_mode=off should warn, got %+v", d)
	}
}


// The run-over-run failed_count delta must fire archiving_failing even when the
// timestamp says currently-succeeding (the intermittent-failure case).
func TestWalArchiving_deltaTrigger(t *testing.T) {
	recent := time.Now().Add(-time.Minute)
	c := &model.Context{
		Archiver: &model.Archiver{LastArchivedTime: &recent, FailedCount: 100}, // succeeding now
		Deltas:   &model.Deltas{Changes: []model.Delta{{ID: "archiver.failed_count", Before: 80, After: 100}}},
	}
	f := has(Compute(c), "archiving_failing")
	if f == nil || f.Severity != model.SeverityCritical {
		t.Fatalf("20 new failures since baseline must fire critical even while succeeding now, got %+v", f)
	}
	if !strings.Contains(f.Evidence[0], "new archiving failures") {
		t.Errorf("evidence should cite the delta: %v", f.Evidence)
	}
}
