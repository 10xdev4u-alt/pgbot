package advisor

import (
	"bytes"
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// SchemaVersion is the semver of the `pgbot advise --json` contract.
const SchemaVersion = "1.0.0"

// Report is the shape of `pgbot advise --json` (and the MCP suggest_indexes
// tool). Kept as a type so the JSON Schema is generated, not hand-written.
type Report struct {
	Database        string           `json:"database"`
	Available       bool             `json:"available"`
	Reason          string           `json:"reason,omitempty"` // why the advisor can't run
	Recommendations []Recommendation `json:"recommendations"`
	Considered      int              `json:"considered"`    // slow queries examined
	Planned         int              `json:"planned"`       // successfully planned
	Candidates      int              `json:"candidates"`    // candidate indexes tested
	SkippedStale    int              `json:"skipped_stale"` // candidates skipped: stats too stale
	Note            string           `json:"note,omitempty"`
}

// ReportSchema returns the JSON Schema for the advise contract.
func ReportSchema() []byte {
	r := &jsonschema.Reflector{DoNotReference: false}
	s := r.Reflect(&Report{})
	s.ID = jsonschema.ID("https://pgbot.dev/schema/pgbot-advise-" + SchemaVersion + ".json")
	s.Description = "pgbot advise --json — planner-validated index recommendations (hypopg). Nothing is built."
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(bytes.TrimRight(b, "\n"), '\n')
}
