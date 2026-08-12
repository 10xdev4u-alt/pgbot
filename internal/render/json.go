package render

import (
	"encoding/json"
	"io"

	"github.com/pgrundev/pgbot/internal/model"
)

// JSON writes the Context as the stable, versioned, PII-free agent/script
// contract. Struct field order gives deterministic key ordering; no ANSI.
func JSON(w io.Writer, c *model.Context) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(c)
}
