-- Redundant indexes: index A whose leading columns are a prefix of index B on the
-- same table (or an exact duplicate) is usually safe to drop — B serves the same
-- access paths. Matched on indkey (column list) AND indclass (operator classes)
-- prefix, same access method, so a differing opclass/collation doesn't produce a
-- false positive. A must be droppable and not a narrow-path index (same
-- exclusions the unused-index rule uses). Duplicates are reported once (higher oid).
SELECT n.nspname                        AS schema,
       t.relname                        AS "table",
       ia.relname                       AS redundant_index,
       ib.relname                       AS covering_index,
       pg_relation_size(a.indexrelid)   AS redundant_bytes
FROM pg_index a
JOIN pg_index b
  ON a.indrelid = b.indrelid AND a.indexrelid <> b.indexrelid
JOIN pg_class ia ON ia.oid = a.indexrelid
JOIN pg_class ib ON ib.oid = b.indexrelid
JOIN pg_class t  ON t.oid  = a.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE ia.relam = ib.relam
  AND (
        (b.indkey::text LIKE a.indkey::text || ' %'
           AND b.indclass::text LIKE a.indclass::text || ' %')                       -- a is a strict prefix of b
     OR (b.indkey::text = a.indkey::text AND b.indclass::text = a.indclass::text
           AND a.indexrelid > b.indexrelid)                                          -- exact duplicate, report once
      )
  AND NOT a.indisprimary AND NOT a.indisunique AND NOT a.indisexclusion
  AND a.indpred IS NULL AND a.indexprs IS NULL
  -- the COVERING index b must also be non-partial and non-expression: a partial b
  -- only indexes a subset of rows (so it can't serve all of a's lookups), and an
  -- expression b has 0-valued indkey entries that would match a prefix spuriously.
  AND b.indpred IS NULL AND b.indexprs IS NULL
  AND NOT EXISTS (SELECT 1 FROM pg_constraint co WHERE co.conindid = a.indexrelid)
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY redundant_bytes DESC
LIMIT 50;
