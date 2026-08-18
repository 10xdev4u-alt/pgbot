---
id: connection_saturation
severity: warn
critical_when: "at/near max_connections"
dimension: risk
object: cluster
scope: workload
requires: []
thresholds: []
related: [connections_overprovisioned, idle_in_transaction]
---

# connection_saturation

**Severity:** warn (critical when at/near max_connections) · **Dimension:** risk · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

Open connections have reached a high fraction of `max_connections`. pgbot computes
`frac = connectionsUsed / connectionsMax` and fires at **`connSaturationWarn` = 0.85
(85%)** as `warn`, escalating to `critical` at **`connSaturationCrit` = 0.95 (95%)**.
Confidence is `1.0` — it is a direct count against a hard ceiling — and the impact
score is the utilization percentage itself. When session detail is available, the
evidence breaks the connections down by application, user, and state, so you can see
*where* the slots are going.

## Why it matters

`max_connections` is a hard wall, not a soft target. Once every slot is taken,
Postgres **refuses new connections** with `FATAL: sorry, too many clients already`
(minus any slots held back by `superuser_reserved_connections` /
`reserved_connections`). At that point new application requests can't get a database
connection at all — the app effectively locks out even though the server is healthy.
The two ways to get here are a **pool leak** (connections opened and never returned,
often as `idle` or `idle in transaction`) and a **traffic burst** that outruns the
pool. Approaching the ceiling also means each of those backends is reserving memory
(`work_mem` per operation plus per-backend overhead), so saturation and memory
pressure tend to arrive together.

## How to verify it yourself

```sql
SELECT count(*)                                              AS used,
       current_setting('max_connections')::int              AS max,
       round(100.0 * count(*)
             / current_setting('max_connections')::int, 1)  AS pct_used
FROM pg_stat_activity;
```

Then see **where** the connections come from — the breakdown that turns the warning
into an action:

```sql
SELECT application_name, usename, state, count(*) AS conns
FROM pg_stat_activity
GROUP BY application_name, usename, state
ORDER BY conns DESC;
```

A large `idle` or `idle in transaction` bucket points at a pool that is holding
connections it isn't using.

## How to fix it

1. **Put a pooler in front.** PgBouncer in `transaction` pooling mode lets hundreds
   or thousands of client connections share a small set of server connections — this
   is the standard fix and usually the right one. The application pools connect to
   PgBouncer; PgBouncer keeps `max_connections` low and stable.
2. **Right-size per-service pools.** If several services each open a fat pool, the
   sum can exceed `max_connections`. Lower each service's maximum pool size so the
   totals fit, and make sure idle connections are reaped.
3. **Raise `max_connections` only as a last resort.** Each additional slot costs
   memory whether used or not, so bump it only when you have RAM to spare and have
   ruled out a leak — otherwise you are enlarging the wall, not removing it.
4. **Stop leaks at the source.** Terminate and then fix sessions stuck
   `idle in transaction` (see [idle_in_transaction](idle_in_transaction.md)); they
   consume a slot indefinitely.

## When to ignore it

A **planned** load event — a launch, a migration backfill, a load test — where you
expect to run close to the ceiling for a bounded time and have verified it is not a
leak. The connections are identified only by ephemeral PIDs and the check is a
cluster-wide count, so the suppression is cluster-scoped:

```toml
[[ignore]]
finding = "connection_saturation"
reason  = "load test week of 2026-12; peak ~90% of max_connections expected, monitored in OPS-4410"
expires = "2027-01-01"
```

## What pgbot cannot see

- It sees a **snapshot** count, not the peak between samples — a burst that briefly
  hit the wall and receded may not appear, and a momentary spike it caught may not
  be representative.
- It cannot see the **pooler's** own view — whether PgBouncer is queuing clients,
  and how close *that* layer is to its own limits.
- The connections behind the number are keyed by **PID**, an ephemeral identifier,
  and the finding is a cluster-wide ratio. That is why it is `cluster`-scoped with no
  `object` line in its `[[ignore]]`: there is no durable object to suppress, so it is
  all-or-nothing by design.

## Related

- [connections_overprovisioned](connections_overprovisioned.md) — the mirror image:
  `max_connections` set far *above* real usage, wasting memory and inviting storms.
- [idle_in_transaction](idle_in_transaction.md) — leaked idle-in-transaction sessions
  are a common way the connection count creeps toward the ceiling.
