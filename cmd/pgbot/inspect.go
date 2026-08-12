package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/pgrundev/pgbot/internal/collect"
	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/diff"
	"github.com/pgrundev/pgbot/internal/findings"
	"github.com/pgrundev/pgbot/internal/model"
	"github.com/pgrundev/pgbot/internal/render"
	"github.com/pgrundev/pgbot/internal/store"
	"github.com/spf13/cobra"
)

// Exit-code contract (CI users depend on these):
const (
	exitClean    = 0 // no findings above info
	exitWarn     = 1 // warnings present
	exitCritical = 2 // critical findings present
	exitFailure  = 3 // connection / execution failure
)

type inspectFlags struct {
	json       bool
	interval   time.Duration
	noColor    bool
	noStore    bool
	storePath  string
	rawQueries bool
}

func newInspectCmd() *cobra.Command {
	var f inspectFlags
	cmd := &cobra.Command{
		Use:   "inspect <connection-string>",
		Short: "Collect a full in-database health report",
		Long: "Connect read-only, sample the statistics views, and print a findings-first\n" +
			"report (or --json). Writes a baseline snapshot so later runs show what changed.\n\n" +
			"The connection string may be a URL (postgres://...) or a libpq DSN, or set\n" +
			"$DATABASE_URL and omit the argument. Use a role holding pg_monitor and no write grants.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd, args, f)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.json, "json", false, "emit the versioned Context as JSON (the agent/script contract)")
	fl.DurationVar(&f.interval, "interval", time.Second, "gap between the two counter samples (min 500ms)")
	fl.BoolVar(&f.noColor, "no-color", false, "disable ANSI color")
	fl.BoolVar(&f.noStore, "no-store", false, "do not read or write the local baseline store")
	fl.StringVar(&f.storePath, "store", "", "baseline DB path (default: XDG state dir)")
	fl.BoolVar(&f.rawQueries, "raw-query-text", false, "keep raw pg_stat_activity query text (never sent anywhere; PII risk)")
	return cmd
}

func runInspect(cmd *cobra.Command, args []string, f inspectFlags) error {
	connString := firstNonEmpty(argAt(args, 0), os.Getenv("DATABASE_URL"), os.Getenv("PGBOT_DATABASE_URL"))
	if connString == "" {
		return fmt.Errorf("no connection string (pass one or set $DATABASE_URL)")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	target, err := conn.Connect(ctx, connString)
	if err != nil {
		return fmt.Errorf("connect: %s", conn.RedactConnString(err.Error()))
	}
	defer target.Close()

	c, err := collect.Run(ctx, target, collect.Options{Interval: f.interval})
	if err != nil {
		return fmt.Errorf("collect: %s", conn.RedactConnString(err.Error()))
	}

	// Fingerprint the target so baselines survive host/rename changes.
	host, port := hostPort(target)
	c.Fingerprint = store.Fingerprint(host, port, c.Server.Database, target.Caps.SystemIdentifier)

	// Baseline: diff against history, then persist this run. Store trouble is
	// non-fatal — a broken local DB must never stop a health report.
	var trends map[string][]float64
	var baselinePath string
	if !f.noStore {
		trends, baselinePath = withStore(f.storePath, c)
	}

	// Deterministic findings — computed in Go, never by a model.
	c.Findings = findings.Compute(c)

	if f.json {
		if err := render.JSON(os.Stdout, c); err != nil {
			return err
		}
	} else {
		opts := render.Options{Color: useColor(f.noColor), Trends: trends, BaselinePath: baselinePath}
		if err := render.Terminal(os.Stdout, c, opts); err != nil {
			return err
		}
	}

	os.Exit(exitCode(c.Findings))
	return nil
}

// withStore loads the baseline (for Deltas + sparkline trends) and persists this
// run. It mutates c.Deltas in place and returns sparkline series + the store
// path for the footer. All store errors are swallowed after a stderr note.
func withStore(path string, c *model.Context) (map[string][]float64, string) {
	st, err := store.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pgbot: baseline store unavailable: "+err.Error())
		return nil, ""
	}
	defer st.Close()

	now := c.CollectedAt
	// Prefer a baseline at least 15 minutes old (avoids same-minute noise); fall
	// back to the most recent prior run so two back-to-back inspects still diff.
	// This runs before Save, so "most recent" is genuinely an earlier run.
	prev, _ := st.Previous(c.Fingerprint, now, 15*time.Minute)
	if prev == nil {
		prev, _ = st.Previous(c.Fingerprint, now, 0)
	}
	if prev != nil {
		var yday *diff.Baseline
		if y, err := st.SameHourYesterday(c.Fingerprint, now); err == nil && y != nil {
			yday = &diff.Baseline{CollectedAt: y.CollectedAt, Context: y.Context}
		}
		c.Deltas = diff.Compute(c, &diff.Baseline{CollectedAt: prev.CollectedAt, Context: prev.Context}, yday)
	}

	if _, err := st.Save(c); err != nil {
		fmt.Fprintln(os.Stderr, "pgbot: could not write baseline: "+err.Error())
	}

	trends := map[string][]float64{}
	for _, col := range []string{"tps", "cache_hit", "connections", "db_size_bytes"} {
		if series, err := st.Trend(c.Fingerprint, col, 24); err == nil && len(series) > 1 {
			trends[col] = series
		}
	}
	return trends, st.Path()
}

// exitCode maps the worst finding severity to the CI contract.
func exitCode(fs []model.Finding) int {
	worst := exitClean
	for _, f := range fs {
		switch f.Severity {
		case model.SeverityCritical:
			return exitCritical
		case model.SeverityWarn:
			worst = exitWarn
		}
	}
	return worst
}
