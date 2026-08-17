package conn

import (
	"strings"
	"testing"
)

func TestSelfPIDs_addRemoveList(t *testing.T) {
	s := newSelfPIDs()
	s.add(20)
	s.add(10)
	s.add(10) // dup ignored
	s.add(0)  // 0 ignored (no PID yet)
	if got := s.list(); len(got) != 2 || got[0] != 10 || got[1] != 20 {
		t.Fatalf("want sorted [10 20], got %v", got)
	}
	s.remove(10)
	if got := s.list(); len(got) != 1 || got[0] != 20 {
		t.Fatalf("after remove want [20], got %v", got)
	}
}

func TestExcludeSelf_rewrite(t *testing.T) {
	sql := "SELECT 1 FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND state='active'"

	// No tracked PIDs (e.g. before the pool warms) -> unchanged, so we never
	// over-exclude: the fallback is exactly the old querying-backend filter.
	if got := (&Target{}).ExcludeSelf(sql); got != sql {
		t.Errorf("empty set must leave SQL unchanged, got:\n%s", got)
	}

	// Populated -> the querying-backend filter is widened to the whole pool, PIDs
	// inlined and sorted.
	tgt := &Target{self: newSelfPIDs()}
	tgt.self.add(42)
	tgt.self.add(7)
	got := tgt.ExcludeSelf(sql)
	if !strings.Contains(got, "pid <> ALL(ARRAY[7,42]::int[])") {
		t.Errorf("expected inlined pool PIDs, got:\n%s", got)
	}
	if !strings.Contains(got, "pid <> pg_backend_pid()") {
		t.Error("must keep the original backend filter as a subset")
	}
	if !strings.Contains(got, "state='active'") {
		t.Error("the rest of the query must be preserved")
	}
}
