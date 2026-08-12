package collect

import (
	"context"
	"sync"
	"time"

	"github.com/pgrundev/pgbot/internal/conn"
	"github.com/pgrundev/pgbot/internal/model"
	"golang.org/x/sync/errgroup"
)

// Run performs the two-phase collection and assembles a Context. Gauges and the
// first counter sample are read in phase 1; the second counter sample after the
// interval in phase 2. A single failing collector marks its section unavailable
// rather than failing the run.
func Run(ctx context.Context, t *conn.Target, opts Options) (*model.Context, error) {
	iv := opts.interval()
	deadline := opts.Deadline
	if deadline <= 0 {
		deadline = 5*time.Second + iv
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	caps := t.Caps
	var mu sync.Mutex
	results := make(map[string]*sampled, len(registry))

	// Phase 1: sample A for every available collector (counters and gauges),
	// concurrently and bounded to the pool.
	tA := nowUTC()
	g1, gctx := errgroup.WithContext(ctx)
	g1.SetLimit(4)
	for _, c := range registry {
		c := c
		if !c.Available(caps) {
			mu.Lock()
			results[c.Name()] = &sampled{}
			mu.Unlock()
			continue
		}
		g1.Go(func() error {
			v, err := c.Sample(gctx, t, caps)
			mu.Lock()
			results[c.Name()] = &sampled{A: v, Err: err}
			mu.Unlock()
			return nil
		})
	}
	_ = g1.Wait()

	// Wait out the sample interval, then re-read the counters (sample B).
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(iv):
	}
	tB := nowUTC()
	dt := tB.Sub(tA)

	g2, gctx2 := errgroup.WithContext(ctx)
	g2.SetLimit(4)
	for _, c := range registry {
		c := c
		if c.Kind() != KindCounter || !c.Available(caps) {
			continue
		}
		g2.Go(func() error {
			v, err := c.Sample(gctx2, t, caps)
			mu.Lock()
			r := results[c.Name()]
			if r == nil {
				r = &sampled{}
				results[c.Name()] = r
			}
			r.B = v
			if err != nil && r.Err == nil {
				r.Err = err
			}
			mu.Unlock()
			return nil
		})
	}
	_ = g2.Wait()

	out := newContext(caps, tB, dt)
	for _, c := range registry {
		s := results[c.Name()]
		if s == nil {
			s = &sampled{}
		}
		c.Assemble(out, caps, *s, dt, opts)
	}
	return out, nil
}
