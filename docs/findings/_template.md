---
id: <finding_id>
severity: warn            # base severity: critical | warn | info
critical_when: ""         # when it escalates to critical ("" if the severity is fixed)
dimension: storage        # risk | storage | latency | throughput  (must match Impact.Dimension)
object: cluster           # suppression object class: cluster|relation|index|query|slot|sub|setting|db
requires: []              # capabilities/versions, e.g. [pg_stat_statements, PG16+]
thresholds: []            # overridable [thresholds] keys, e.g. [unused_index_min_size_mb]
related: []               # finding ids that travel with this one
---

# <finding_id>

**Severity:** warn (critical when …) · **Dimension:** storage · **Object identity:** `cluster` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

The literal condition, with the exact threshold and the constant name that
overrides it in `[thresholds]`.

## Why it matters

Two or three sentences. Consequence, not restatement.

## How to verify it yourself

A copy-pasteable, read-only query the user can run to confirm pgbot is right.

```sql
SELECT …;
```

## How to fix it

Concrete steps. `CONCURRENTLY` where relevant.

## When to ignore it

The legitimate cases — with the exact pasteable suppression:

```toml
[[ignore]]
finding = "<finding_id>"
reason  = "…"
expires = "2027-01-01"
```

## What pgbot cannot see

The limits: per-node scan counts, cumulative counters, estimate vs measurement.

## Related

Links to findings that travel with this one.
