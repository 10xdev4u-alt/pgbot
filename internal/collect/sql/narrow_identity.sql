-- Narrow (int2/int4) columns backed by a sequence — identity columns or serial /
-- owned sequences. Purely structural: it reads pg_attribute/pg_depend, never the
-- sequence's last_value, so it detects the problem on a freshly-migrated database
-- that has never been inserted into (where last_value is still NULL). An int4
-- identity/serial wraps at 2.1 billion regardless of the sequence's own max_value;
-- a common migration mistake that's invisible to review of the migration file.
SELECT n.nspname  AS schema,
       c.relname  AS "table",
       a.attname  AS "column",
       t.typname  AS type
FROM pg_attribute a
JOIN pg_class c     ON c.oid = a.attrelid AND c.relkind IN ('r', 'p')
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_type t      ON t.oid = a.atttypid
WHERE a.attnum > 0 AND NOT a.attisdropped
  AND t.typname IN ('int2', 'int4')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND (
    a.attidentity IN ('a', 'd')  -- GENERATED { ALWAYS | BY DEFAULT } AS IDENTITY
    OR EXISTS (                  -- serial / an owned sequence (pg_depend 'a')
      SELECT 1
      FROM pg_depend d
      JOIN pg_class s ON s.oid = d.objid AND s.relkind = 'S'
      WHERE d.refobjid = a.attrelid AND d.refobjsubid = a.attnum AND d.deptype = 'a'
    )
  )
ORDER BY 1, 2, 3
LIMIT 200;
