// Command schemagen generates the JSON Schema for pgbot's public JSON contracts
// from the Go types themselves — a hand-written schema drifts the moment a field
// is added, so this is the single source. Run `go run ./tools/schemagen`; the
// committed output under schema/ is diffed against a fresh generation by
// TestSchema_matchesModel (the CI drift guard, B7-2 DoD 10).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pgrundev/pgbot/internal/advisor"
	"github.com/pgrundev/pgbot/internal/model"
)

func main() {
	dir := "schema"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	writes := []struct {
		name string
		blob []byte
	}{
		{fmt.Sprintf("pgbot-context-%s.json", model.SchemaVersion), model.ContextSchema()},
		{fmt.Sprintf("pgbot-advise-%s.json", advisor.SchemaVersion), advisor.ReportSchema()},
	}
	for _, w := range writes {
		p := filepath.Join(dir, w.name)
		if err := os.WriteFile(p, w.blob, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", p)
	}
}
