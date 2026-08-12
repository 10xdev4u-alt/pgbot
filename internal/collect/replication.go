package collect

import (
	"context"
	_ "embed"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/replication.sql
var sqlReplication string

// replication = standbys connected (on a primary) or replay lag (on a replica).
type replicationCollector struct{}

type replRow struct {
	ClientAddr string `db:"client_addr"`
	State      string `db:"state"`
	SyncState  string `db:"sync_state"`
	WriteLagB  int64  `db:"write_lag_bytes"`
	FlushLagB  int64  `db:"flush_lag_bytes"`
	ReplayLagB int64  `db:"replay_lag_bytes"`
}

type replSample struct {
	IsReplica bool
	Repl      []replRow
	RecvLag   *float64
}

func (replicationCollector) Name() string                     { return "replication" }
func (replicationCollector) Kind() Kind                       { return KindGauge }
func (replicationCollector) Available(conn.Capabilities) bool { return true }

func (replicationCollector) Sample(ctx context.Context, t *conn.Target, _ conn.Capabilities) (any, error) {
	isReplica, err := scalar[bool](ctx, t, `SELECT pg_is_in_recovery()`)
	if err != nil {
		return nil, err
	}
	if isReplica {
		lag, _ := scalar[*float64](ctx, t, `SELECT extract(epoch FROM now() - pg_last_xact_replay_timestamp())`)
		return replSample{IsReplica: true, RecvLag: lag}, nil
	}
	rows, err := queryMany[replRow](ctx, t, sqlReplication)
	if err != nil {
		return nil, err
	}
	return replSample{Repl: rows}, nil
}

func (replicationCollector) Assemble(c *model.Context, _ conn.Capabilities, s sampled, _ time.Duration, _ Options) {
	rs, ok := s.A.(replSample)
	if s.Err != nil || !ok {
		c.Replication = &model.Replication{Section: unavail(s.Err, "replication stats unavailable")}
		return
	}
	r := &model.Replication{Section: model.Section{Exactness: model.ExactnessScraped}, IsReplica: rs.IsReplica, ReceiverLagSec: round2p(rs.RecvLag)}
	for _, row := range rs.Repl {
		r.Replicas = append(r.Replicas, model.ReplicaRow{
			ClientAddr: row.ClientAddr, State: row.State, SyncState: row.SyncState,
			WriteLagB: row.WriteLagB, FlushLagB: row.FlushLagB, ReplayLagB: row.ReplayLagB,
		})
	}
	c.Replication = r
}
