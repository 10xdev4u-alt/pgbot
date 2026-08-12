package model

import (
	"encoding/json"
	"testing"
	"time"
)

// v100Consumer models what a Context JSON consumer written against the v1.0.0
// contract reads: the top-level sections and the finding fields agents actually
// branch on. Go's json ignores unknown keys, so the 1.1.0 additions (events,
// wait_profile, finding confidence/caveats/remediation) fall away harmlessly.
type v100Consumer struct {
	SchemaVersion string `json:"schema_version"`
	Server        struct {
		Database   string `json:"database"`
		VersionNum int    `json:"version_num"`
	} `json:"server"`
	Findings []struct {
		ID       string   `json:"id"`
		Severity string   `json:"severity"`
		Title    string   `json:"title"`
		Detail   string   `json:"detail"`
		Evidence []string `json:"evidence"`
	} `json:"findings"`
}

// TestV100ConsumerParsesV110 asserts a v1.0.0-shaped consumer still parses the
// current (1.1.0) output: additive-only means the fields it depends on are intact
// and the new keys are simply ignored.
func TestV100ConsumerParsesV110(t *testing.T) {
	// A realistic 1.1.0 Context carrying every additive surface.
	reset := time.Now().UTC()
	age := int64(4 * 3600)
	c := &Context{
		SchemaVersion: SchemaVersion,
		CollectedAt:   reset,
		Server:        ServerInfo{Database: "production", VersionNum: 170002},
		Window:        Window{WindowAgeSeconds: &age, StatsResetAt: &reset},
		Events:        []Event{{Kind: "config.changed", Object: "work_mem", Confidence: 0.5}},
		WaitProfile:   &WaitProfile{Available: true, Samples: 50, Buckets: []WaitBucket{{Type: "Lock", Share: 0.6}}},
		Findings: []Finding{{
			ID: "unused_indexes", Severity: SeverityWarn, Title: "3 unused indexes", Detail: "…",
			Evidence: []string{"public.a.idx"}, Remediation: "drop it",
			Impact:     Impact{Score: 80, Dimension: DimStorage, Estimate: "≈43 GB"},
			Confidence: 0.4, Caveats: []string{"replication active"},
		}},
	}

	blob, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got v100Consumer
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("a v1.0.0 consumer must parse v1.1.0 output, got: %v", err)
	}
	if got.SchemaVersion != "1.1.0" {
		t.Errorf("schema_version = %q, want 1.1.0", got.SchemaVersion)
	}
	if got.Server.Database != "production" || got.Server.VersionNum != 170002 {
		t.Errorf("server fields did not survive: %+v", got.Server)
	}
	if len(got.Findings) != 1 || got.Findings[0].ID != "unused_indexes" ||
		got.Findings[0].Severity != SeverityWarn || got.Findings[0].Evidence[0] != "public.a.idx" {
		t.Errorf("finding core fields did not survive: %+v", got.Findings)
	}
}
