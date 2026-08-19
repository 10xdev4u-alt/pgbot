package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestIndexVerdict_roundTripAndUpsert(t *testing.T) {
	s := openTemp(t)
	win := 12.0
	v := IndexVerdict{
		IndexID: "public.Job_externalIdNormalized_idx", Verdict: "not_found_in_code",
		Source: "agent_repo_search", RepoRef: "abc123",
		CheckedAt: time.Unix(1_700_000_000, 0), StatsWindowDays: &win,
	}
	if err := s.SaveIndexVerdict("fp1", v); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.LoadIndexVerdicts("fp1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	g, ok := got["public.Job_externalIdNormalized_idx"]
	if !ok {
		t.Fatal("verdict not loaded")
	}
	if g.Verdict != "not_found_in_code" || g.RepoRef != "abc123" || g.StatsWindowDays == nil || *g.StatsWindowDays != 12 {
		t.Errorf("round-trip mismatch: %+v", g)
	}

	// Upsert: re-record the same index with a newer verdict + wider window.
	win2 := 26.0
	if err := s.SaveIndexVerdict("fp1", IndexVerdict{
		IndexID: v.IndexID, Verdict: "found_in_code", CheckedAt: time.Unix(1_800_000_000, 0), StatsWindowDays: &win2,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, _ = s.LoadIndexVerdicts("fp1")
	if len(got) != 1 {
		t.Fatalf("upsert must replace, not duplicate: %d rows", len(got))
	}
	if got[v.IndexID].Verdict != "found_in_code" || *got[v.IndexID].StatsWindowDays != 26 {
		t.Errorf("latest verdict should win: %+v", got[v.IndexID])
	}
}

func TestIndexVerdict_isolatedByFingerprint(t *testing.T) {
	s := openTemp(t)
	_ = s.SaveIndexVerdict("fpA", IndexVerdict{IndexID: "public.a", Verdict: "found_in_code", CheckedAt: time.Now()})
	_ = s.SaveIndexVerdict("fpB", IndexVerdict{IndexID: "public.b", Verdict: "not_found_in_code", CheckedAt: time.Now()})
	a, _ := s.LoadIndexVerdicts("fpA")
	if len(a) != 1 || a["public.a"].Verdict != "found_in_code" {
		t.Errorf("fpA leaked or wrong: %+v", a)
	}
	if _, ok := a["public.b"]; ok {
		t.Error("fpB verdict must not appear under fpA")
	}
}

// The new migration must not disturb existing tables — a Save/List on snapshots
// still works after 006 is applied.
func TestMigration006_leavesExistingTablesUsable(t *testing.T) {
	s := openTemp(t)
	if _, err := s.LoadIndexVerdicts("none"); err != nil {
		t.Fatalf("index_verdicts table should exist and be queryable: %v", err)
	}
	// suppression_rules (005) still works.
	if _, err := s.RecordSuppressionUsage("fp", nil, nil, time.Now()); err != nil {
		t.Fatalf("existing suppression table broke after 006: %v", err)
	}
}
