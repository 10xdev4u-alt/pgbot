package render

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/pgrundev/pgbot/internal/model"
)

// Options carries the presentation inputs the render layer shouldn't compute
// itself: whether color is wanted, sparkline series pulled from the baseline
// store, and where that store lives (shown in the footer).
type Options struct {
	Color        bool
	Trends       map[string][]float64
	BaselinePath string
}

// styler applies lipgloss styles, or no-ops when color is disabled (NO_COLOR,
// non-TTY, or --no-color).
type styler struct{ on bool }

func (s styler) c(code string, bold bool, text string) string {
	if !s.on {
		return text
	}
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(code))
	if bold {
		st = st.Bold(true)
	}
	return st.Render(text)
}

func (s styler) dim(text string) string  { return s.c("240", false, text) }
func (s styler) head(text string) string { return s.c("39", true, text) }
func (s styler) crit(text string) string { return s.c("196", true, text) }
func (s styler) warn(text string) string { return s.c("208", true, text) }
func (s styler) info(text string) string { return s.c("245", false, text) }
func (s styler) good(text string) string { return s.c("42", false, text) }

// Terminal writes the human report: findings first (sentences), then sections.
func Terminal(w io.Writer, c *model.Context, opts Options) error {
	st := styler{on: opts.Color}
	var b strings.Builder

	// Header line.
	fmt.Fprintf(&b, "%s · %s · %s · %s\n\n",
		st.head("pgbot"),
		c.Server.Database,
		pgVersion(c.Server.VersionNum),
		c.CollectedAt.Format("2006-01-02 15:04 MST"))

	if !c.Server.HasPgMonitor {
		fmt.Fprintln(&b, st.warn("! role lacks pg_monitor — some stats are partial. Fix: GRANT pg_monitor TO <role>;"))
		fmt.Fprintln(&b)
	}

	renderFindings(&b, st, c.Findings)
	renderHealth(&b, st, c, opts)
	renderActivity(&b, st, c)
	renderLocks(&b, st, c)
	renderQueries(&b, st, c)
	renderTables(&b, st, c)
	renderIndexes(&b, st, c)
	renderInfra(&b, st, c)
	renderChanges(&b, st, c)

	// Footer.
	fmt.Fprintln(&b)
	if opts.BaselinePath != "" {
		fmt.Fprintln(&b, st.dim("baseline: "+opts.BaselinePath))
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func renderFindings(b *strings.Builder, st styler, fs []model.Finding) {
	if len(fs) == 0 {
		fmt.Fprintln(b, st.good("✓ no findings — nothing stood out"))
		fmt.Fprintln(b)
		return
	}
	crit, warn := 0, 0
	for _, f := range fs {
		switch f.Severity {
		case model.SeverityCritical:
			crit++
		case model.SeverityWarn:
			warn++
		}
	}
	summary := fmt.Sprintf("%d finding(s)", len(fs))
	if crit > 0 {
		summary += fmt.Sprintf(" · %d critical", crit)
	}
	if warn > 0 {
		summary += fmt.Sprintf(" · %d warning", warn)
	}
	fmt.Fprintln(b, st.head(summary))
	for _, f := range fs {
		icon, color := "·", st.info
		switch f.Severity {
		case model.SeverityCritical:
			icon, color = "⛔", st.crit
		case model.SeverityWarn:
			icon, color = "⚠", st.warn
		}
		fmt.Fprintf(b, "  %s %s\n", color(icon), color(f.Title))
		if f.Detail != "" {
			fmt.Fprintf(b, "     %s\n", st.dim(f.Detail))
		}
		if len(f.Evidence) > 0 {
			fmt.Fprintf(b, "     %s\n", st.dim(strings.Join(f.Evidence, ", ")))
		}
		if f.Impact != "" {
			fmt.Fprintf(b, "     %s\n", st.dim("→ "+f.Impact))
		}
	}
	fmt.Fprintln(b)
}

func section(b *strings.Builder, st styler, name string, sec model.Section) bool {
	label := st.head(strings.ToUpper(name))
	tag := st.dim(sec.Exactness)
	fmt.Fprintf(b, "%s  %s\n", label, tag)
	if sec.Exactness == model.ExactnessUnavailable {
		fmt.Fprintf(b, "  %s\n\n", st.dim(sec.Reason))
		return false
	}
	return true
}

func renderHealth(b *strings.Builder, st styler, c *model.Context, opts Options) {
	if c.Health == nil {
		return
	}
	h := c.Health
	if !section(b, st, "health", h.Section) {
		return
	}
	fmt.Fprintf(b, "  TPS %s   cache hit %s   connections %d   rollbacks %s\n",
		f2(h.TPS, humanNum), f2(h.CacheHitRatio, pct), h.Connections, f2(h.RollbackRatio, pct))
	fmt.Fprintf(b, "  writes %s/s   returned %s/s   deadlocks %s/min\n",
		f2(h.TupWrittenPerS, humanNum), f2(h.TupReturnedPerS, humanNum), f2(h.DeadlocksPerMin, humanNum))
	if spark := sparkline(opts.Trends["tps"]); spark != "" {
		fmt.Fprintf(b, "  %s %s\n", spark, st.dim("tps, recent runs"))
	}
	if c.Window.StatsWindowDays != nil && *c.Window.StatsWindowDays > 30 {
		fmt.Fprintf(b, "  %s\n", st.dim(fmt.Sprintf("(ratios span %.0f days since last stats reset)", *c.Window.StatsWindowDays)))
	}
	fmt.Fprintln(b)
}

func renderActivity(b *strings.Builder, st styler, c *model.Context) {
	if c.Activity == nil {
		return
	}
	a := c.Activity
	if !section(b, st, "activity", a.Section) {
		return
	}
	fmt.Fprintf(b, "  %d total · %d active · %d idle · %d idle-in-txn · %d waiting\n",
		a.Total, a.Active, a.Idle, a.IdleInTransaction, a.Waiting)
	if a.LongestXactSec > 0 {
		fmt.Fprintf(b, "  longest transaction %.0fs · longest active query %.0fs\n", a.LongestXactSec, a.LongestActiveSec)
	}
	fmt.Fprintln(b)
}

func renderLocks(b *strings.Builder, st styler, c *model.Context) {
	if c.Locks == nil || c.Locks.Exactness == model.ExactnessUnavailable {
		return
	}
	if c.Locks.BlockedCount == 0 {
		return // silence when clean; a blocking finding already fires when not
	}
	section(b, st, "locks", c.Locks.Section)
	tw := newTab(b)
	fmt.Fprintln(tw, "  blocked pid\tblocked by\twaited\tquery")
	for _, ch := range c.Locks.Chains {
		fmt.Fprintf(tw, "  %d\t%v\t%.0fs\t%s\n", ch.BlockedPID, ch.BlockingPIDs, ch.WaitSeconds, truncate(ch.BlockedQuery, 40))
	}
	tw.Flush()
	fmt.Fprintln(b)
}

func renderQueries(b *strings.Builder, st styler, c *model.Context) {
	if c.Queries == nil {
		return
	}
	if !section(b, st, "queries", c.Queries.Section) {
		return
	}
	if len(c.Queries.Top) == 0 {
		fmt.Fprintf(b, "  %s\n\n", st.dim("no query activity recorded yet"))
		return
	}
	tw := newTab(b)
	fmt.Fprintln(tw, "  calls\ttotal\tmean\tquery")
	shown := len(c.Queries.Top)
	if shown > 8 {
		shown = 8
	}
	for _, q := range c.Queries.Top[:shown] {
		fmt.Fprintf(tw, "  %s\t%s ms\t%.2f ms\t%s\n", humanNum(float64(q.Calls)), humanNum(q.TotalMS), q.MeanMS, truncate(q.Query, 48))
	}
	tw.Flush()
	if len(c.Queries.Top) > shown {
		fmt.Fprintf(b, "  %s\n", st.dim(fmt.Sprintf("… and %d more (see --json)", len(c.Queries.Top)-shown)))
	}
	fmt.Fprintln(b)
}

func renderTables(b *strings.Builder, st styler, c *model.Context) {
	if c.Tables == nil {
		return
	}
	if !section(b, st, "tables", c.Tables.Section) {
		return
	}
	fmt.Fprintf(b, "  database size %s\n", humanBytes(c.Tables.DBSizeBytes))
	tw := newTab(b)
	fmt.Fprintln(tw, "  table\tsize\trows\tdead\tseq/idx scans")
	for i, t := range c.Tables.Top {
		if i >= 6 {
			break
		}
		fmt.Fprintf(tw, "  %s.%s\t%s\t%s\t%s\t%s/%s\n",
			t.Schema, t.Name, humanBytes(t.TotalBytes), humanNum(float64(t.LiveTuples)),
			pct(t.DeadRatio), humanNum(float64(t.SeqScans)), humanNum(float64(t.IndexScans)))
	}
	tw.Flush()
	fmt.Fprintln(b)
}

func renderIndexes(b *strings.Builder, st styler, c *model.Context) {
	if c.Indexes == nil {
		return
	}
	if !section(b, st, "indexes", c.Indexes.Section) {
		return
	}
	var unusedBytes int64
	for _, ix := range c.Indexes.Unused {
		unusedBytes += ix.Bytes
	}
	fmt.Fprintf(b, "  %d total · %s unused (%s)\n", c.Indexes.Total, humanNum(float64(len(c.Indexes.Unused))), humanBytes(unusedBytes))
	fmt.Fprintln(b)
}

func renderInfra(b *strings.Builder, st styler, c *model.Context) {
	// WAL / IO / replication / settings condensed to a couple of lines each.
	if c.WAL != nil && c.WAL.Exactness == model.ExactnessSampled {
		fmt.Fprintf(b, "%s  %s   WAL %s/s\n", st.head("WAL"), st.dim(c.WAL.Exactness), f2(c.WAL.BytesPerSec, humanBytes2))
	}
	if c.IO != nil && c.IO.Exactness == model.ExactnessSampled {
		fmt.Fprintf(b, "%s   %s   buffers written %s/s · checkpoints %d timed / %d req\n",
			st.head("IO"), st.dim(c.IO.Exactness), f2(c.IO.BuffersWrittenPerS, humanNum), c.IO.CheckpointsTimed, c.IO.CheckpointsReq)
	}
	if c.Replication != nil && c.Replication.Exactness == model.ExactnessScraped {
		if c.Replication.IsReplica {
			fmt.Fprintf(b, "%s  %s   replica · receiver lag %s\n", st.head("REPLICATION"), st.dim("scraped"), f2(c.Replication.ReceiverLagSec, func(v float64) string { return fmt.Sprintf("%.1fs", v) }))
		} else if len(c.Replication.Replicas) > 0 {
			fmt.Fprintf(b, "%s  %s   %d standby(s) connected\n", st.head("REPLICATION"), st.dim("scraped"), len(c.Replication.Replicas))
		}
	}
	if c.Settings != nil && len(c.Settings.Overrides) > 0 {
		keys := make([]string, 0, len(c.Settings.Overrides))
		for k := range c.Settings.Overrides {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(b, "%s  %s   %d non-default parameters (see --json)\n", st.head("SETTINGS"), st.dim("scraped"), len(keys))
	}
	fmt.Fprintln(b)
}

func renderChanges(b *strings.Builder, st styler, c *model.Context) {
	if c.Deltas == nil {
		return
	}
	if len(c.Deltas.Changes) == 0 {
		fmt.Fprintf(b, "%s  %s\n", st.head("CHANGES"), st.dim("nothing material changed since "+c.Deltas.Against.Format("15:04")))
		return
	}
	fmt.Fprintf(b, "%s since %s\n", st.head("CHANGES"), c.Deltas.Against.Format("2006-01-02 15:04"))
	for _, d := range c.Deltas.Changes {
		color := st.info
		if d.Severity == model.SeverityWarn {
			color = st.warn
		}
		change := fmt.Sprintf("%s → %s", humanNum(d.Before), humanNum(d.After))
		if d.PctChange != nil {
			change += fmt.Sprintf(" (%+.0f%%)", *d.PctChange*100)
		}
		fmt.Fprintf(b, "  %s %s  %s  %s\n", color("·"), d.Subject, change, st.dim(d.Note))
	}
}

// helpers

func newTab(b *strings.Builder) *tabwriter.Writer {
	return tabwriter.NewWriter(b, 0, 2, 2, ' ', 0)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func humanBytes2(v float64) string { return humanBytes(int64(v)) }

func pgVersion(num int) string {
	if num == 0 {
		return "PostgreSQL"
	}
	major := num / 10000
	minor := num % 100
	return fmt.Sprintf("PostgreSQL %d.%d", major, minor)
}

var _ = time.Now // keep time imported for future use
