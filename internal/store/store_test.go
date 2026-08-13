package store

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func ctxAt(fp string, at time.Time, tps float64) *model.Context {
	v := tps
	return &model.Context{
		SchemaVersion: model.SchemaVersion, Fingerprint: fp, CollectedAt: at,
		Health: &model.Health{Connections: 10, TPS: &v},
		Tables: &model.Tables{DBSizeBytes: 1 << 30},
	}
}

func TestSaveAndPrevious(t *testing.T) {
	st := tempStore(t)
	now := time.Now().UTC()
	fp := "abc123"

	if _, err := st.Save(ctxAt(fp, now.Add(-30*time.Minute), 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Save(ctxAt(fp, now, 200)); err != nil {
		t.Fatal(err)
	}

	prev, err := st.Previous(fp, now, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if prev == nil {
		t.Fatal("expected a baseline ≥15min old")
	}
	if prev.Context.Health.TPS == nil || *prev.Context.Health.TPS != 100 {
		t.Errorf("expected the 30-min-old snapshot (tps 100), got %+v", prev.Context.Health)
	}
}

func TestFingerprint_prefersSystemIdentifier(t *testing.T) {
	a := Fingerprint("h1", "5432", "db", "sysid-42")
	b := Fingerprint("DIFFERENT-HOST", "6000", "db", "sysid-42")
	if a != b {
		t.Error("same system identifier must yield the same fingerprint regardless of host")
	}
	c := Fingerprint("h1", "5432", "db", "")
	if c == a {
		t.Error("fallback fingerprint should differ from the system-identifier one")
	}
}

func TestTrend_guardsColumnName(t *testing.T) {
	st := tempStore(t)
	if _, err := st.Trend("fp", "tps; DROP TABLE snapshots", 10); err == nil {
		t.Error("Trend must reject an unknown/injected column name")
	}
}

func TestListPruneExport(t *testing.T) {
	st := tempStore(t)
	fp := "fp1"
	for i := 0; i < 3; i++ {
		if _, err := st.Save(ctxAt(fp, time.Now().UTC().Add(time.Duration(-i)*time.Hour), float64(100*i))); err != nil {
			t.Fatal(err)
		}
	}
	items, err := st.List()
	if err != nil || len(items) != 1 || items[0].Count != 3 {
		t.Fatalf("expected 1 group of 3, got %+v (%v)", items, err)
	}

	var buf bytes.Buffer
	if err := st.Export(fp, &buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"schema_version"`)) {
		t.Error("export should contain full Context JSON")
	}

	n, err := st.Prune(fp)
	if err != nil || n != 3 {
		t.Fatalf("expected to prune 3, got %d (%v)", n, err)
	}
	if items, _ := st.List(); len(items) != 0 {
		t.Errorf("store should be empty after prune, got %+v", items)
	}
}

func TestSaveWaitProfile_accumulateAndFold(t *testing.T) {
	st := tempStore(t)
	fp := "waitfp"
	prof := func(lock, cpu int) *model.WaitProfile {
		return &model.WaitProfile{Available: true, Samples: lock + cpu, Buckets: []model.WaitBucket{
			{Type: "Lock", Count: lock, Events: []model.WaitEvent{{Event: "transactionid", Count: lock}}},
			{Type: "CPU", Count: cpu},
		}}
	}
	// Two profiles in the SAME minute accumulate into one minute bucket.
	base := time.Date(2026, 8, 12, 10, 30, 20, 0, time.UTC)
	if err := st.SaveWaitProfile(fp, base, prof(30, 10)); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWaitProfile(fp, base.Add(15*time.Second), prof(10, 5)); err != nil {
		t.Fatal(err)
	}
	var lockSamples int
	err := st.db.QueryRow(`SELECT samples FROM wait_rollups
		WHERE target_id=? AND granularity='minute' AND wait_type='Lock' AND wait_event='transactionid'`, fp).Scan(&lockSamples)
	if err != nil {
		t.Fatal(err)
	}
	if lockSamples != 40 {
		t.Errorf("same-minute Lock samples should accumulate to 40, got %d", lockSamples)
	}

	// A profile far in the past should end up folded into an hourly bucket by a
	// later run's prune (minute rows past the 7d horizon are folded).
	old := base.Add(-10 * 24 * time.Hour)
	if err := st.SaveWaitProfile(fp, old, prof(5, 5)); err != nil {
		t.Fatal(err)
	}
	// Trigger prune again with a current-time save so the old minute rows fold.
	if err := st.SaveWaitProfile(fp, base.Add(2*time.Minute), prof(1, 1)); err != nil {
		t.Fatal(err)
	}
	var oldMinute, oldHour int
	st.db.QueryRow(`SELECT count(*) FROM wait_rollups WHERE target_id=? AND granularity='minute' AND bucket_ts < ?`,
		fp, base.Add(-7*24*time.Hour).Unix()).Scan(&oldMinute)
	st.db.QueryRow(`SELECT count(*) FROM wait_rollups WHERE target_id=? AND granularity='hour'`, fp).Scan(&oldHour)
	if oldMinute != 0 {
		t.Errorf("aged minute rows should be folded away, %d remain", oldMinute)
	}
	if oldHour == 0 {
		t.Error("aged minute rows should have been folded into hourly buckets")
	}
}

func TestPrune_thinsAgedToHourlyAndDropsAncient(t *testing.T) {
	st := tempStore(t)
	fp := "ret"
	now := time.Now().UTC()
	save := func(at time.Time) {
		if _, err := st.Save(ctxAt(fp, at, 1)); err != nil {
			t.Fatal(err)
		}
	}

	// Past the 90-day horizon → dropped (even on its own Save's prune).
	save(now.Add(-100 * 24 * time.Hour))
	// Three in the SAME hour ~8 days ago (older than the 7-day full-resolution
	// window) → thinned to one per hour bucket.
	oldHour := now.Add(-8 * 24 * time.Hour).Truncate(time.Hour).Add(30 * time.Minute)
	save(oldHour)
	save(oldHour.Add(1 * time.Minute))
	save(oldHour.Add(2 * time.Minute))
	// Recent → kept at full resolution.
	save(now.Add(-1 * time.Hour))

	count := func(where string, args ...any) int {
		var n int
		if err := st.db.QueryRow("SELECT count(*) FROM snapshots WHERE fingerprint=? AND "+where, append([]any{fp}, args...)...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("collected_at < ?", now.Add(-90*24*time.Hour).Unix()); n != 0 {
		t.Errorf("snapshots past the 90-day horizon should be dropped, %d remain", n)
	}
	if n := count("collected_at BETWEEN ? AND ?", oldHour.Add(-time.Hour).Unix(), oldHour.Add(time.Hour).Unix()); n != 1 {
		t.Errorf("three same-hour aged snapshots should thin to 1, got %d", n)
	}
	if n := count("collected_at > ?", now.Add(-2*time.Hour).Unix()); n != 1 {
		t.Errorf("the recent snapshot should be kept, got %d", n)
	}
}

func TestTrendAndSameHourYesterday(t *testing.T) {
	st := tempStore(t)
	fp := "trend"
	now := time.Now().UTC()

	// A yesterday-same-hour snapshot for the diff baseline.
	yday := now.Add(-24 * time.Hour)
	if _, err := st.Save(ctxAt(fp, yday, 500)); err != nil {
		t.Fatal(err)
	}
	// A short recent series for the sparkline trend.
	for i, tps := range []float64{100, 200, 300} {
		if _, err := st.Save(ctxAt(fp, now.Add(time.Duration(-2+i)*time.Minute), tps)); err != nil {
			t.Fatal(err)
		}
	}

	series, err := st.Trend(fp, "tps", 24)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) < 3 {
		t.Errorf("Trend should return the recent tps series, got %v", series)
	}

	snap, err := st.SameHourYesterday(fp, now)
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil {
		t.Fatal("SameHourYesterday should find the ~24h-old snapshot")
	}
	if snap.Context.Health == nil || snap.Context.Health.TPS == nil || *snap.Context.Health.TPS != 500 {
		t.Errorf("wrong yesterday snapshot: %+v", snap.Context.Health)
	}
}
