---
id: sequence_exhaustion
severity: warn
critical_when: "a sequence is ≥90% consumed"
dimension: risk
object: relation
scope: workload
requires: []
thresholds: []
related: [txid_wraparound]
---

# sequence_exhaustion

**Severity:** warn (critical when a sequence is ≥90% consumed) · **Dimension:** risk · **Object identity:** `schema.sequence` (see [configuration](../configuration.md)) · **Requires:** —

## What pgbot observed

At least one sequence has consumed **≥80%** of its usable range (`warn`), or
**≥90%** (`critical`). pgbot measures usage as `last_value / min(max_value, column
type max)` — the second term matters: an `int4` identity/`serial` column wraps at
**2,147,483,647** even when the sequence's own `max_value` is the `int8` ceiling,
because the value has to fit the column.

## Why it matters

When a sequence reaches its ceiling, the very next `nextval()` raises
`nextval: reached maximum value of sequence`. Every `INSERT` that depends on it
then fails — a write outage for any table using that sequence as its primary key.
There is no gradual degradation; it works until the instant it doesn't.

## How to verify it yourself

```sql
-- Usage against each sequence's own max_value. Watch the int4 caveat below:
-- a serial/identity column on int4 really wraps at 2.1e9, not max_value.
SELECT schemaname || '.' || sequencename        AS sequence,
       last_value,
       max_value,
       round(100.0 * last_value / max_value, 2)  AS pct_used
FROM pg_sequences
WHERE last_value IS NOT NULL
ORDER BY pct_used DESC NULLS LAST
LIMIT 20;
```

To see which columns are still `int4` (the ones that wrap early):

```sql
SELECT format('%I.%I.%I', n.nspname, c.relname, a.attname) AS column, t.typname
FROM pg_attribute a
JOIN pg_class c   ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_type t    ON t.oid = a.atttypid
WHERE a.attnum > 0 AND NOT a.attisdropped
  AND pg_get_serial_sequence(format('%I.%I', n.nspname, c.relname), a.attname) IS NOT NULL
  AND t.typname = 'int4';
```

## How to fix it

Widen the owning column to `bigint`. `ALTER TABLE … ALTER COLUMN … TYPE bigint`
rewrites the whole table under an `ACCESS EXCLUSIVE` lock — acceptable for a small
table in a maintenance window, but for a large, hot table do it **online** instead.
(`pg_repack` can rewrite a bloated table but **cannot** change a column's type, so
it is not the tool for this.) The online pattern:

1. Add a new `bigint` column (a fast metadata-only change on PG11+).
2. Backfill it in **batches** — bounded `UPDATE`s by primary-key range — so you
   never lock the whole table at once, keeping a trigger in sync for new writes.
3. Once caught up, swap in a short transaction: drop/rename the old column, repoint
   the sequence's `OWNED BY`, and set the column default.

**Widen the foreign-key columns in child tables too.** If `orders.id` becomes
`bigint` while `order_items.order_id` stays `int4`, you hit the same 2.1-billion
wall from the child side once its values grow — and the type mismatch also defeats
some join optimizations. Find the child columns that still need it:

```sql
SELECT conrelid::regclass AS child_table, a.attname AS fk_column, t.typname
FROM pg_constraint c
JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
JOIN pg_type t      ON t.oid = a.atttypid
WHERE c.contype = 'f'
  AND c.confrelid = 'public.orders'::regclass
  AND t.typname = 'int4';
```

If the column is **already `bigint`**, exhaustion is astronomically far off
(9.2×10¹⁸) and the finding is almost certainly noise from a sequence whose
`max_value` was set low by hand — raise `max_value` or ignore it.

## When to ignore it

You've confirmed a **specific** sequence is `bigint`-backed, so its wrap is
astronomically far off. Scope the rule to that sequence by name — a new `int4`
serial that crosses the threshold tomorrow still surfaces, because the rule only
drops this one object:

```toml
[[ignore]]
finding = "sequence_exhaustion"
object  = "public.legacy_events_id_seq"
reason  = "already bigint; wrap is astronomically far off"
expires = "2027-01-01"
```

Do **not** omit `object` here — a bare `finding = "sequence_exhaustion"` mutes the
check for *every* sequence, including ones you add later, which is exactly how a
future `int4` overflow gets hidden.

## What pgbot cannot see

- It reads `last_value`, which lags under concurrency because of the sequence
  cache (`CACHE n`) — the true next value can be slightly ahead.
- It cannot see application-managed or sharded ID allocation that bypasses the
  sequence, nor a hi/lo allocator that reserves large blocks.
- The `int4`-column ceiling is inferred from the column type; a `domain` over
  `int4` or an unusual cast can hide it.

## Related

- [txid_wraparound](txid_wraparound.md) — a different 2.1-billion wall (transaction
  ids), also a hard write stop, but unrelated in cause.
