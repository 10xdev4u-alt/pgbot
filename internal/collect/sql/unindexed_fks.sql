-- Foreign keys with no supporting index on the child. Without one, a DELETE or
-- UPDATE of a parent key sequentially scans the whole child to check references,
-- and holds locks longer. A supporting index must have the FK columns as its
-- leading columns, in order. Catalog-only; fk_columns are attnames (no data).
SELECT n.nspname   AS schema,
       c.relname   AS child_table,
       con.conname AS constraint_name,
       pg_relation_size(c.oid) AS child_bytes,
       (SELECT string_agg(a.attname, ', ' ORDER BY x.ord)
          FROM unnest(con.conkey) WITH ORDINALITY x(attnum, ord)
          JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = x.attnum) AS fk_columns
FROM pg_constraint con
JOIN pg_class c     ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE con.contype = 'f'
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND NOT EXISTS (
    SELECT 1 FROM pg_index i
    WHERE i.indrelid = con.conrelid
      AND i.indpred IS NULL  -- a partial index can't be relied on for FK checks
      AND (string_to_array(i.indkey::text, ' ')::int2[])[1:cardinality(con.conkey)] = con.conkey
  )
ORDER BY child_bytes DESC
LIMIT 50;
