package findings

import (
	"strings"
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestChecksumFindings(t *testing.T) {
	ts := time.Now()
	corrupt := &model.Context{Checksums: &model.Checksums{Failures: []model.ChecksumFailure{
		{Database: "app_prod", Count: 3, LastFailure: &ts},
	}}}
	f := has(Compute(corrupt), "checksum_failures")
	if f == nil || f.Severity != model.SeverityCritical {
		t.Fatalf("expected critical checksum_failures, got %+v", f)
	}
	if len(f.Caveats) == 0 || !strings.Contains(f.Caveats[0], "VACUUM FULL") {
		t.Errorf("must carry the don't-rewrite caveat: %v", f.Caveats)
	}
	if has(Compute(&model.Context{Settings: &model.Settings{Params: map[string]string{"ignore_checksum_failure": "on"}}}), "ignore_checksum_failure_on") == nil {
		t.Error("ignore_checksum_failure=on must fire critical")
	}
	if f := has(Compute(&model.Context{Settings: &model.Settings{Params: map[string]string{"data_checksums": "off"}}}), "checksums_disabled"); f == nil || f.Severity != model.SeverityInfo {
		t.Errorf("data_checksums=off must be low-key info, got %+v", f)
	}
	healthy := &model.Context{Checksums: &model.Checksums{}, Settings: &model.Settings{Params: map[string]string{"data_checksums": "on", "ignore_checksum_failure": "off"}}}
	for _, id := range []string{"checksum_failures", "ignore_checksum_failure_on", "checksums_disabled"} {
		if has(Compute(healthy), id) != nil {
			t.Errorf("healthy checksums must not fire %s", id)
		}
	}
}
