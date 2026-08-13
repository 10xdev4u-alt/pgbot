package render

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func sampleContext() *model.Context {
	tps := 1200.0
	hit := 0.994
	return &model.Context{
		SchemaVersion: model.SchemaVersion,
		CollectedAt:   time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
		Server:        model.ServerInfo{VersionNum: 170010, Database: "app", HasPgMonitor: true},
		Window:        model.Window{SampleSeconds: 1.0},
		Health:        &model.Health{Section: model.Section{Exactness: model.ExactnessSampled}, Connections: 24, TPS: &tps, CacheHitRatio: &hit},
		Activity:      &model.Activity{Section: model.Section{Exactness: model.ExactnessScraped}, Total: 24, Active: 6},
		Findings: []model.Finding{
			{ID: "unused_indexes", Severity: model.SeverityWarn, Title: "1 unused index",
				Impact: model.Impact{Score: 60, Dimension: model.DimStorage, Estimate: "≈4.2 GiB reclaimable"}},
		},
	}
}

func TestTerminal_groupedIsDefault(t *testing.T) {
	var buf bytes.Buffer
	if err := Terminal(&buf, sampleContext(), Options{Color: false}); err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile("\x1b\\[").MatchString(buf.String()) {
		t.Error("no-color output must contain no ANSI escapes")
	}
	out := buf.String()
	// Grouped view: header, a health score, the warning group with the finding
	// title bulleted, a GOOD list, and the --full pointer. No section tables.
	for _, want := range []string{"connected", "postgres 17", "Database health:", "/100", "WARNING", "● 1 unused index", "GOOD", "--full"} {
		if !strings.Contains(out, want) {
			t.Errorf("grouped output missing %q", want)
		}
	}
	if strings.Contains(out, "HEALTH  ") || strings.Contains(out, "ACTIVITY  ") {
		t.Error("default grouped view must not print section tables")
	}
}

func TestTerminal_fullShowsSectionsAndCaveats(t *testing.T) {
	c := sampleContext()
	c.Findings = []model.Finding{{
		ID: "unused_indexes", Severity: model.SeverityWarn, Title: "3 unused indexes",
		Confidence: 0.4, Caveats: []string{"replication is active — per-node counts only"},
		Impact: model.Impact{Score: 50, Dimension: model.DimStorage, Estimate: "≈43 GB"},
	}}
	var buf bytes.Buffer
	if err := Terminal(&buf, c, Options{Color: false, Full: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// --full carries the detail the dashboard omits: sections, caveats inline,
	// and the low-confidence "(possible)" marker.
	for _, want := range []string{"HEALTH", "cache hit", "3 unused indexes", "replication is active", "(possible)"} {
		if !strings.Contains(out, want) {
			t.Errorf("--full output missing %q", want)
		}
	}
}

func TestFull_leadsWithStatusBoard(t *testing.T) {
	c := sampleContext()
	c.Locks = &model.Locks{BlockedCount: 2, Chains: []model.BlockingRow{{BlockedPID: 91}}}
	c.Findings = append(c.Findings, model.Finding{
		ID: "blocking_chains", Severity: model.SeverityCritical, Title: "2 blocked",
		Impact: model.Impact{Dimension: model.DimRisk, Score: 90},
	})
	var buf bytes.Buffer
	if err := Terminal(&buf, c, Options{Color: false, Full: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// Box-drawn header + subsystem rows; locks reads "fail" (its finding is critical).
	for _, want := range []string{"┌", "┼", "subsystem", "status", "connections", "cache", "locks", "2 blocked", "fail"} {
		if !strings.Contains(out, want) {
			t.Errorf("status board missing %q", want)
		}
	}
	// The board is the LEAD of --full: it must appear before the section tables.
	if bi, hi := strings.Index(out, "subsystem"), strings.Index(out, "HEALTH"); bi < 0 || (hi >= 0 && bi > hi) {
		t.Error("status board should lead --full, before the section tables")
	}
}

func TestTerminal_cleanGroupedScoresHighAndListsGood(t *testing.T) {
	c := sampleContext()
	c.Findings = nil
	var buf bytes.Buffer
	if err := Terminal(&buf, c, Options{Color: false}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// No findings → perfect score, no CRITICAL/WARNING groups, a GOOD list that
	// names the healthy cache hit with its value.
	for _, want := range []string{"100/100", "GOOD", "cache hit ratio 99.4%"} {
		if !strings.Contains(out, want) {
			t.Errorf("clean grouped view missing %q", want)
		}
	}
	if strings.Contains(out, "CRITICAL") || strings.Contains(out, "WARNING") {
		t.Error("a clean database must show no CRITICAL/WARNING groups")
	}
}

func TestSparkline(t *testing.T) {
	if s := sparkline(nil); s != "" {
		t.Errorf("empty series -> empty spark, got %q", s)
	}
	s := sparkline([]float64{1, 2, 3, 4, 5})
	if len([]rune(s)) != 5 {
		t.Errorf("expected 5 spark runes, got %q", s)
	}
}
