package collect

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
)

//go:embed sql/replication.sql
var sqlReplication string

// replication = standbys connected (on a primary) or replay lag (on a replica),
// plus replication-slot health (an inactive slot pins WAL and can fill the disk)
// and logical-subscription health (a stalled apply worker means data drift).
type replicationCollector struct{}

type replRow struct {
	ClientAddr string `db:"client_addr"`
	State      string `db:"state"`
	SyncState  string `db:"sync_state"`
	WriteLagB  int64  `db:"write_lag_bytes"`
	FlushLagB  int64  `db:"flush_lag_bytes"`
	ReplayLagB int64  `db:"replay_lag_bytes"`
}

type slotRow struct {
	Name          string `db:"slot_name"`
	Type          string `db:"slot_type"`
	Active        bool   `db:"active"`
	Database      string `db:"database"`
	RetainedBytes int64  `db:"retained_bytes"`
	WALStatus     string `db:"wal_status"`
}

type subRow struct {
	Name          string  `db:"subname"`
	WorkerRunning bool    `db:"worker_running"`
	LastMsgAgeSec float64 `db:"last_msg_age_sec"`
}

type replSample struct {
	IsReplica bool
	Repl      []replRow
	RecvLag   *float64
	Slots     []slotRow
	Subs      []subRow
}

func (replicationCollector) Name() string                     { return "replication" }
func (replicationCollector) Kind() Kind                       { return KindGauge }
func (replicationCollector) Available(conn.Capabilities) bool { return true }

func (replicationCollector) Sample(ctx context.Context, t *conn.Target, caps conn.Capabilities) (any, error) {
	isReplica, err := scalar[bool](ctx, t, `SELECT pg_is_in_recovery()`)
	if err != nil {
		return nil, err
	}
	out := replSample{IsReplica: isReplica}
	if isReplica {
		out.RecvLag, _ = scalar[*float64](ctx, t, `SELECT extract(epoch FROM now() - pg_last_xact_replay_timestamp())`)
	} else {
		rows, err := queryMany[replRow](ctx, t, sqlReplication)
		if err != nil {
			return nil, err
		}
		out.Repl = rows
	}

	// Slots + subscriptions are best-effort: they need pg_monitor / pg_read_all_stats,
	// aren't present everywhere, and a permission error here must not sink the whole
	// replication section. wal_status is PG13+, so gate that column on the version.
	walStatus := "''::text"
	if caps.VersionNum >= 130000 {
		walStatus = "coalesce(wal_status::text, '')"
	}
	slotSQL := fmt.Sprintf(`
		SELECT slot_name,
		       coalesce(slot_type, '')                                        AS slot_type,
		       active,
		       coalesce(database, '')                                         AS database,
		       coalesce(pg_wal_lsn_diff(
		           CASE WHEN pg_is_in_recovery()
		                THEN pg_last_wal_receive_lsn()
		                ELSE pg_current_wal_lsn() END,
		           restart_lsn), 0)::bigint                                    AS retained_bytes,
		       %s                                                             AS wal_status
		FROM pg_replication_slots`, walStatus)
	if slots, err := queryMany[slotRow](ctx, t, slotSQL); err == nil {
		out.Slots = slots
	}

	if subs, err := queryMany[subRow](ctx, t, `
		SELECT subname,
		       (pid IS NOT NULL)                                                     AS worker_running,
		       coalesce(extract(epoch FROM now() - last_msg_receipt_time), 0)::float8 AS last_msg_age_sec
		FROM pg_stat_subscription`); err == nil {
		out.Subs = subs
	}
	return out, nil
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
	for _, row := range rs.Slots {
		r.Slots = append(r.Slots, model.ReplicationSlot{
			Name: row.Name, Type: row.Type, Active: row.Active, Database: row.Database,
			RetainedBytes: row.RetainedBytes, WALStatus: row.WALStatus,
		})
	}
	for _, row := range rs.Subs {
		r.Subscriptions = append(r.Subscriptions, model.Subscription{
			Name: row.Name, WorkerRunning: row.WorkerRunning, LastMsgAgeSec: round2(row.LastMsgAgeSec),
		})
	}
	c.Replication = r
}
