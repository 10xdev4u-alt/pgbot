package main

import (
	"fmt"
	"io"

	"github.com/pgrundev/pgbot/internal/correlate"
	"github.com/pgrundev/pgbot/internal/model"
	"github.com/pgrundev/pgbot/internal/render"
	"github.com/pgrundev/pgbot/internal/store"
)

// loadIndexVerdicts reads prior code-search verdicts for a database from the
// local store. Best-effort: a missing/unreadable store just means no history —
// correlation still works, it simply can't say "evidence has strengthened".
func loadIndexVerdicts(storePath, fingerprint string) map[string]correlate.PriorVerdict {
	if fingerprint == "" {
		return nil
	}
	st, err := store.Open(storePath)
	if err != nil {
		return nil
	}
	defer st.Close()
	raw, err := st.LoadIndexVerdicts(fingerprint)
	if err != nil {
		return nil
	}
	out := make(map[string]correlate.PriorVerdict, len(raw))
	for k, v := range raw {
		out[k] = correlate.PriorVerdict{
			Verdict: v.Verdict, Source: v.Source, RepoRef: v.RepoRef,
			CheckedAt: v.CheckedAt, StatsWindowDays: v.StatsWindowDays,
		}
	}
	return out
}

// buildCorrelation assembles the index/code correlation report, folding in any
// prior verdicts from the store.
func buildCorrelation(c *model.Context, storePath string) correlate.Report {
	return correlate.Build(c, loadIndexVerdicts(storePath, c.Fingerprint))
}

// renderCorrelation prints the report grouped by confidence, highest first. The
// grouping is the point: catalog_proven is safe, needs_code_check is a task for
// the agent, inconclusive must not be dropped on this evidence.
func renderCorrelation(w io.Writer, st render.Styler, rep correlate.Report) {
	head := "Index / code correlation"
	if rep.StatsWindowDays != nil {
		head += fmt.Sprintf(" — %.0fd window", *rep.StatsWindowDays)
	}
	fmt.Fprintf(w, "%s\n\n", st.Head(head))

	if rep.ColdWindow {
		fmt.Fprintln(w, st.AI("⚠ cold statistics window — zero-scan counts just started; nothing is actionable yet."))
	}
	if rep.ReplicationActive {
		fmt.Fprintln(w, st.AI("⚠ replication active — scan counts are per-node; a replica may use an index that looks unused here."))
	}
	if rep.ColdWindow || rep.ReplicationActive {
		fmt.Fprintln(w)
	}

	groups := []struct {
		conf  correlate.Confidence
		label string
	}{
		{correlate.CatalogProven, st.Good("CATALOG-PROVEN") + " — provable from the catalog alone, no code check"},
		{correlate.NeedsCodeCheck, "NEEDS CODE CHECK — grep the terms, then decide"},
		{correlate.Inconclusive, st.Dim("INCONCLUSIVE — do NOT drop on this evidence")},
	}
	any := false
	for _, g := range groups {
		var rows []correlate.IndexReport
		for _, ix := range rep.Indexes {
			if ix.Confidence == g.conf {
				rows = append(rows, ix)
			}
		}
		if len(rows) == 0 {
			continue
		}
		any = true
		fmt.Fprintf(w, "%s (%d)\n", g.label, len(rows))
		for _, ix := range rows {
			loc := ix.Schema + "." + ix.Table
			fmt.Fprintf(w, "  ● %s  %s\n", ix.Index, st.Dim(fmt.Sprintf("(%s · %s)", loc, render.HumanBytes(ix.SizeBytes))))
			fmt.Fprintf(w, "    %s\n", ix.Reason)
			if len(ix.SearchTerms) > 0 {
				fmt.Fprintf(w, "    %s %v\n", st.Dim("search:"), ix.SearchTerms)
				fmt.Fprintf(w, "    %s\n", st.Dim("in filters only (WHERE/JOIN/ORDER BY/GROUP BY), never SELECT lists"))
			}
			if ix.IfNotFound != "" {
				fmt.Fprintf(w, "    %s %s\n", st.Dim("if absent:"), st.Dim(ix.IfNotFound))
			}
			if v := ix.PriorVerdict; v != nil {
				stale := ""
				if v.Stale {
					stale = st.AI(" — STALE")
				}
				ref := ""
				if v.RepoRef != "" {
					ref = ", commit " + v.RepoRef
				}
				fmt.Fprintf(w, "    %s %s (%dd old%s)%s\n", st.Dim("prior code search:"), v.Verdict, v.AgeDays, ref, stale)
				// The corroboration/staleness sentence only rides a needs_code_check note.
				if ix.Note != "" && ix.Confidence == correlate.NeedsCodeCheck {
					fmt.Fprintf(w, "    %s\n", st.Dim(ix.Note))
				}
			}
		}
		fmt.Fprintln(w)
	}
	if !any {
		fmt.Fprintln(w, st.Good("✓ no unused, redundant, or invalid indexes to correlate in this window"))
	}
}
