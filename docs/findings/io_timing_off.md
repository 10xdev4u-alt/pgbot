---
id: io_timing_off
severity: info
critical_when: ""
dimension: throughput
object: setting
scope: infra
requires: []
thresholds: []
related: [wait_io_bound]
---

# io_timing_off

**Severity:** info · **Dimension:** throughput · **Object identity:** `setting:track_io_timing` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The `track_io_timing` GUC reads exactly `off` — pgbot's literal test is
`settingParam(c, "track_io_timing") != "off"` to *skip*, so the finding fires when the
value is `off`. There is no threshold to tune; it is `info`, a data-quality note about
what pgbot (and you) can measure.

## Why it matters

With `track_io_timing` off, the block read/write time counters
(`blk_read_time` / `blk_write_time` in `pg_stat_database` and `pg_stat_statements`) stay
at **zero**, and `EXPLAIN (ANALYZE, BUFFERS)` shows no I/O timing. That means neither
pgbot nor you can tell an **I/O-bound** query from a **CPU-bound** one — the single most
useful split for query tuning — which weakens both query analysis and the wait
profile. The measured overhead of turning it on is negligible on modern hardware with a
fast clock source.

## How to verify it yourself

```sql
SHOW track_io_timing;
-- confirm the counters really are dead (they'll be 0 with it off):
SELECT datname, blk_read_time, blk_write_time
FROM pg_stat_database
WHERE datname = current_database();
```

## How to fix it

Turn it on — `track_io_timing` is reloadable (SIGHUP), no restart:

```sql
ALTER SYSTEM SET track_io_timing = on;
SELECT pg_reload_conf();
```

Before enabling on **old** hardware, check the clock-source cost with the
`pg_test_timing` utility: if per-reading latency is in the low tens of nanoseconds the
overhead is negligible; if the system falls back to a slow clock source it can be
material. On any modern cloud or server host, `on` is the right default.

## When to ignore it

The measured clock overhead is genuinely unacceptable — confirmed with
`pg_test_timing` on old hardware or a slow clock source — or a managed provider leaves
it off and won't let you change it. This is a clean per-object suppression case: mute
just this one setting on a provider you can't reconfigure, and nothing else is affected.

```toml
[[ignore]]
finding = "io_timing_off"
object  = "setting:track_io_timing"
reason  = "managed provider doesn't expose track_io_timing; can't be enabled"
expires = "2027-01-01"
```

## What pgbot cannot see

- It reads the setting, not the **actual** timing overhead on this host — that needs
  `pg_test_timing` and knowledge of the clock source, which live below Postgres.
- A managed provider may not expose or permit changing `track_io_timing` at all.

## Related

- [wait_io_bound](wait_io_bound.md) — with `track_io_timing` off, pgbot loses the
  clearest evidence for this finding; turning it on sharpens the I/O-vs-CPU picture.
