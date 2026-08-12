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
