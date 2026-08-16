package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func TestRequiredSyncStandbys(t *testing.T) {
	cases := map[string]int{"": 0, "s1": 1, "s1, s2": 1, "2 (s1,s2,s3)": 2, "ANY 2 (a,b,c)": 2, "FIRST 1 (a, b)": 1, "ANY 3(x,y,z,w)": 3}
	for in, want := range cases {
		if got := requiredSyncStandbys(in); got != want {
			t.Errorf("requiredSyncStandbys(%q)=%d want %d", in, got, want)
		}
	}
}

func TestSyncRepDegraded(t *testing.T) {
	degraded := &model.Context{
		Settings:    &model.Settings{Params: map[string]string{"synchronous_standby_names": "ANY 2 (s1,s2,s3)", "synchronous_commit": "on"}},
		Replication: &model.Replication{Replicas: []model.ReplicaRow{{AppName: "s1", SyncState: "quorum"}, {AppName: "s2", SyncState: "potential"}}},
	}
	if f := has(Compute(degraded), "sync_rep_degraded"); f == nil || f.Severity != model.SeverityCritical {
		t.Fatalf("1 of 2 required sync standbys must be critical, got %+v", f)
	}
	ok := &model.Context{
		Settings:    &model.Settings{Params: map[string]string{"synchronous_standby_names": "ANY 1 (s1,s2)", "synchronous_commit": "on"}},
		Replication: &model.Replication{Replicas: []model.ReplicaRow{{AppName: "s1", SyncState: "quorum"}}},
	}
	if has(Compute(ok), "sync_rep_degraded") != nil {
		t.Error("enough sync standbys must not fire")
	}
	local := &model.Context{Settings: &model.Settings{Params: map[string]string{"synchronous_standby_names": "ANY 2 (s1,s2)", "synchronous_commit": "local"}}}
	if has(Compute(local), "sync_rep_degraded") != nil {
		t.Error("synchronous_commit=local must not fire (sync rep not enforced)")
	}
}

func TestReplicaLagTime_gatedOnWAL(t *testing.T) {
	lag, flowing, idle := 120.0, 1000.0, 0.0
	fire := &model.Context{WAL: &model.WAL{BytesPerSec: &flowing}, Replication: &model.Replication{Replicas: []model.ReplicaRow{{AppName: "s1", ReplayLagSec: &lag}}}}
	if has(Compute(fire), "replica_lag_time") == nil {
		t.Error("lag over threshold with WAL flowing should fire")
	}
	stale := &model.Context{WAL: &model.WAL{BytesPerSec: &idle}, Replication: &model.Replication{Replicas: []model.ReplicaRow{{AppName: "s1", ReplayLagSec: &lag}}}}
	if has(Compute(stale), "replica_lag_time") != nil {
		t.Error("no WAL flow must NOT report lag (stale interval)")
	}
}
