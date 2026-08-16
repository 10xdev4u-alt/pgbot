-- Sequences approaching exhaustion. The effective ceiling is the LESSER of the
-- sequence's max_value and the OWNING COLUMN's integer type range: an int4
-- column fed by a (default bigint) sequence wraps at 2^31 even though max_value
-- reads 2^63. Joined through pg_depend to the owning column's type.
-- last_value is null unless the role can read the sequence (SELECT/USAGE); those
-- rows are skipped, so a locked-down role degrades gracefully.
SELECT s.schemaname   AS schema,
       s.sequencename AS sequence,
       s.last_value   AS last_value,
       least(s.max_value,
         CASE t.typname WHEN 'int2' THEN 32767::bigint
                        WHEN 'int4' THEN 2147483647::bigint
                        ELSE s.max_value END) AS ceiling,
       coalesce(reln.relname || '.' || att.attname, '') AS owned_by
FROM pg_sequences s
JOIN pg_class seqc      ON seqc.relname = s.sequencename
JOIN pg_namespace seqn  ON seqn.oid = seqc.relnamespace AND seqn.nspname = s.schemaname
LEFT JOIN pg_depend d   ON d.objid = seqc.oid AND d.classid = 'pg_class'::regclass
     AND d.refclassid = 'pg_class'::regclass AND d.deptype IN ('a', 'i')
LEFT JOIN pg_class reln ON reln.oid = d.refobjid
LEFT JOIN pg_attribute att ON att.attrelid = d.refobjid AND att.attnum = d.refobjsubid
LEFT JOIN pg_type t     ON t.oid = att.atttypid
WHERE s.last_value IS NOT NULL
ORDER BY s.last_value::numeric / nullif(least(s.max_value,
           CASE t.typname WHEN 'int2' THEN 32767 WHEN 'int4' THEN 2147483647 ELSE s.max_value END), 0) DESC
LIMIT 50;
