package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/pgrundev/pgbot/internal/store"
	"github.com/spf13/cobra"
)

// newBaselinesCmd exposes the local store so users can see and delete what's
// kept on their machine — a privacy affordance, not just a debugging aid.
func newBaselinesCmd() *cobra.Command {
	var storePath string
	cmd := &cobra.Command{
		Use:   "baselines",
		Short: "Inspect and manage the local baseline store",
	}
	cmd.PersistentFlags().StringVar(&storePath, "store", "", "baseline DB path (default: XDG state dir)")

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List stored databases and snapshot counts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := store.Open(storePath)
			if err != nil {
				return err
			}
			defer st.Close()
			items, err := st.List()
			if err != nil {
				return err
			}
			if len(items) == 0 {
				fmt.Println("no baselines stored yet — run `pgbot inspect` first")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "FINGERPRINT\tDATABASE\tSNAPSHOTS\tOLDEST\tNEWEST")
			for _, it := range items {
				fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", it.Fingerprint, it.Database, it.Count,
					it.Oldest.Format("2006-01-02 15:04"), it.Newest.Format("2006-01-02 15:04"))
			}
			return tw.Flush()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "prune <fingerprint>",
		Short: "Delete all snapshots for one database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(storePath)
			if err != nil {
				return err
			}
			defer st.Close()
			n, err := st.Prune(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("deleted %d snapshot(s) for %s\n", n, args[0])
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "export <fingerprint>",
		Short: "Write all stored snapshots for one database as JSON to stdout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(storePath)
			if err != nil {
				return err
			}
			defer st.Close()
			return st.Export(args[0], os.Stdout)
		},
	})

	return cmd
}
