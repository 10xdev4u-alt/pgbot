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

// Under --fail-on-new a Preexisting finding must not fail the test pane: the
// exit code ignores it, so the pane must agree. It stays visible as a passing
// testcase (unlike SARIF, which omits it entirely).
func TestJUnit_preexistingNotAFailure(t *testing.T) {
	c := sampleContextForFormats()
	c.Findings[0].Preexisting = true // the critical fsync_off is old news
	var buf bytes.Buffer
	if err := JUnit(&buf, c, "critical"); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.Contains(s, `failures="0"`) {
		t.Errorf("a preexisting finding must not fail the pane:\n%s", s)
	}
	if !strings.Contains(s, `name="fsync_off setting:fsync"`) {
		t.Errorf("the preexisting finding must stay visible as a testcase:\n%s", s)
	}
	if strings.Contains(s, "<failure") {
		t.Errorf("no <failure> element expected when the only gated finding is preexisting:\n%s", s)
	}
}

// A finding that is BOTH suppressed and preexisting must render as <skipped>
// (suppression stays visible), not fall through to the silent preexisting pass —
// the switch order in JUnit decides this, and nothing else guards it.
func TestJUnit_suppressedWinsOverPreexisting(t *testing.T) {
	c := sampleContextForFormats()
	c.Findings[0].Preexisting = true
	c.Findings[0].Suppressed = true
	c.Findings[0].SuppressionReason = "accepted risk"
	var buf bytes.Buffer
	if err := JUnit(&buf, c, "critical"); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.Contains(s, "suppressed by config: accepted risk") {
		t.Errorf("suppressed+preexisting must render as <skipped> with the reason:\n%s", s)
	}
	if strings.Contains(s, "<failure") {
		t.Errorf("suppressed+preexisting must never be a <failure>:\n%s", s)
	}
}

func TestPrometheus_golden(t *testing.T) {
	c := sampleContextForFormats()
	// add a gauge source so a metric line is exercised
	hit := 0.994
	c.Health = &model.Health{CacheHitRatio: &hit}
	var buf bytes.Buffer
	if err := Prometheus(&buf, c); err != nil {
		t.Fatal(err)
	}
	golden(t, "sample.prom", buf.Bytes())
	s := buf.String()
	// every finding is a series; the suppressed one carries suppressed="true".
	if !strings.Contains(s, `pgbot_finding{database="app",id="fsync_off"`) {
		t.Error("missing pgbot_finding series")
	}
	if !strings.Contains(s, `id="checksums_disabled"`) || !strings.Contains(s, `suppressed="true"`) {
		t.Error("suppressed finding must be exported with suppressed=\"true\", not omitted")
	}
	if !strings.Contains(s, "pgbot_cache_hit_ratio{") {
		t.Error("underlying gauges should be exported")
	}
}

// A label value containing a quote or backslash must be escaped exactly once.
// The old code ran esc() and then %q, double-escaping: a"b came out as a\\\"b,
// which Prometheus parses as a different (wrong) label value.
func TestPrometheus_labelEscaping(t *testing.T) {
	c := sampleContextForFormats()
	c.Server.Database = `a"b\c`
	var buf bytes.Buffer
	if err := Prometheus(&buf, c); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	want := `database="a\"b\\c"`
	if !strings.Contains(s, want) {
		t.Errorf("label must be single-escaped %s, got:\n%s", want, s)
	}
	if strings.Contains(s, `\\\"`) {
		t.Errorf("double-escaped label value found:\n%s", s)
	}
}

// Under --all-databases the exposition must stay valid: one # HELP/# TYPE per
// metric name across every database, with each database's samples grouped beneath
// (a per-database block would repeat the headers and the textfile collector would
// reject the whole file).
func TestPrometheusAll_groupedHeaders(t *testing.T) {
	a := sampleContextForFormats()
	b := sampleContextForFormats()
	b.Server.Database = "analytics"
	hit := 0.99
	a.Health = &model.Health{CacheHitRatio: &hit}
	b.Health = &model.Health{CacheHitRatio: &hit}

	var buf bytes.Buffer
	if err := PrometheusAll(&buf, []*model.Context{a, b}); err != nil {
		t.Fatal(err)
	}
	help := map[string]int{}
	typ := map[string]int{}
	for _, ln := range strings.Split(buf.String(), "\n") {
		if f := strings.TrimPrefix(ln, "# HELP "); f != ln {
			help[strings.Fields(f)[0]]++
		} else if f := strings.TrimPrefix(ln, "# TYPE "); f != ln {
			typ[strings.Fields(f)[0]]++
		}
	}
	for name, n := range help {
		if n != 1 {
			t.Errorf("# HELP %s appears %d times; the text format allows exactly one", name, n)
		}
	}
	for name, n := range typ {
		if n != 1 {
			t.Errorf("# TYPE %s appears %d times; the text format allows exactly one", name, n)
		}
	}
	// Both databases must still be present as distinct label sets.
	if !strings.Contains(buf.String(), `database="app"`) || !strings.Contains(buf.String(), `database="analytics"`) {
		t.Error("each database's series must be present under its own database label")
	}
}
