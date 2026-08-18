---
id: connections_overprovisioned
severity: info
critical_when: ""
dimension: cost
object: cluster
scope: workload
requires: []
thresholds: []
related: [connection_saturation, work_mem_overcommit]
---

# connections_overprovisioned

**Severity:** info · **Dimension:** cost · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

`max_connections` is set well above real usage. pgbot fires only when the ceiling is
already large — **`max_connections >= tuneConnOverprovMax` = 200** — *and* observed
usage is a small fraction of it — **`connectionsUsed < max_connections ×
tuneConnOverprovUseFrac`**, i.e. **under 15%** of the configured slots in use. Both
conditions must hold, which keeps this an `info`-level cost nudge rather than an
alarm; confidence is `0.6`. The basis is `max_connections` compared against the
observed live connection count.

## Why it matters

`max_connections` is not free headroom. Every slot reserves per-backend resources
whether or not it is ever used: backend memory and, crucially, a share of the
worst-case working-memory budget — each query operation can allocate up to
`work_mem`, so `max_connections × work_mem × (operations per query)` is the memory
the server must be prepared to hand out. A ceiling of thousands with a hundred
actual clients means you are sizing RAM (and `shared_buffers` trade-offs) for a load
that never arrives, and you are leaving room for a **connection storm** — a
thundering herd of new backends — to consume memory faster than the box can absorb.
Lowering the ceiling and fronting the database with a pooler is almost always the
cheaper, safer shape.

## How to verify it yourself

```sql
SELECT count(*)                                              AS used,
       current_setting('max_connections')::int              AS max,
       round(100.0 * count(*)
             / current_setting('max_connections')::int, 1)  AS pct_used
FROM pg_stat_activity;
```

A `max` of several hundred with `pct_used` in the low single digits is exactly the
shape this finding flags. To gauge the memory the headroom reserves in the worst
case, compare `max_connections × work_mem` against RAM:

```sql
SELECT current_setting('max_connections')          AS max_connections,
       current_setting('work_mem')                 AS work_mem,
       current_setting('shared_buffers')           AS shared_buffers;
```

## How to fix it

1. **Front the database with a pooler.** PgBouncer in `transaction` mode lets many
   client connections share a small server pool, so the application keeps its
   concurrency while the database's `max_connections` stays modest.
2. **Lower `max_connections` to match real concurrency** (plus a sensible margin and
   whatever your pooler and admin tooling need):
   ```sql
   ALTER SYSTEM SET max_connections = 200;
   -- requires a restart to take effect
   SELECT pg_reload_conf();
   ```
   Size it from observed peak usage, not from a round number inherited from an old
   config. Then you can safely raise `work_mem` for the connections that remain.

## When to ignore it

The high ceiling is **deliberate** — you front the database with a pooler that opens
a large but mostly-idle server pool, or you are provisioned for a known burst that
the snapshot simply didn't catch. Since this is a cluster-wide setting-versus-usage
comparison with no per-object identity, the suppression is cluster-scoped:

```toml
[[ignore]]
finding = "connections_overprovisioned"
reason  = "max_connections sized for documented failover/burst headroom; accepted, OPS-5099"
expires = "2027-01-01"
```

## What pgbot cannot see

- It compares the ceiling against a **single snapshot** of usage. If your real peak
  concurrency lands between samples, the instantaneous count understates it and the
  headroom may be justified.
- It cannot see whether a **pooler** deliberately maintains a large idle server pool,
  or whether the headroom exists for a planned burst or failover consolidation.
- The check is a **cluster-level** setting-vs-usage comparison — there is no relation,
  query, or PID to attach it to. That is why it is `cluster`-scoped and its
  `[[ignore]]` has no `object` line: suppression is wholesale, by design.

## Related

- [connection_saturation](connection_saturation.md) — the opposite failure mode:
  usage pressing *against* `max_connections`. The healthy middle is a modest ceiling
  fronted by a pooler.
- [work_mem_overcommit](work_mem_overcommit.md) — a high `max_connections` multiplies
  the worst-case `work_mem` the server must be ready to allocate; the two findings
  share the same memory-budget arithmetic.
