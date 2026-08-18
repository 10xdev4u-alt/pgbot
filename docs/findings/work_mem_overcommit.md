---
id: work_mem_overcommit
severity: warn
critical_when: ""
dimension: risk
object: setting
scope: infra
requires: []
thresholds: []
related: [work_mem_low, connections_overprovisioned]
---

# work_mem_overcommit

**Severity:** warn · **Dimension:** risk · **Object identity:** `setting:work_mem` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

pgbot computed worst-case sort memory as `work_mem × max_connections` and found it
**greater than** `effective_cache_size`. The literal condition is
`parseMemBytes(work_mem) × max_connections > parseMemBytes(effective_cache_size)`,
all three read straight from the GUCs. There is no tunable threshold — it is a direct
inequality between three settings.

## Why it matters

`work_mem` is a budget **per sort/hash operation**, not per connection or per query:
a single complex query with several sorts and hash joins can allocate multiples of
`work_mem` at once. So the true worst case under load is even higher than
`work_mem × max_connections`. When that already exceeds the memory you've told the
planner exists, a burst of concurrent memory-hungry queries can drive the host into
**OOM** — and the Linux OOM killer typically kills a Postgres backend or the
postmaster, forcing a crash-and-recover of the entire cluster. This is a latent
foot-gun: everything is fine until a traffic spike lines up the wrong queries.

## How to verify it yourself

```sql
-- Mirror pgbot's arithmetic, read-only:
SELECT current_setting('work_mem')             AS work_mem,
       current_setting('max_connections')      AS max_connections,
       current_setting('effective_cache_size') AS effective_cache_size,
       pg_size_pretty(
         pg_size_bytes(current_setting('work_mem'))
         * current_setting('max_connections')::bigint
       )                                        AS worst_case_sort_mem;
```

If `worst_case_sort_mem` exceeds `effective_cache_size` — and, more importantly, your
real physical RAM minus `shared_buffers` and OS overhead — the headroom isn't there.

## How to fix it

Bring the worst case back under available memory by any of:

```sql
ALTER SYSTEM SET work_mem = '8MB';    -- lower the per-operation budget
SELECT pg_reload_conf();
```

`work_mem` is reloadable (no restart). Other levers: cap real concurrency with a
pooler (PgBouncer) so `max_connections` overstates true parallelism far less; or add
host RAM. The best pattern is a **modest global `work_mem`** plus a per-session or
per-role raise (`SET work_mem = …`) for the few queries that genuinely need it — see
[work_mem_low](work_mem_low.md) for the spill side of the same trade-off.

## When to ignore it

You've confirmed real concurrency is capped far below `max_connections` — typically a
pooler in front — so multiplying by the raw `max_connections` ceiling wildly
overstates the worst case. Suppression is the clean per-object case, scoped to this
one setting:

```toml
[[ignore]]
finding = "work_mem_overcommit"
object  = "setting:work_mem"
reason  = "PgBouncer caps active backends at 40; max_connections ceiling is theoretical"
expires = "2027-01-01"
```

## What pgbot cannot see

- It multiplies by `max_connections`, the ceiling, not by **actual** concurrency.
  Behind a pooler the real worst case is far smaller, so this can over-warn.
- `effective_cache_size` is a **planner hint** about combined OS and shared-buffer
  cache, not a measurement of free RAM, so the comparison is a rough proxy rather than
  a precise memory accounting.
- It uses one `work_mem` per connection, but a single query can hold several `work_mem`
  allocations at once — so the real worst case can also be *higher* than what pgbot
  computes.

## Related

- [work_mem_low](work_mem_low.md) — the opposite failure: `work_mem` too small,
  spilling sorts to disk. The two findings bracket the safe range for the knob.
- [connections_overprovisioned](connections_overprovisioned.md) — a high
  `max_connections` inflates this worst case; lowering it (with a pooler) fixes both.
