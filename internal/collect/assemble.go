package collect

import (
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
	"github.com/pgrundev/pgbot/internal/rate"
)

func nowUTC() time.Time { return time.Now().UTC() }

// unavailable builds a Section marked unavailable, using the collector's
// recorded error (if any) as the human-readable reason.
func (s *samples) unavailable(name, fallback string) model.Section {
	reason := fallback
	s.mu.Lock()
	if s.errs != nil {
		if e := s.errs[name]; e != nil {
			reason = conn.RedactConnString(e.Error())
		}
	}
	s.mu.Unlock()
	return model.Section{Exactness: model.ExactnessUnavailable, Reason: reason}
}

func assemble(caps conn.Capabilities, s *samples, _ time.Time, tB time.Time, dt time.Duration) *model.Context {
	ctx := &model.Context{
		SchemaVersion: model.SchemaVersion,
		CollectedAt:   tB,
		Server: model.ServerInfo{
			VersionNum:   caps.VersionNum,
			VersionText:  caps.VersionText,
			Database:     caps.Database,
			Extensions:   caps.Extensions,
			Capabilities: caps.Satisfied(),
			HasPgMonitor: caps.HasPgMonitor,
		},
		Window:   model.Window{SampleSeconds: round2(dt.Seconds())},
		Findings: []model.Finding{},
	}

	assembleHealth(ctx, s, tB, dt)
	assembleActivity(ctx, s)
	assembleLocks(ctx, s)
	assembleQueries(ctx, caps, s)
	assembleTables(ctx, s)
	assembleIndexes(ctx, s)
	assembleWAL(ctx, caps, s, dt)
	assembleIO(ctx, s, dt)
	assembleReplication(ctx, s)
	assembleSettings(ctx, s)
	return ctx
}

func assembleHealth(ctx *model.Context, s *samples, now time.Time, dt time.Duration) {
	a, b := s.healthA, s.healthB
	if a == nil || b == nil {
		ctx.Health = &model.Health{Section: s.unavailable("health", "pg_stat_database unavailable")}
		return
	}
	h := &model.Health{Connections: int(b.Numbackends)}
	reset := false
	mark := func(v *float64, ok bool) *float64 {
		if !ok {
			reset = true
			return nil
		}
		return v
	}
	tps, ok := rate.PerSecond(a.XactCommit+a.XactRollback, b.XactCommit+b.XactRollback, dt)
	h.TPS = mark(tps, ok)
	cps, ok := rate.PerSecond(a.XactCommit, b.XactCommit, dt)
	h.CommitsPerSec = mark(cps, ok)
	rps, ok := rate.PerSecond(a.XactRollback, b.XactRollback, dt)
	h.RollbacksPerSec = mark(rps, ok)
	if rr, ok := rate.Ratio(a.XactRollback, b.XactRollback, a.XactCommit, b.XactCommit); ok {
		h.RollbackRatio = round4p(rr)
	}
	if chr, ok := rate.Ratio(a.BlksHit, b.BlksHit, a.BlksRead, b.BlksRead); ok {
		h.CacheHitRatio = round4p(chr)
	}
	if d, ok := rate.PerSecond(a.Deadlocks, b.Deadlocks, dt); ok {
		h.DeadlocksPerMin = rate.Ptr(round2(*d * 60))
	}
	if tb, ok := rate.PerSecond(a.TempBytes, b.TempBytes, dt); ok {
		h.TempBytesPerSec = round2p(tb)
	}
	if tr, ok := rate.PerSecond(a.TupReturned, b.TupReturned, dt); ok {
		h.TupReturnedPerS = round2p(tr)
	}
	if tw, ok := rate.PerSecond(a.TupInserted+a.TupUpdated+a.TupDeleted, b.TupInserted+b.TupUpdated+b.TupDeleted, dt); ok {
		h.TupWrittenPerS = round2p(tw)
	}
	if reset {
		h.Section = model.Section{Exactness: model.ExactnessReset, Reason: "a counter reset between samples; rates omitted"}
	} else {
		h.Section = model.Section{Exactness: model.ExactnessSampled}
	}
	if b.StatsReset != nil {
		ctx.Window.StatsResetAt = b.StatsReset
		days := now.Sub(*b.StatsReset).Hours() / 24
		ctx.Window.StatsWindowDays = round2p(rate.Ptr(days))
	}
	ctx.Health = h
}

func assembleActivity(ctx *model.Context, s *samples) {
	if _, unavail := s.errs["activity"]; unavail && s.activity == nil {
		ctx.Activity = &model.Activity{Section: s.unavailable("activity", "pg_stat_activity unavailable")}
		return
	}
	act := &model.Activity{ByState: map[string]int{}, WaitEvents: map[string]int{}, Section: model.Section{Exactness: model.ExactnessScraped}}
	for _, r := range s.activity {
		act.Total += r.N
		act.ByState[r.State] += r.N
		switch r.State {
		case "active":
			act.Active += r.N
		case "idle":
			act.Idle += r.N
		case "idle in transaction", "idle in transaction (aborted)":
			act.IdleInTransaction += r.N
		}
		if r.WaitEventType != "" {
			act.Waiting += r.N
			act.WaitEvents[r.WaitEventType] += r.N
		}
		if r.MaxXactAgeS > act.LongestXactSec {
			act.LongestXactSec = round2(r.MaxXactAgeS)
		}
		if r.MaxActiveAgeS > act.LongestActiveSec {
			act.LongestActiveSec = round2(r.MaxActiveAgeS)
		}
	}
	if len(act.WaitEvents) == 0 {
		act.WaitEvents = nil
	}
	ctx.Activity = act
}

func assembleLocks(ctx *model.Context, s *samples) {
	if _, unavail := s.errs["locks"]; unavail && s.locks == nil {
		ctx.Locks = &model.Locks{Section: s.unavailable("locks", "pg_locks unavailable")}
		return
	}
	l := &model.Locks{Section: model.Section{Exactness: model.ExactnessScraped}, BlockedCount: len(s.locks)}
	for _, r := range s.locks {
		pids := make([]int64, len(r.BlockingPIDs))
		for i, p := range r.BlockingPIDs {
			pids[i] = int64(p)
		}
		l.Chains = append(l.Chains, model.BlockingRow{
			BlockedPID:   int(r.BlockedPID),
			BlockingPIDs: pids,
			WaitEvent:    r.WaitEventType,
			WaitSeconds:  round2(r.BlockedWaitS),
			BlockedQuery: conn.ScrubQueryText(r.BlockedQuery), // RAW SQL — must scrub
		})
	}
	ctx.Locks = l
}

func assembleQueries(ctx *model.Context, caps conn.Capabilities, s *samples) {
	if !caps.HasStatStatements {
		ctx.Queries = &model.Queries{Enabled: false, Section: model.Section{
			Exactness: model.ExactnessUnavailable,
			Reason:    "pg_stat_statements not installed — run CREATE EXTENSION pg_stat_statements;",
		}}
		return
	}
	if _, unavail := s.errs["queries"]; unavail && s.queries == nil {
		ctx.Queries = &model.Queries{Enabled: true, Section: s.unavailable("queries", "pg_stat_statements read failed")}
		return
	}
	q := &model.Queries{Enabled: true, Section: model.Section{Exactness: model.ExactnessCumulative}}
	for _, r := range s.queries {
		item := model.QueryStat{
			QueryID: r.QueryID, Query: r.Query, Calls: r.Calls,
			TotalMS: round2(r.TotalMS), MeanMS: round4(r.MeanMS), MaxMS: round2(r.MaxMS),
			Rows: r.Rows, WALBytes: r.WalBytes,
		}
		if tot := r.SharedBlksHit + r.SharedBlksRead; tot > 0 {
			item.CacheHit = round4p(rate.Ptr(float64(r.SharedBlksHit) / float64(tot)))
		}
		q.Top = append(q.Top, item)
	}
	ctx.Queries = q
}

func assembleTables(ctx *model.Context, s *samples) {
	if _, unavail := s.errs["tables"]; unavail && s.tables == nil {
		ctx.Tables = &model.Tables{Section: s.unavailable("tables", "pg_stat_user_tables unavailable")}
		return
	}
	t := &model.Tables{Section: model.Section{Exactness: model.ExactnessScraped}, DBSizeBytes: s.dbSize}
	for _, r := range s.tables {
		dead := 0.0
		if tot := r.LiveTuples + r.DeadTuples; tot > 0 {
			dead = float64(r.DeadTuples) / float64(tot)
		}
		t.Top = append(t.Top, model.TableStat{
			Schema: r.Schema, Name: r.Table, TotalBytes: r.TotalBytes,
			LiveTuples: r.LiveTuples, DeadTuples: r.DeadTuples, DeadRatio: round4(dead),
			SeqScans: r.SeqScans, IndexScans: r.IndexScans, ModsSinceAnalyze: r.ModsSinceAnalyze,
			LastVacuum: r.LastVacuum, LastAutovac: r.LastAutovacuum,
		})
	}
	ctx.Tables = t
}

func assembleIndexes(ctx *model.Context, s *samples) {
	if _, unavail := s.errs["indexes"]; unavail && s.indexes == nil {
		ctx.Indexes = &model.Indexes{Section: s.unavailable("indexes", "pg_stat_user_indexes unavailable")}
		return
	}
	idx := &model.Indexes{Section: model.Section{Exactness: model.ExactnessScraped}, Total: len(s.indexes)}
	for _, r := range s.indexes {
		st := model.IndexStat{Schema: r.Schema, Table: r.Table, Name: r.Index, Scans: r.Scans, Bytes: r.Bytes, Definition: r.Definition}
		// Unused = zero-scan, non-trivial, and NOT backing a primary key or
		// unique constraint (those enforce integrity and can't just be dropped).
		if r.Scans == 0 && r.Bytes > 16384 && !r.IsPrimary && !r.IsUnique && len(idx.Unused) < 50 {
			idx.Unused = append(idx.Unused, st)
		}
		if len(idx.Largest) < 10 {
			idx.Largest = append(idx.Largest, st) // rows arrive largest-first
		}
	}
	ctx.Indexes = idx
}

func assembleWAL(ctx *model.Context, caps conn.Capabilities, s *samples, dt time.Duration) {
	if !caps.HasStatWAL() {
		ctx.WAL = &model.WAL{Section: model.Section{Exactness: model.ExactnessUnavailable, Reason: "pg_stat_wal requires PostgreSQL 14+"}}
		return
	}
	if s.walA == nil || s.walB == nil {
		ctx.WAL = &model.WAL{Section: s.unavailable("wal", "pg_stat_wal unavailable")}
		return
	}
	a, b := s.walA.v, s.walB.v
	w := &model.WAL{Section: model.Section{Exactness: model.ExactnessSampled}, BuffersFull: b.WalBuffersFull}
	if bp, ok := rate.PerSecond(a.WalBytes, b.WalBytes, dt); ok {
		w.BytesPerSec = round2p(bp)
	} else {
		w.Section = model.Section{Exactness: model.ExactnessReset, Reason: "wal counter reset between samples"}
	}
	if rp, ok := rate.PerSecond(a.WalRecords, b.WalRecords, dt); ok {
		w.RecordsPerSec = round2p(rp)
	}
	ctx.WAL = w
}

func assembleIO(ctx *model.Context, s *samples, dt time.Duration) {
	if s.ioA == nil || s.ioB == nil {
		ctx.IO = &model.IO{Section: s.unavailable("io", "IO stats unavailable")}
		return
	}
	a, b := s.ioA.v, s.ioB.v
	io := &model.IO{
		Section:          model.Section{Exactness: model.ExactnessSampled},
		CheckpointsTimed: b.CheckpointsTimed, CheckpointsReq: b.CheckpointsReq, BackendFsyncs: b.BackendFsyncs,
	}
	if bw, ok := rate.PerSecond(a.BuffersWritten, b.BuffersWritten, dt); ok {
		io.BuffersWrittenPerS = round2p(bw)
	}
	ctx.IO = io
}

func assembleReplication(ctx *model.Context, s *samples) {
	if _, unavail := s.errs["replication"]; unavail && !s.isReplica && s.repl == nil {
		ctx.Replication = &model.Replication{Section: s.unavailable("replication", "replication stats unavailable")}
		return
	}
	r := &model.Replication{Section: model.Section{Exactness: model.ExactnessScraped}, IsReplica: s.isReplica, ReceiverLagSec: round2p(s.recvLag)}
	for _, row := range s.repl {
		r.Replicas = append(r.Replicas, model.ReplicaRow{
			ClientAddr: row.ClientAddr, State: row.State, SyncState: row.SyncState,
			WriteLagB: row.WriteLagB, FlushLagB: row.FlushLagB, ReplayLagB: row.ReplayLagB,
		})
	}
	ctx.Replication = r
}

func assembleSettings(ctx *model.Context, s *samples) {
	if _, unavail := s.errs["settings"]; unavail && s.settings == nil {
		ctx.Settings = &model.Settings{Section: s.unavailable("settings", "pg_settings unavailable")}
		return
	}
	set := &model.Settings{Section: model.Section{Exactness: model.ExactnessScraped}, Overrides: map[string]string{}}
	for _, r := range s.settings {
		set.Overrides[r.Name] = r.Setting
	}
	ctx.Settings = set
}
