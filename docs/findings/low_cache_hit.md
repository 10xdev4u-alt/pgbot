---
id: low_cache_hit
severity: warn
critical_when: ""
dimension: throughput
object: cluster
scope: workload
requires: []
thresholds: []
related: [seq_scan_heavy, wait_io_bound]
---

# low_cache_hit

**Severity:** warn · **Dimension:** throughput · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The buffer-cache hit ratio over the sample window fell below `cacheHitWarn = 0.90` — i.e.
**under 90%** of block reads were served from `shared_buffers` and more than 10% went to
disk. pgbot computes it as `1 − blks_hit/(blks_hit + blks_read)` from `pg_stat_database`
over the window between its two counter samples.

The check is **suppressed on a cold window**: if the effective stats window is younger than
`model.ColdWindowThresholdSeconds = 900` (`Window.ColdWindow()`), the ratio is dominated by
cold-cache misses right after wake and says nothing about steady state, so nothing fires.

## Why it matters

A page that isn't in the buffer cache is fetched from storage, which is orders of magnitude
slower than RAM. A sustained sub-90% hit ratio means the working set no longer fits in
memory, so hot pages are read from disk repeatedly and throughput is capped by the storage
layer instead of the CPU. As data grows the ratio only falls further, and latency rises
across every query — not just the one that missed.

## How to verify it yourself

Read the same counters pgbot does. For a snapshot since the last stats reset:

```sql
-- READ-ONLY. Cache-hit ratio per database from pg_stat_database.
SELECT datname,
       blks_hit,
       blks_read,
       round(100.0 * blks_hit / nullif(blks_hit + blks_read, 0), 2) AS cache_hit_pct
FROM pg_stat_database
WHERE datname NOT IN ('template0', 'template1')
ORDER BY cache_hit_pct ASC NULLS LAST;
```

That ratio is cumulative since `stats_reset`. To measure the *current* rate the way pgbot's
window does, sample `blks_hit`/`blks_read` twice a minute apart and diff them — a healthy
OLTP cluster usually sits above 99%. You can also find which relations miss most via
`pg_statio_user_tables` (`heap_blks_read` vs `heap_blks_hit`).

## How to fix it

Confirm it's sustained (not a one-off scan) over a longer window first, then:

1. **Raise `shared_buffers`.** A common starting point is ~25% of system RAM; give the
   working set room to stay resident. This needs a restart.
2. **Add RAM** if the working set genuinely exceeds memory — the OS page cache also backs
   Postgres reads, so more RAM helps even beyond `shared_buffers`.
3. **Read fewer pages.** Often the ratio is low because a few queries scan far more than
   they return. Add or fix indexes ([seq_scan_heavy](seq_scan_heavy.md)) so hot queries
   stop pulling cold pages into the cache and evicting the hot ones.
4. Set `random_page_cost` to match your storage (≈1.1 on SSD) so the planner prefers index
   scans over sequential ones where appropriate.

## When to ignore it

The window happened to capture a legitimate large scan (analytics query, backup, bulk
load) whose cold reads are expected and don't reflect steady-state OLTP behavior:

```toml
[[ignore]]
finding = "low_cache_hit"
reason  = "…"
expires = "2027-01-01"
```

## What pgbot cannot see

- The ratio is **cumulative counter arithmetic**, averaged over the whole window since the
  last `pg_stat_reset()`. A recent reset makes it swing wildly; a very old window
  ([stale_stats_window](stale_stats_window.md)) buries a recent regression under months of
  good history.
- `blks_hit` counts hits in `shared_buffers` only — a read that missed the buffer cache but
  was served by the **OS page cache** still counts as a disk read here, so the number can
  understate the true memory-served rate.
- It's a cluster-wide average: one badly-indexed table can drag the ratio down while
  everything else is fine. `pg_statio_user_tables` localizes it; this finding does not.

## Related

- [wait_io_bound](wait_io_bound.md) — the active-session-sampling view of the same disk
  pressure; the two corroborate each other.
- [seq_scan_heavy](seq_scan_heavy.md) — large sequential scans are a leading cause of a low
  hit ratio, flushing hot pages out of the cache.
