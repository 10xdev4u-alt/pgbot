package collect_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pgrundev/pgbot/internal/collect"
	"github.com/pgrundev/pgbot/internal/conn"
)

// A confirmed false positive, found walking the install path for v0.2.0: pgbot
// samples through a small pool whose connections are all application_name='pgbot'
// and each briefly idle-in-transaction between short READ ONLY samples.
// activity.sql only dropped pg_backend_pid(), so sibling pool connections were
// counted as "idle in transaction" — a flaky finding on an otherwise-quiet
// database (0 one run, 2 the next). activity.sql now also excludes
// application_name='pgbot'. This proves a pgbot-labelled idle-in-transaction
// session is NOT counted, while a genuine external one still is.
func TestIntegration_idleInTransaction_excludesPgbotOwnBackends(t *testing.T) {
	d := dsn(t)
	ctx := context.Background()

	run := func() int {
		target, err := conn.Connect(ctx, d)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer target.Close()
		c, err := collect.Run(ctx, target, collect.Options{Interval: 500 * time.Millisecond, ASHHz: 0})
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		if c.Activity == nil {
			t.Fatal("no activity section")
		}
		return c.Activity.IdleInTransaction
	}

	// hold opens a connection with the given application_name and parks it
	// idle-in-transaction (persistent, so every sample sees it) until closed.
	hold := func(app string) func() {
		cfg, err := pgx.ParseConfig(d)
		if err != nil {
			t.Fatalf("parse dsn: %v", err)
		}
		cfg.RuntimeParams["application_name"] = app
		pc, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("hold connect: %v", err)
		}
		if _, err := pc.Exec(ctx, "BEGIN READ ONLY"); err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := pc.Exec(ctx, "SELECT 1"); err != nil { // -> idle in transaction
			t.Fatalf("select: %v", err)
		}
		return func() { _, _ = pc.Exec(ctx, "ROLLBACK"); pc.Close(ctx) }
	}

	base := run()

	// A genuine external idle-in-transaction session MUST be counted.
	closeReal := hold("pgbot_selftest_app")
	defer closeReal()
	if withReal := run(); withReal != base+1 {
		t.Fatalf("a real idle-in-transaction session must be counted: base=%d withReal=%d (want base+1)", base, withReal)
	}

	// A pgbot-labelled idle-in-transaction session must NOT be counted — this is
	// the regression: pgbot must never flag its own monitoring connections.
	closePgbot := hold("pgbot")
	defer closePgbot()
	if withBoth := run(); withBoth != base+1 {
		t.Fatalf("pgbot's own idle-in-transaction connection must be excluded: base=%d withBoth=%d (want base+1, i.e. only the real one)", base, withBoth)
	}
}
