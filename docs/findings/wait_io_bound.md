---
id: wait_io_bound
severity: warn
critical_when: ""
dimension: throughput
object: cluster
scope: workload
requires: [ASH sampling (ash-hz>0), track_io_timing]
thresholds: []
related: [low_cache_hit, seq_scan_heavy]
---

# wait_io_bound

**Severity:** warn · **Dimension:** throughput · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** ASH sampling (ash-hz>0), track_io_timing

## What pgbot observed

Sampling `pg_stat_activity` many times over the collection window (active-session history,
at `--ash-hz`, default **10 Hz**), pgbot found the whole window dominated by storage waits.
The literal condition is `share("IO") > waitIOBoundShare` where `waitIOBoundShare = 0.50`:
**more than 50%** of all active-session samples had `wait_event_type = 'IO'` (e.g.
`DataFileRead`, `DataFileWrite`, `WALWrite`) — active backends were waiting on the disk,
not on CPU or on locks.

As with every wait finding this is gated on `model.WaitMinSamples = 20`: below 20 total
samples the profile is `Thin()` and nothing fires. The 50% is a share of *sampled
moments*, not a measured I/O time.

## Why it matters

When active time is spent waiting on storage, the database's throughput ceiling is the
disk, not the CPU — adding workers or connections just lengthens the I/O queue. It almost
always means the working set no longer fits in RAM (so hot pages are read from disk on
every access) or that a few queries are scanning far more data than they return. Either
way, latency rises across the board and gets worse as data grows.

## How to verify it yourself

The profile is a *sample*, so reproduce it by polling `pg_stat_activity` repeatedly and
tallying the `IO` share of active backends:

```sql
-- READ-ONLY. Run repeatedly (e.g. \watch 0.1) over a minute and aggregate:
-- the fraction of active samples in wait_event_type='IO' is what pgbot compares
-- against 50%. One snapshot is not a profile — sample many times.
SELECT wait_event_type,
       wait_event,
       count(*) AS sampled
FROM pg_stat_activity
WHERE state = 'active'
  AND backend_type = 'client backend'
  AND pid <> pg_backend_pid()
GROUP BY wait_event_type, wait_event
ORDER BY sampled DESC;
```

Corroborate against cumulative counters — the buffer-cache miss rate and, with
`track_io_timing = on`, the actual read/write time per database:

```sql
SELECT datname,
       blks_hit, blks_read,
       round(100.0 * blks_hit / nullif(blks_hit + blks_read, 0), 2) AS cache_hit_pct,
       blk_read_time, blk_write_time
FROM pg_stat_database
WHERE datname NOT IN ('template0', 'template1')
ORDER BY blks_read DESC;
```

## How to fix it

The wait profile says the bottleneck is I/O, so attack the read volume and the cache:

1. **Fit more of the working set in cache.** Raise `shared_buffers` (a common start is
   ~25% of RAM) and/or add RAM. See [low_cache_hit](low_cache_hit.md) for the ratio.
2. **Read fewer pages.** The cheapest I/O is the read you don't do — add or fix indexes so
   hot queries stop scanning. Check [seq_scan_heavy](seq_scan_heavy.md) and use
   `EXPLAIN (ANALYZE, BUFFERS)` to find large scans that return few rows.
3. **Raise `effective_io_concurrency`** on SSD/NVMe so bitmap heap scans prefetch, and make
   sure `random_page_cost` reflects your storage (≈1.1 on SSD) so the planner stops
   avoiding index scans.
4. If writes dominate the `IO` bucket (`WALWrite`, `DataFileWrite`), it is a checkpoint /
   WAL-flush pattern — see [wait_lwlock_pressure](wait_lwlock_pressure.md) and checkpoint
   tuning.

## When to ignore it

The window was captured during a known bulk operation (a backup, a big `COPY`/restore, a
one-off backfill) whose I/O is expected and bounded:

```toml
[[ignore]]
finding = "wait_io_bound"
reason  = "…"
expires = "2027-01-01"
```

## What pgbot cannot see

- A wait profile is a **distribution over sampled moments**, never an exact I/O time. The
  finding carries its sample count; a 50%+ share off a thin sample is suggestive, not
  measured — confidence scales with the total (20 thin, 200 solid).
- It cannot tell you *which* query or *which* relation drove the reads unless the profile
  also has per-query attribution; the `pg_stat_database` and `EXPLAIN (BUFFERS)` steps
  above localize it.
- It sees the wait, not the storage layer beneath it — a noisy neighbor, a throttled cloud
  volume, or a degraded RAID array all look like the same `IO` samples from inside
  Postgres.

## Related

- [low_cache_hit](low_cache_hit.md) — the cumulative-counter view of the same undersized
  cache; the two usually fire together.
- [seq_scan_heavy](seq_scan_heavy.md) — large sequential scans are a leading source of the
  read volume this finding samples.
