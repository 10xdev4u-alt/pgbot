package config

import (
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func fixedNow() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) }

func find(fs []model.Finding, id string) *model.Finding {
	for i := range fs {
		if fs[i].ID == id {
			return &fs[i]
		}
	}
	return nil
}

func TestApply_severityRemap(t *testing.T) {
	c := &Config{Severity: map[string]string{"unused_indexes": "info"}}
	fs := []model.Finding{{ID: "unused_indexes", Severity: model.SeverityWarn}}
	fs = c.Apply(fs, fixedNow())
	f := find(fs, "unused_indexes")
	if f.Severity != "info" {
		t.Errorf("severity not remapped: %q", f.Severity)
	}
	if f.SeverityRemapped != model.SeverityWarn {
		t.Errorf("original severity not recorded: %q", f.SeverityRemapped)
	}
	if f.Suppressed {
		t.Error("remap must not suppress")
	}
}

func TestApply_ignorePrecedence(t *testing.T) {
	// exact object beats glob beats omitted-object.
	c := &Config{Ignore: []IgnoreRule{
		{Finding: "unused_indexes", Reason: "all"},                                // omitted → spec 1
		{Finding: "unused_indexes", Object: "public.idx_*", Reason: "glob"},       // glob → spec 2
		{Finding: "unused_indexes", Object: "public.idx_legacy", Reason: "exact"}, // exact → spec 3
	}}
	fs := []model.Finding{{ID: "unused_indexes", Object: "public.idx_legacy", Severity: model.SeverityWarn}}
	fs = c.Apply(fs, fixedNow())
	f := find(fs, "unused_indexes")
	if !f.Suppressed || f.SuppressionReason != "exact" {
		t.Errorf("most specific rule should win, got suppressed=%v reason=%q", f.Suppressed, f.SuppressionReason)
	}

	// An object the exact/glob rules don't match falls back to the omitted rule.
	fs = []model.Finding{{ID: "unused_indexes", Object: "public.other", Severity: model.SeverityWarn}}
	fs = c.Apply(fs, fixedNow())
	if f := find(fs, "unused_indexes"); !f.Suppressed || f.SuppressionReason != "all" {
		t.Errorf("omitted-object rule should catch the rest, got reason=%q", f.SuppressionReason)
	}
}

// Aggregate findings suppress per row: an object rule drops only matching entries
// and the finding survives on the rest, with the leading count corrected.
func TestApply_aggregatePartialSuppression(t *testing.T) {
	mk := func() []model.Finding {
		return []model.Finding{{
			ID: "sequence_exhaustion", Severity: model.SeverityWarn,
			Title:    "3 sequence(s) near exhaustion (worst 95%)",
			Evidence: []string{"public.a: 95%", "public.b: 88%", "public.c: 82%"},
			Objects:  []string{"public.a", "public.b", "public.c"},
		}}
	}

	// Drop just one object → finding survives with 2, count fixed, caveat added.
	c := &Config{Ignore: []IgnoreRule{{Finding: "sequence_exhaustion", Object: "public.b", Reason: "bigint"}}}
	f := find(c.Apply(mk(), fixedNow()), "sequence_exhaustion")
	if f.Suppressed {
		t.Fatal("dropping one of three rows must not suppress the whole finding")
	}
	if len(f.Objects) != 2 || f.Objects[0] != "public.a" || f.Objects[1] != "public.c" {
		t.Errorf("wrong survivors: %v", f.Objects)
	}
	if len(f.Evidence) != 2 {
		t.Errorf("evidence not filtered in lockstep: %v", f.Evidence)
	}
	if !strings.HasPrefix(f.Title, "2 sequence(s)") {
		t.Errorf("leading count not corrected: %q", f.Title)
	}
	var noted bool
	for _, cav := range f.Caveats {
		if strings.Contains(cav, "public.b") && strings.Contains(cav, "suppressed") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("partial suppression must be noted, caveats=%v", f.Caveats)
	}

	// A rule with NO object mutes the whole aggregate.
	c = &Config{Ignore: []IgnoreRule{{Finding: "sequence_exhaustion", Reason: "all"}}}
	if f := find(c.Apply(mk(), fixedNow()), "sequence_exhaustion"); !f.Suppressed {
		t.Error("a rule with no object should suppress the whole aggregate")
	}

	// A glob matching every object suppresses the whole finding too.
	c = &Config{Ignore: []IgnoreRule{{Finding: "sequence_exhaustion", Object: "public.*"}}}
	if f := find(c.Apply(mk(), fixedNow()), "sequence_exhaustion"); !f.Suppressed {
		t.Error("a glob matching all rows should suppress the whole finding")
	}
}

func TestApply_globDoesNotMatchDifferentObject(t *testing.T) {
	c := &Config{Ignore: []IgnoreRule{{Finding: "unused_indexes", Object: "public.idx_*"}}}
	fs := []model.Finding{{ID: "unused_indexes", Object: "public.orders_pkey", Severity: model.SeverityWarn}}
	fs = c.Apply(fs, fixedNow())
	if find(fs, "unused_indexes").Suppressed {
		t.Error("glob public.idx_* must not match public.orders_pkey")
	}
}

func TestApply_expiredRuleDoesNotApply(t *testing.T) {
	c := &Config{Ignore: []IgnoreRule{
		{Finding: "checksums_disabled", Object: "setting:data_checksums", Reason: "temp", Expires: "2026-08-15"},
	}}
	fs := []model.Finding{{ID: "checksums_disabled", Object: "setting:data_checksums", Severity: model.SeverityInfo}}
	// now = 2026-08-16 → the 08-15 rule has expired.
	fs = c.Apply(fs, fixedNow())
	if find(fs, "checksums_disabled").Suppressed {
		t.Error("an expired rule must not suppress")
	}
	// The rule is active on its expiry date itself.
	onDate := time.Date(2026, 8, 15, 23, 0, 0, 0, time.UTC)
	fs = []model.Finding{{ID: "checksums_disabled", Object: "setting:data_checksums", Severity: model.SeverityInfo}}
	fs = c.Apply(fs, onDate)
	if !find(fs, "checksums_disabled").Suppressed {
		t.Error("a rule should be active through its whole expiry day")
	}
}

// A suppressed finding must record enough for an agent to explain itself.
func TestApply_recordsRuleAndReason(t *testing.T) {
	c := &Config{Ignore: []IgnoreRule{{Finding: "io_timing_off", Object: "setting:track_io_timing", Reason: "managed provider"}}}
	fs := []model.Finding{{ID: "io_timing_off", Object: "setting:track_io_timing", Severity: model.SeverityInfo}}
	fs = c.Apply(fs, fixedNow())
	f := find(fs, "io_timing_off")
	if f.SuppressionRule != "io_timing_off object=setting:track_io_timing" {
		t.Errorf("rule identity not recorded: %q", f.SuppressionRule)
	}
	if f.SuppressionReason != "managed provider" {
		t.Errorf("reason not recorded: %q", f.SuppressionReason)
	}
}

func TestExpiredFindings(t *testing.T) {
	c := &Config{Ignore: []IgnoreRule{
		{Finding: "checksums_disabled", Expires: "2026-08-15", Reason: "temp"}, // expired at fixedNow
		{Finding: "unused_indexes", Expires: "2027-01-01"},                     // still valid
		{Finding: "io_timing_off"},                                             // no expiry
	}}
	fs := c.ExpiredFindings(fixedNow())
	if len(fs) != 1 {
		t.Fatalf("expected exactly one expired finding, got %d: %+v", len(fs), fs)
	}
	if fs[0].ID != "suppression_expired" || fs[0].Severity != model.SeverityInfo {
		t.Errorf("wrong meta-finding: %+v", fs[0])
	}
	if !contains(fs[0].Title, "checksums_disabled") {
		t.Errorf("expired finding should name the rule: %q", fs[0].Title)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestAddInlineIgnores(t *testing.T) {
	c := Default()
	c.AddInlineIgnores([]string{"unused_indexes", "checksums_disabled:setting:data_checksums", "", "  "})
	if len(c.Ignore) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(c.Ignore))
	}
	if c.Ignore[1].Finding != "checksums_disabled" || c.Ignore[1].Object != "setting:data_checksums" {
		t.Errorf("finding:object parse wrong: %+v", c.Ignore[1])
	}
}
