package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestPartitionSeqScanHeavy(t *testing.T) {
	// Heavy aggregate seq scans on a big partitioned table → fire.
	c := &model.Context{Tables: &model.Tables{Partitioned: []model.PartitionRollup{
		{Schema: "public", Name: "events", Partitions: 200, TotalBytes: 50 << 30,
			LiveTuples: 100_000_000, SeqScans: 5000, IndexScans: 100},
	}}}
	if has(Compute(c), "partition_seq_scan_heavy") == nil {
		t.Error("heavy partitioned seq scans should fire")
	}
	// Index-dominated access → healthy, no fire.
	healthy := &model.Context{Tables: &model.Tables{Partitioned: []model.PartitionRollup{
		{Schema: "public", Name: "events", Partitions: 200, TotalBytes: 50 << 30,
			LiveTuples: 100_000_000, SeqScans: 100, IndexScans: 1_000_000},
	}}}
	if has(Compute(healthy), "partition_seq_scan_heavy") != nil {
		t.Error("index-dominated partitioned table must not fire")
	}
}
