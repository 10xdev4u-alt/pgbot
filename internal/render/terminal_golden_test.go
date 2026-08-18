package render

import (
	"bytes"
	"testing"
)

// Exact-snapshot lock on the human report — the substring tests prove the pieces
// are present; this proves the whole layout doesn't drift unnoticed. Regenerate a
// deliberate change with UPDATE_GOLDEN=1 to review it as a diff. Width is fixed so
// wrapping is deterministic; color is off so there are no ANSI escapes.
func TestTerminal_golden(t *testing.T) {
	var grouped bytes.Buffer
	if err := Terminal(&grouped, sampleContext(), Options{Color: false, Width: 100}); err != nil {
		t.Fatal(err)
	}
	golden(t, "terminal_grouped.txt", grouped.Bytes())

	var full bytes.Buffer
	if err := Terminal(&full, sampleContext(), Options{Color: false, Width: 100, Full: true}); err != nil {
		t.Fatal(err)
	}
	golden(t, "terminal_full.txt", full.Bytes())
}
