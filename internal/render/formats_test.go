package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenrumney/go-sarif/v2/sarif"
	"github.com/pgrundev/pgbot/internal/model"
)

// sampleContextForFormats is a fixed Context spanning the cases the machine
// formats must handle: a critical, a warn aggregate, an info, and a suppressed
// finding, with and without an Object.
func sampleContextForFormats() *model.Context {
	return &model.Context{
		SchemaVersion: model.SchemaVersion,
		Server:        model.ServerInfo{Database: "app", VersionNum: 180000},
		Findings: []model.Finding{
			{
				ID: "fsync_off", Object: "setting:fsync", Severity: model.SeverityCritical,
				Title: "fsync is OFF — a crash can corrupt the database", Remediation: "Set fsync = on.",
				Impact: model.Impact{Dimension: model.DimRisk, Score: 95},
			},
			{
				ID: "unused_indexes", Severity: model.SeverityWarn,
				Title: "2 unused index(es)", Remediation: "Drop with DROP INDEX CONCURRENTLY.",
				Objects: []string{"public.idx_a", "public.idx_b"},
				Impact:  model.Impact{Dimension: model.DimStorage, Score: 60},
			},
			{
				ID: "io_timing_off", Object: "setting:track_io_timing", Severity: model.SeverityInfo,
				Title: "track_io_timing is off", Impact: model.Impact{Dimension: model.DimThroughput, Score: 15},
			},
			{
				ID: "checksums_disabled", Object: "setting:data_checksums", Severity: model.SeverityInfo,
				Title: "data checksums are off", Suppressed: true, SuppressionReason: "managed provider",
				Impact: model.Impact{Dimension: model.DimRisk, Score: 10},
			},
		},
	}
}

// golden compares got to the committed testdata file, regenerating it when
// UPDATE_GOLDEN=1 (so a deliberate format change is a reviewable diff).
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v — regenerate with UPDATE_GOLDEN=1", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s does not match golden — run UPDATE_GOLDEN=1 to review the diff", name)
	}
}

func TestSARIF_golden(t *testing.T) {
	var buf bytes.Buffer
	if err := SARIF(&buf, sampleContextForFormats()); err != nil {
		t.Fatal(err)
	}
	golden(t, "sample.sarif", buf.Bytes())

	// Re-parse with the SARIF library: valid by round-trip, and assert the mapping.
	report, err := sarif.FromBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("emitted SARIF does not parse as valid SARIF: %v", err)
	}
	if report.Version != "2.1.0" || len(report.Runs) != 1 {
		t.Fatalf("wrong SARIF envelope: version=%s runs=%d", report.Version, len(report.Runs))
	}
	run := report.Runs[0]
	if len(run.Results) != 4 {
		t.Errorf("expected 4 results, got %d", len(run.Results))
	}
	// critical → error, and it carries the docs helpUri via its rule.
	var sawErrorFsync, sawSuppressed bool
	for _, r := range run.Results {
		if r.RuleID != nil && *r.RuleID == "fsync_off" && r.Level != nil && *r.Level == "error" {
			sawErrorFsync = true
		}
		if len(r.Suppressions) > 0 {
			sawSuppressed = true
		}
	}
	if !sawErrorFsync {
		t.Error("fsync_off should be a SARIF error result")
	}
	if !sawSuppressed {
		t.Error("the suppressed finding must appear with a SARIF suppression, not be dropped")
	}
	if !strings.Contains(buf.String(), "docs/findings/fsync_off.md") {
		t.Error("rule helpUri should link to the catalogue page")
	}
}

func TestJUnit_golden(t *testing.T) {
	var buf bytes.Buffer
	if err := JUnit(&buf, sampleContextForFormats(), "warn"); err != nil {
		t.Fatal(err)
	}
	golden(t, "sample.junit.xml", buf.Bytes())

	s := buf.String()
	// fail-on=warn: the critical + warn are failures (2), the info is a passing
	// case, the suppressed is skipped.
	if !strings.Contains(s, `failures="2"`) {
		t.Errorf("expected 2 failures at --fail-on=warn:\n%s", s)
	}
	if !strings.Contains(s, "<skipped") {
		t.Error("a suppressed finding must render as skipped, not a failure")
	}
}

// fail-on gates which findings become JUnit failures.
func TestJUnit_failOnCritical(t *testing.T) {
	var buf bytes.Buffer
	if err := JUnit(&buf, sampleContextForFormats(), "critical"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `failures="1"`) {
		t.Errorf("--fail-on=critical: only the critical is a failure:\n%s", buf.String())
	}
}
