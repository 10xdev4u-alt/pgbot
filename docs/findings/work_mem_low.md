---
id: work_mem_low
severity: warn
critical_when: ""
dimension: throughput
object: setting
scope: workload
requires: []
thresholds: []
related: [work_mem_overcommit, wait_io_bound]
---

# work_mem_low

**Severity:** warn · **Dimension:** throughput · **Object identity:** `setting:work_mem` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

pgbot does **not** read `work_mem` and compare it to a number — the setting alone
tells you nothing. It infers that `work_mem` is too small from the *symptom*: sorts
and hashes spilling to disk. The literal condition is a temporary-file write rate at
or above **1 MiB/s** (`tuneTempSpillBytesPerSec = 1 << 20`), derived from the
`pg_stat_database.temp_bytes` counter across two snapshots. When that rate is
exceeded, pgbot reports the current `work_mem` value in the title as the likely knob.

## Why it matters

Each sort, hash join, or hash aggregate that doesn't fit in `work_mem` spills to
temporary files on disk and finishes with a slower on-disk algorithm. A steady stream
of temp-file writes means queries are routinely paying disk latency for work that
could have happened in memory — a throughput tax spread across every affected query,
and often the real reason a workload looks "I/O bound."

## How to verify it yourself

```sql
-- The temp-file counters pgbot rates. Cumulative since the last stats reset:
SELECT datname,
       temp_files,
       temp_bytes,
       pg_size_pretty(temp_bytes) AS temp_total
FROM pg_stat_database
WHERE temp_bytes > 0
ORDER BY temp_bytes DESC;

SHOW work_mem;
```

pgbot measures the *rate* between two snapshots; a single `temp_bytes` reading only
shows the lifetime total. To catch spills as they happen and see which statements
cause them, set `log_temp_files = 0` and watch the server log.

## How to fix it

Give operations more memory, or remove the need to sort:

```sql
ALTER SYSTEM SET work_mem = '32MB';   -- start modest; see the caution below
SELECT pg_reload_conf();
```

`work_mem` is reloadable (no restart) and can also be set per-session
(`SET work_mem = '256MB'`) or per-role (`ALTER ROLE reporting SET work_mem = …`) —
often the better move, since a single reporting query may need far more than the OLTP
default. **Caution:** `work_mem` is allocated *per operation*, so a global raise
multiplies across every concurrent sort/hash and can invite OOM — see
[work_mem_overcommit](work_mem_overcommit.md). Alternatively, add an index that lets
the planner avoid the sort or switch to a nested-loop, eliminating the spill entirely.

## When to ignore it

A known analytics or ETL workload where large sorts spilling to disk is expected and
acceptable, or where you've already raised `work_mem` per-session for the offending
queries and don't want a global bump. Suppression is the clean per-object case —
scoped to this one setting, it mutes nothing else:

```toml
[[ignore]]
finding = "work_mem_low"
object  = "setting:work_mem"
reason  = "nightly ETL sorts intentionally spill; work_mem raised per-session for it"
expires = "2027-01-01"
```

## What pgbot cannot see

- It sees the aggregate temp-file **rate**, not which query or which operation spilled.
  A single rogue reporting query can trip the finding for the whole cluster.
- It cannot tell a rare batch job's spill from steady OLTP pressure — both add to
  `temp_bytes`. Use `log_temp_files` or `pg_stat_statements.temp_blks_written` to
  attribute it to a statement.
- The counters are cumulative since the last stats reset, so a recent reset can hide a
  chronic problem (and vice versa).

## Related

- [work_mem_overcommit](work_mem_overcommit.md) — the opposite risk: raising
  `work_mem` too far, so worst-case concurrent allocation threatens OOM.
- [wait_io_bound](wait_io_bound.md) — spilling sorts are a common hidden source of the
  I/O waits it reports.
