package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

// DoD 12: `pgbot config init` output must load cleanly under `pgbot config check`
// (i.e. Load with zero warnings). Every finding is seeded as a COMMENTED rule, so
// the active config is just schema=1.
func TestInitTemplate_roundTripsCleanly(t *testing.T) {
	fs := []model.Finding{
		{ID: "checksums_disabled", Object: "setting:data_checksums", Title: "checksums off"},
		{ID: "unused_indexes", Object: "", Title: "3 unused indexes"},
		{ID: "replication_slot_inactive", Object: "slot:wal2json", Title: "inactive slot"},
		{ID: "unused_indexes", Object: "", Title: "dup — should dedup"},
	}
	tmpl := InitTemplate(fs, time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC))

	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PGBOT_CONFIG", "")
	p := filepath.Join(dir, ".pgbot.toml")
	if err := os.WriteFile(p, []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("init template must load: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("init template must be warning-free, got: %v", cfg.Warnings)
	}
	// Everything is commented, so it suppresses nothing and overrides nothing.
	if len(cfg.Ignore) != 0 || len(cfg.Severity) != 0 || len(cfg.ThresholdOverrides) != 0 {
		t.Errorf("init template should be inert until edited: %+v", cfg)
	}
	// The finding's object is present (as a comment) so uncommenting is one edit.
	if !strings.Contains(tmpl, `# object  = "setting:data_checksums"`) {
		t.Error("seeded rule should carry the finding's object")
	}
	// Deduped: only one unused_indexes block.
	if strings.Count(tmpl, `# finding = "unused_indexes"`) != 1 {
		t.Error("summary findings should seed exactly one rule")
	}
}

func TestExplain_reportsMatchingRuleAndReason(t *testing.T) {
	c := &Config{Ignore: []IgnoreRule{
		{Finding: "checksums_disabled", Object: "setting:data_checksums", Reason: "managed provider"},
	}, Severity: map[string]string{}}
	out := c.Explain("checksums_disabled", "setting:data_checksums", fixedNow())
	if !strings.Contains(out, "SUPPRESSED") || !strings.Contains(out, "managed provider") {
		t.Errorf("explain should report the firing rule + reason:\n%s", out)
	}

	// A glob that doesn't match should say so, not just "no rule".
	c2 := &Config{Ignore: []IgnoreRule{{Finding: "unused_indexes", Object: "public.idx_*"}}, Severity: map[string]string{}}
	out = c2.Explain("unused_indexes", "public.orders_pkey", fixedNow())
	if !strings.Contains(out, "NOT suppressed") || !strings.Contains(out, "doesn't match") {
		t.Errorf("explain should diagnose a non-matching glob:\n%s", out)
	}
}

func TestHygieneWarnings_flagsMissingExpiry(t *testing.T) {
	c := &Config{Ignore: []IgnoreRule{
		{Finding: "a", Expires: "2026-12-31"},
		{Finding: "b"}, // no expiry
	}}
	w := c.HygieneWarnings()
	if len(w) != 1 || !strings.Contains(w[0], "no `expires`") {
		t.Errorf("expected one missing-expiry warning, got %v", w)
	}
}
