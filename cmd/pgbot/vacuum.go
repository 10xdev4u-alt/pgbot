package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
	"github.com/pgrundev/pgbot/internal/render"
	"github.com/spf13/cobra"
)

// Postgres' default autovacuum trigger: a table is eligible once its dead-tuple
// count passes threshold + scale_factor * live_tuples. Per-table storage params
// can override these, but the cluster defaults cover the overwhelming majority.
const (
	avDefaultThreshold   = 50
	avDefaultScaleFactor = 0.2
)

// newVacuumCmd — `pgbot vacuum`. Autovacuum health per table: dead tuples, when
// autovacuum last ran, and whether Postgres would now expect it to fire. Rising
// dead tuples with expect=yes and no recent run means autovacuum is behind.
func newVacuumCmd() *cobra.Command {
	var f inspectFlags
	cmd := &cobra.Command{
		Use:   "vacuum <connection-string>",
		Short: "Autovacuum health per table — dead tuples, last run, and whether it's due",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVacuum(cmd, args, f)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.noColor, "no-color", false, "disable ANSI color")
	return cmd
}

func runVacuum(cmd *cobra.Command, args []string, f inspectFlags) error {
	connString := firstNonEmpty(argAt(args, 0), os.Getenv("DATABASE_URL"), os.Getenv("PGBOT_DATABASE_URL"))
	if connString == "" {
		return fmt.Errorf("no connection string (pass one or set $DATABASE_URL)")
	}
	f.ashHz = 0
	f.noStore = true
	f.interval = time.Second

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	c, host, err := gather(ctx, connString, f)
	if err != nil {
		return err
	}
	if host == "" {
		host = c.Server.Database
	}
	st := render.NewStyler(useColor(f.noColor))

	if c.Tables == nil || len(c.Tables.Top) == 0 {
		fmt.Println(st.Dim("no user tables visible (need SELECT on pg_stat_user_tables)"))
		return nil
	}

	tbls := append([]model.TableStat(nil), c.Tables.Top...)
	sort.SliceStable(tbls, func(i, j int) bool { return tbls[i].DeadTuples > tbls[j].DeadTuples })

	behind := 0
	for _, t := range tbls {
		if expectAutovacuum(t.LiveTuples, t.DeadTuples) {
			behind++
		}
	}

	summary := st.Good("autovacuum keeping up")
	if behind > 0 {
		summary = st.Warn(fmt.Sprintf("%d table(s) past the autovacuum threshold", behind))
	}
	fmt.Printf("%s · %s · %s\n\n", st.Head(host), pgVersionShort(c.Server.VersionNum), summary)

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  table\tlive\tdead\tdead%\tlast autovacuum\tdue?")
	for _, t := range tbls {
		due := st.Dim("no")
		if expectAutovacuum(t.LiveTuples, t.DeadTuples) {
			due = st.Warn("yes")
		}
		name := t.Schema + "." + t.Name
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%.1f%%\t%s\t%s\n",
			truncStr(name, 40), humanCount(t.LiveTuples), humanCount(t.DeadTuples),
			t.DeadRatio*100, agoStr(t.LastAutovac), due)
	}
	tw.Flush()
	fmt.Println()
	fmt.Println(st.Dim("due? = dead tuples past Postgres' default autovacuum trigger (50 + 20% of live rows)."))
	fmt.Println(st.Dim("tables ranked by dead tuples, from the 30 largest by size."))
	return nil
}

func expectAutovacuum(live, dead int64) bool {
	return float64(dead) > avDefaultThreshold+avDefaultScaleFactor*float64(live)
}

// agoStr renders how long ago a timestamp was, or "never" if unset.
func agoStr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "never"
	}
	d := time.Since(*t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours())/24)
	}
}
