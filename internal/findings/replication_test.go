package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestReplicationSlotRisk(t *testing.T) {
	const gib = 1 << 30
	cases := []struct {
		name    string
		slot    model.ReplicationSlot
		wantID  bool
		wantSev string
	}{
		{"active slot is fine",
			model.ReplicationSlot{Name: "s1", Type: "physical", Active: true, RetainedBytes: 20 * gib}, false, ""},
		{"inactive but tiny is ignored",
			model.ReplicationSlot{Name: "s2", Type: "logical", Active: false, RetainedBytes: 4 << 20}, false, ""},
		{"inactive over warn floor",
			model.ReplicationSlot{Name: "s3", Type: "physical", Active: false, RetainedBytes: 1 * gib}, true, model.SeverityWarn},
		{"inactive over crit floor",
			model.ReplicationSlot{Name: "s4", Type: "logical", Active: false, RetainedBytes: 20 * gib}, true, model.SeverityCritical},
		{"wal_status=lost is critical even when active",
			model.ReplicationSlot{Name: "s5", Type: "physical", Active: true, WALStatus: "lost", RetainedBytes: 0}, true, model.SeverityCritical},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := &model.Context{Replication: &model.Replication{Slots: []model.ReplicationSlot{c.slot}}}
			f := has(Compute(ctx), "replication_slot_inactive")
			if c.wantID && f == nil {
				t.Fatalf("expected replication_slot_inactive to fire")
			}
			if !c.wantID && f != nil {
				t.Fatalf("expected no finding, got %+v", f)
			}
			if c.wantID && f.Severity != c.wantSev {
				t.Fatalf("severity = %q, want %q", f.Severity, c.wantSev)
			}
			if c.wantID && f.Impact.Dimension != model.DimRisk {
				t.Fatalf("slot risk should be a DimRisk finding, got %q", f.Impact.Dimension)
			}
		})
	}
}

func TestSubscriptionDown(t *testing.T) {
	up := &model.Context{Replication: &model.Replication{Subscriptions: []model.Subscription{
		{Name: "sub_ok", WorkerRunning: true},
	}}}
	if has(Compute(up), "subscription_worker_down") != nil {
		t.Fatalf("a running subscription should not fire")
	}
	down := &model.Context{Replication: &model.Replication{Subscriptions: []model.Subscription{
		{Name: "sub_stalled", WorkerRunning: false},
	}}}
	f := has(Compute(down), "subscription_worker_down")
	if f == nil {
		t.Fatalf("a stalled subscription should fire")
	}
	if f.Severity != model.SeverityWarn {
		t.Fatalf("severity = %q, want warn", f.Severity)
	}
}
