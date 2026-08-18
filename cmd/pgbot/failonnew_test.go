package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func ctxOf(fs ...model.Finding) *model.Context {
	return &model.Context{SchemaVersion: model.SchemaVersion, Findings: fs}
}
func agg(id, sev string, objs ...string) model.Finding {
	return model.Finding{ID: id, Severity: sev, Objects: objs}
}
func single(id, sev, obj string) model.Finding {
	return model.Finding{ID: id, Severity: sev, Object: obj}
}

// Case 1: a finding absent from base is new. Case 2: an escalated severity is new.
func TestMarkPreexisting_newAndEscalated(t *testing.T) {
	base := ctxOf(single("index_invalid", "warn", "public.idx_a"))

	// absent from base
	h1 := ctxOf(single("fk_unindexed", "warn", "public.t"))
	markPreexisting(h1, base)
	if h1.Findings[0].Preexisting {
		t.Error("a finding absent from base must be new")
	}

	// present but severity escalated warn -> critical
	h2 := ctxOf(single("index_invalid", "critical", "public.idx_a"))
	markPreexisting(h2, base)
	if h2.Findings[0].Preexisting {
		t.Error("an escalated severity (warn→critical) is a regression, not preexisting")
	}

	// present, same severity -> preexisting
	h3 := ctxOf(single("index_invalid", "warn", "public.idx_a"))
	markPreexisting(h3, base)
	if !h3.Findings[0].Preexisting {
		t.Error("an unchanged finding must be preexisting")
	}
}

// DoD 11: a new Evidence entry inside an otherwise-existing aggregate must fail —
// the case a naive (id) diff misses.
func TestMarkPreexisting_aggregateNewRow(t *testing.T) {
	base := ctxOf(agg("fk_unindexed", "warn", "public.a", "public.b", "public.c"))

	// PR adds a fourth unindexed FK: the finding exists in both runs, but the
	// fourth row is new → the finding is NOT preexisting and must fail --fail-on.
	head := ctxOf(agg("fk_unindexed", "warn", "public.a", "public.b", "public.c", "public.d"))
	markPreexisting(head, base)
	if head.Findings[0].Preexisting {
		t.Fatal("a fourth aggregate row on top of three is new — the finding must not be preexisting (DoD 11)")
	}
	if exitCode(head.Findings, "warn") == 0 {
		t.Error("a new aggregate row must fail --fail-on=warn")
	}

	// Same three, no new row → preexisting, and the exit code stays clean.
	same := ctxOf(agg("fk_unindexed", "warn", "public.a", "public.b", "public.c"))
	markPreexisting(same, base)
	if !same.Findings[0].Preexisting {
		t.Error("an unchanged aggregate must be preexisting")
	}
	if exitCode(same.Findings, "warn") != 0 {
		t.Error("an unchanged aggregate must not move the exit code under --fail-on-new")
	}
}

// DoD 12: a base with a mismatched profile or an incompatible SchemaVersion is
// rejected, never silently compared.
func TestValidateBase_rejectsMismatch(t *testing.T) {
	head := &model.Context{SchemaVersion: "1.1.0", Profile: "schema"}

	if err := validateBase(&model.Context{SchemaVersion: "1.1.0", Profile: ""}, head); err == nil {
		t.Error("a full base compared against a schema head must be rejected")
	}
	if err := validateBase(&model.Context{SchemaVersion: "2.0.0", Profile: "schema"}, head); err == nil {
		t.Error("an incompatible major SchemaVersion must be rejected")
	}
	if err := validateBase(&model.Context{Profile: "schema"}, head); err == nil {
		t.Error("a base with no schema_version (not a pgbot report) must be rejected")
	}
	if err := validateBase(&model.Context{SchemaVersion: "1.0.0", Profile: "schema"}, head); err != nil {
		t.Errorf("same major version and same profile should compare: %v", err)
	}
}

// loadBaseReport round-trips a real report JSON.
func TestLoadBaseReport_roundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "base.json")
	want := ctxOf(single("fk_unindexed", "warn", "public.t"))
	want.Profile = "schema"
	raw, _ := json.Marshal(want)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadBaseReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Profile != "schema" || len(got.Findings) != 1 || got.Findings[0].ID != "fk_unindexed" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if _, err := loadBaseReport(filepath.Join(dir, "nope.json")); err == nil {
		t.Error("a missing base file must be a loud error")
	}
}
