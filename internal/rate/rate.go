// Package rate turns a pair of cumulative counter samples into per-second (or
// per-window) rates, guarding against the two ways this silently goes wrong:
// a counter reset between samples (negative delta) and a zero elapsed interval.
package rate

import "time"

// Delta returns b-a and whether it's a valid (non-negative) counter delta. A
// negative result means the counter was reset between samples (pg_stat_reset,
// pg_stat_statements_reset, a server restart) — the caller must omit the rate
// rather than print a garbage negative or a huge wrapped number.
func Delta(a, b int64) (int64, bool) {
	if b < a {
		return 0, false
	}
	return b - a, true
}

// PerSecond computes (b-a)/seconds. ok is false on a reset or a non-positive
// interval. Returns a pointer so an omitted rate marshals as absent JSON.
func PerSecond(a, b int64, dt time.Duration) (*float64, bool) {
	d, ok := Delta(a, b)
	if !ok {
		return nil, false
	}
	secs := dt.Seconds()
	if secs <= 0 {
		return nil, false
	}
	v := float64(d) / secs
	return &v, true
}

// PerSecondF is PerSecond for float counters (e.g. pg_stat_statements times).
func PerSecondF(a, b float64, dt time.Duration) (*float64, bool) {
	if b < a {
		return nil, false
	}
	secs := dt.Seconds()
	if secs <= 0 {
		return nil, false
	}
	v := (b - a) / secs
	return &v, true
}

// Ratio computes num/(num+den) over the window from two counter pairs — e.g.
// cache hit = hitΔ / (hitΔ + readΔ). ok is false on a reset or an empty window
// (nothing happened, so the ratio is undefined rather than 0 or NaN).
func Ratio(numA, numB, denA, denB int64) (*float64, bool) {
	num, ok1 := Delta(numA, numB)
	den, ok2 := Delta(denA, denB)
	if !ok1 || !ok2 {
		return nil, false
	}
	total := num + den
	if total == 0 {
		return nil, false
	}
	v := float64(num) / float64(total)
	return &v, true
}

// Ptr wraps a value for the many *float64 fields in the model.
func Ptr(v float64) *float64 { return &v }
