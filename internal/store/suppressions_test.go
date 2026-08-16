package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecordSuppressionUsage_deadRuleDetection(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fp := "fp-1"
	active := []string{"unused_indexes object=public.idx_a", "checksums_disabled object=*"}
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	// Run 1: only the checksums rule matches; idx_a matches nothing.
	unused, err := st.RecordSuppressionUsage(fp, active, map[string]bool{"checksums_disabled object=*": true}, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(unused) != 0 {
		t.Fatalf("nothing should be flagged after 1 miss, got %v", unused)
	}

	// Runs 2..5: idx_a keeps matching nothing → dead after SuppressionUnusedAfter.
	for i := 1; i < SuppressionUnusedAfter; i++ {
		unused, err = st.RecordSuppressionUsage(fp, active, map[string]bool{"checksums_disabled object=*": true}, base.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(unused) != 1 || unused[0] != "unused_indexes object=public.idx_a" {
		t.Fatalf("idx_a rule should be flagged dead after %d misses, got %v", SuppressionUnusedAfter, unused)
	}

	// A matching run resets the counter: no longer dead.
	unused, err = st.RecordSuppressionUsage(fp, active, map[string]bool{
		"unused_indexes object=public.idx_a": true,
		"checksums_disabled object=*":        true,
	}, base.Add(10*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(unused) != 0 {
		t.Fatalf("a fresh match must reset the miss counter, got %v", unused)
	}
}

func TestRecordSuppressionUsage_prunesEditedRules(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fp := "fp-2"
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	// Rule exists and misses for a while.
	old := []string{"unused_indexes object=public.old"}
	for i := 0; i < SuppressionUnusedAfter; i++ {
		if _, err := st.RecordSuppressionUsage(fp, old, nil, now.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// The user edits the config; the rule is gone. Its history must be pruned so
	// it can't re-fire — a new rule set with no overlap yields nothing.
	unused, err := st.RecordSuppressionUsage(fp, []string{"table_bloat object=*"}, nil, now.Add(100*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range unused {
		if u == "unused_indexes object=public.old" {
			t.Error("a removed rule must be pruned, not re-flagged")
		}
	}
}
