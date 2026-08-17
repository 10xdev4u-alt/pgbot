package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pgrundev/pgbot/internal/diff"
	"github.com/pgrundev/pgbot/internal/model"
	"github.com/pgrundev/pgbot/internal/render"
	"github.com/pgrundev/pgbot/internal/store"
	"github.com/spf13/cobra"
)

type diffFlags struct {
	since       time.Duration
	fingerprint string
	storePath   string
	noColor     bool
	json        bool
	force       bool
}

func newDiffCmd() *cobra.Command {
	var f diffFlags
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Compare the latest baseline snapshot against an earlier one (offline)",
		Long: "Diffs the most recent stored snapshot against the one nearest --since ago, from\n" +
			"the local baseline store — no connection needed. It prints the interval it\n" +
			"actually compared (not the one you asked for), and says up front when a stats\n" +
			"reset or pg_stat_statements eviction between the snapshots makes specific deltas\n" +
			"untrustworthy. It never compares two different databases.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDiff(f)
		},
	}
	fl := cmd.Flags()
	fl.DurationVar(&f.since, "since", 24*time.Hour, "compare against the snapshot nearest this far back")
	fl.StringVar(&f.fingerprint, "fingerprint", "", "which database (fingerprint or a unique prefix); required if the store holds more than one")
	fl.StringVar(&f.storePath, "store", "", "baseline DB path (default: XDG state dir)")
	fl.BoolVar(&f.noColor, "no-color", false, "disable ANSI color")
	fl.BoolVar(&f.json, "json", false, "emit the diff as JSON")
	fl.BoolVar(&f.force, "force", false, "allow a diff whose snapshots span fingerprints (different databases) — refused by default")
	return cmd
}

func runDiff(f diffFlags) error {
	st, err := store.Open(f.storePath)
	if err != nil {
		return fmt.Errorf("open baseline store: %w", err)
	}
	defer st.Close()

	items, err := st.List()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("no baselines yet — run `pgbot inspect` first")
	}

	item, err := resolveFingerprint(items, f.fingerprint)
	if err != nil {
		return err
	}

	current, err := st.Previous(item.Fingerprint, time.Now().UTC(), 0)
	if err != nil || current == nil {
		return fmt.Errorf("no snapshot for %s", item.Fingerprint)
	}
	baseline, err := st.Previous(item.Fingerprint, current.CollectedAt, f.since)
	if err != nil {
		return err
	}
	if baseline == nil || baseline.ID == current.ID {
		return fmt.Errorf("not enough history: no snapshot at least %s before the latest (%s). "+
			"This database's oldest snapshot is %s back — try a smaller --since",
			render.HumanDur(f.since), current.CollectedAt.Format("2006-01-02 15:04"),
			render.HumanDur(current.CollectedAt.Sub(item.Oldest)))
	}

	// P0-1 was about a fingerprint collision comparing two databases; the command
	// must not reintroduce it. Both snapshots come from one resolved fingerprint,
	// so this can't trip in normal use — it's a defensive backstop.
	if baseline.Fingerprint != current.Fingerprint && !f.force {
		return fmt.Errorf("refusing to diff across fingerprints (%s vs %s) — different databases; pass --force only if you mean it",
			baseline.Fingerprint, current.Fingerprint)
	}

	deltas := diff.Compute(current.Context, &diff.Baseline{CollectedAt: baseline.CollectedAt, Context: baseline.Context}, nil)
	resetReason := diff.StatsResetBetween(baseline.Context, current.Context)
	pgssEvicted := pgssEvictedBetween(baseline.Context, current.Context)
	actual := current.CollectedAt.Sub(baseline.CollectedAt)

	if f.json {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"database":            item.Database,
			"fingerprint":         item.Fingerprint,
			"baseline_at":         baseline.CollectedAt,
			"current_at":          current.CollectedAt,
			"requested_seconds":   int64(f.since.Seconds()),
			"actual_seconds":      int64(actual.Seconds()),
			"stats_reset_between": resetReason,
			"pgss_evicted":        pgssEvicted,
			"changes":             deltasOrEmpty(deltas),
		})
	}

	render.DiffReport(os.Stdout, render.DiffInput{
		Color: useColor(f.noColor), Database: item.Database, Fingerprint: item.Fingerprint,
		BaselineAt: baseline.CollectedAt, CurrentAt: current.CollectedAt,
		Requested: f.since, Actual: actual,
		ResetReason: resetReason, PgssEvicted: pgssEvicted, Deltas: deltas,
	})
	return nil
}

// resolveFingerprint picks the target database. With one in the store it's
// unambiguous; with several a fingerprint (or a unique prefix, or the database
// name) must be given, and an ambiguous or unknown one is a clear error — never a
// silent pick.
func resolveFingerprint(items []store.ListItem, spec string) (store.ListItem, error) {
	if spec == "" {
		if len(items) == 1 {
			return items[0], nil
		}
		return store.ListItem{}, fmt.Errorf("the store holds %d databases — pass --fingerprint:\n%s", len(items), listDatabases(items))
	}
	var matches []store.ListItem
	for _, it := range items {
		if it.Fingerprint == spec || strings.HasPrefix(it.Fingerprint, spec) || it.Database == spec {
			matches = append(matches, it)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return store.ListItem{}, fmt.Errorf("no database matches %q:\n%s", spec, listDatabases(items))
	default:
		return store.ListItem{}, fmt.Errorf("%q is ambiguous (%d matches) — use a longer prefix", spec, len(matches))
	}
}

func listDatabases(items []store.ListItem) string {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "  %-14s %s (%d snapshots)\n", it.Fingerprint[:min(14, len(it.Fingerprint))], it.Database, it.Count)
	}
	return b.String()
}

// pgssEvictedBetween reports whether pg_stat_statements evicted entries between
// the two snapshots (dealloc rose), which makes per-query deltas incomplete.
func pgssEvictedBetween(base, cur *model.Context) bool {
	if base == nil || cur == nil || base.Queries == nil || cur.Queries == nil {
		return false
	}
	return cur.Queries.PgssDealloc > base.Queries.PgssDealloc
}

func deltasOrEmpty(d *model.Deltas) []model.Delta {
	if d == nil {
		return []model.Delta{}
	}
	return d.Changes
}
