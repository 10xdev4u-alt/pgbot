package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pgrundev/pgbot/docs"
	"github.com/pgrundev/pgbot/internal/findings"
	"github.com/spf13/cobra"
)

// newExplainFindingCmd prints a finding's catalogue page from the binary itself —
// no network, so it works air-gapped where this tool often runs.
func newExplainFindingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain-finding <finding_id>",
		Short: "Print the catalogue page for a finding (offline; every report line references one)",
		Long: "Prints the docs/findings/<id> page — what pgbot observed, why it matters, how to\n" +
			"verify it yourself, how to fix it, when to ignore it, and what pgbot cannot see. The\n" +
			"pages are embedded in the binary, so this works with no network access.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Print(findingIndex())
				return nil
			}
			id := args[0]
			data, err := docs.Findings.ReadFile("findings/" + id + ".md")
			if err != nil {
				return fmt.Errorf("no catalogue page for %q — run `pgbot explain-finding` with no argument to list finding ids", id)
			}
			fmt.Println(stripFrontMatter(string(data)))
			return nil
		},
	}
}

// findingIndex lists the finding ids that have a page, for the no-argument form.
func findingIndex() string {
	entries, _ := docs.Findings.ReadDir("findings")
	var ids []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".md") && n != "README.md" && !strings.HasPrefix(n, "_") {
			id := strings.TrimSuffix(n, ".md")
			if findings.KnownID(id) {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	var b strings.Builder
	fmt.Fprintf(&b, "%d findings — `pgbot explain-finding <id>` for any:\n\n", len(ids))
	for _, id := range ids {
		fmt.Fprintf(&b, "  %-32s %s\n", id, findings.Summary(id))
	}
	return b.String()
}

// stripFrontMatter removes the leading YAML front-matter block so the terminal
// output starts at the H1.
func stripFrontMatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return strings.TrimRight(s, "\n")
	}
	if i := strings.Index(s[4:], "\n---"); i >= 0 {
		rest := s[4+i+4:]
		return strings.TrimLeft(strings.TrimRight(rest, "\n"), "\n")
	}
	return strings.TrimRight(s, "\n")
}
