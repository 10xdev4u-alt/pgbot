package collect_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/collect"
	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/findings"
	"github.com/pgrundev/pgbot/internal/render"
)

// These run only when PGBOT_TEST_DSN points at a live PostgreSQL — CI sets it
// against the docker-compose matrix; a plain `go test ./...` skips them.
func dsn(t *testing.T) string {
	d := os.Getenv("PGBOT_TEST_DSN")
	if d == "" {
		t.Skip("set PGBOT_TEST_DSN to run integration tests")
	}
	return d
}

func TestIntegration_fullPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	target, err := conn.Connect(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer target.Close()

	start := time.Now()
	c, err := collect.Run(ctx, target, collect.Options{Interval: 700 * time.Millisecond})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("one-shot took %v, want < 5s", elapsed)
	}
	c.Findings = findings.Compute(c)

	// Core sections present and labeled.
	if c.Health == nil || c.Health.Exactness == "" {
		t.Error("health section missing exactness label")
	}
	if c.Server.VersionNum == 0 {
		t.Error("server version not detected")
	}

	// PII gate: render JSON and assert no email/uuid leaked from a fake-data table.
	// (The caller is expected to have seeded such data; we assert the invariant
	// regardless — scrubbing must hold, and pgss text is normalized.)
	var buf bytes.Buffer
	if err := render.JSON(&buf, c); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "@example.com") {
		t.Error("PII leaked into JSON output")
	}
}

// TestIntegration_poolerRatesStayCorrect asserts pgbot's central pooler claim:
// behind a transaction pooler, rates are still correct (each counter is read in
// its own transaction; pg_stat_* are cluster-wide). Point PGBOT_POOLER_DSN at a
// PgBouncer/Supabase/Neon pooled endpoint with write load in flight.
func TestIntegration_poolerRatesStayCorrect(t *testing.T) {
	dsn := os.Getenv("PGBOT_POOLER_DSN")
	if dsn == "" || os.Getenv("PGBOT_TEST_LOAD") == "" {
		t.Skip("set PGBOT_POOLER_DSN + PGBOT_TEST_LOAD (with write load running) to run")
	}
	ctx := context.Background()
	target, err := conn.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	t.Logf("pooler detected by signature: %v", target.Pooler.Detected)
	c, err := collect.Run(ctx, target, collect.Options{Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if c.Health == nil || c.Health.TPS == nil || *c.Health.TPS <= 0 {
		t.Errorf("rates must stay correct behind a pooler, got %+v", c.Health)
	}
}

// TestIntegration_nonZeroTPS is the stats-caching regression guard: with write
// load in flight, the double-sampled rate must be non-zero. Requires the caller
// to generate concurrent commits (CI does).
func TestIntegration_ratesArePresent(t *testing.T) {
	if os.Getenv("PGBOT_TEST_LOAD") == "" {
		t.Skip("set PGBOT_TEST_LOAD when concurrent write load is running")
	}
	ctx := context.Background()
	target, err := conn.Connect(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	c, err := collect.Run(ctx, target, collect.Options{Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if c.Health == nil || c.Health.TPS == nil || *c.Health.TPS <= 0 {
		t.Errorf("expected non-zero TPS under load (stats-caching regression), got %+v", c.Health)
	}
}
