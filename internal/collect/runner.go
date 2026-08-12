package collect

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
	"golang.org/x/sync/errgroup"
)

// Options tune a collection run.
type Options struct {
	Interval time.Duration // gap between the two counter samples (default 1s, min 500ms)
	Deadline time.Duration // hard cap on total wall time (default 5s + interval)
}

func (o Options) interval() time.Duration {
	if o.Interval < 500*time.Millisecond {
		return time.Second
	}
	return o.Interval
}

// gauges + counter sample A read in phase 1; counter sample B in phase 2.
type samples struct {
	mu sync.Mutex

	healthA, healthB *healthSample
	walA, walB       *ioPair[walSample]
	ioA, ioB         *ioPair[ioSample]

	activity  []activityRow
	locks     []lockRow
	queries   []queryRow
	tables    []tableRow
	dbSize    int64
	indexes   []indexRow
	settings  []settingRow
	repl      []replRow
	isReplica bool
	recvLag   *float64

	errs map[string]error
}

// ioPair lets walB/ioB be nil-checked uniformly with the counter samples.
type ioPair[T any] struct{ v T }

func (s *samples) fail(name string, err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.errs == nil {
		s.errs = map[string]error{}
	}
	s.errs[name] = err
	s.mu.Unlock()
}

// Run performs the two-phase collection and assembles a Context. A single
// failing collector marks its section unavailable rather than failing the run.
func Run(ctx context.Context, t *conn.Target, opts Options) (*model.Context, error) {
	iv := opts.interval()
	deadline := opts.Deadline
	if deadline <= 0 {
		deadline = 5*time.Second + iv
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	caps := t.Caps
	s := &samples{}

	// Phase 1: counter sample A + all gauges, concurrently (bounded to the pool).
	tA := nowUTC()
	g1, gctx := errgroup.WithContext(ctx)
	g1.SetLimit(4)
	g1.Go(func() error {
		h, err := queryOne[healthSample](gctx, t, sqlHealth)
		set(s, &s.healthA, h, err)
		s.fail("health", err)
		return nil
	})
	if caps.HasStatWAL() {
		g1.Go(func() error {
			w, err := queryOne[walSample](gctx, t, sqlWAL)
			setPair(s, &s.walA, w, err)
			s.fail("wal", err)
			return nil
		})
	}
	g1.Go(func() error {
		io, err := queryOne[ioSample](gctx, t, ioSQL(caps))
		setPair(s, &s.ioA, io, err)
		s.fail("io", err)
		return nil
	})
	g1.Go(func() error {
		r, err := queryMany[activityRow](gctx, t, sqlActivity)
		s.mu.Lock()
		s.activity = r
		s.mu.Unlock()
		s.fail("activity", err)
		return nil
	})
	g1.Go(func() error {
		r, err := queryMany[lockRow](gctx, t, sqlLocks)
		s.mu.Lock()
		s.locks = r
		s.mu.Unlock()
		s.fail("locks", err)
		return nil
	})
	if caps.HasStatStatements {
		g1.Go(func() error {
			r, err := queryMany[queryRow](gctx, t, fmt.Sprintf(sqlQueries, caps.StatStatementsTotalCol()))
			s.mu.Lock()
			s.queries = r
			s.mu.Unlock()
			s.fail("queries", err)
			return nil
		})
	}
	g1.Go(func() error {
		r, err := queryMany[tableRow](gctx, t, sqlTables)
		s.mu.Lock()
		s.tables = r
		s.mu.Unlock()
		s.fail("tables", err)
		return nil
	})
	g1.Go(func() error {
		n, err := scalar[int64](gctx, t, `SELECT pg_database_size(current_database())`)
		s.mu.Lock()
		s.dbSize = n
		s.mu.Unlock()
		s.fail("dbsize", err)
		return nil
	})
	g1.Go(func() error {
		r, err := queryMany[indexRow](gctx, t, sqlIndexes)
		s.mu.Lock()
		s.indexes = r
		s.mu.Unlock()
		s.fail("indexes", err)
		return nil
	})
	g1.Go(func() error {
		r, err := queryMany[settingRow](gctx, t, sqlSettings)
		s.mu.Lock()
		s.settings = r
		s.mu.Unlock()
		s.fail("settings", err)
		return nil
	})
	g1.Go(func() error { collectReplication(gctx, t, s); return nil })
	_ = g1.Wait()

	// Wait out the sample interval, then re-read the counters.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(iv):
	}
	tB := nowUTC()
	dt := tB.Sub(tA)

	g2, gctx2 := errgroup.WithContext(ctx)
	g2.SetLimit(4)
	g2.Go(func() error {
		h, err := queryOne[healthSample](gctx2, t, sqlHealth)
		set(s, &s.healthB, h, err)
		s.fail("health", err)
		return nil
	})
	if caps.HasStatWAL() {
		g2.Go(func() error {
			w, err := queryOne[walSample](gctx2, t, sqlWAL)
			setPair(s, &s.walB, w, err)
			s.fail("wal", err)
			return nil
		})
	}
	g2.Go(func() error {
		io, err := queryOne[ioSample](gctx2, t, ioSQL(caps))
		setPair(s, &s.ioB, io, err)
		s.fail("io", err)
		return nil
	})
	_ = g2.Wait()

	return assemble(caps, s, tA, tB, dt), nil
}

func ioSQL(caps conn.Capabilities) string {
	if caps.HasStatCheckpointer() {
		return sqlIOCheckpointer
	}
	return sqlIOBgwriter
}

func collectReplication(ctx context.Context, t *conn.Target, s *samples) {
	isReplica, err := scalar[bool](ctx, t, `SELECT pg_is_in_recovery()`)
	if err != nil {
		s.fail("replication", err)
		return
	}
	s.mu.Lock()
	s.isReplica = isReplica
	s.mu.Unlock()
	if isReplica {
		lag, _ := scalar[*float64](ctx, t, `SELECT extract(epoch FROM now() - pg_last_xact_replay_timestamp())`)
		s.mu.Lock()
		s.recvLag = lag
		s.mu.Unlock()
		return
	}
	r, err := queryMany[replRow](ctx, t, sqlReplication)
	s.mu.Lock()
	s.repl = r
	s.mu.Unlock()
	s.fail("replication", err)
}

// small typed setters to keep the goroutine bodies terse and lock-safe.
func set(s *samples, dst **healthSample, v healthSample, err error) {
	if err != nil {
		return
	}
	s.mu.Lock()
	*dst = &v
	s.mu.Unlock()
}

func setPair[T any](s *samples, dst **ioPair[T], v T, err error) {
	if err != nil {
		return
	}
	s.mu.Lock()
	*dst = &ioPair[T]{v: v}
	s.mu.Unlock()
}
