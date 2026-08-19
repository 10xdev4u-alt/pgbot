-- User indexes with scan counts + size + definition, plus the flags the
-- unused-index rule needs to avoid recommending an outage:
--   backs_constraint : an index enforcing a PK / UNIQUE / EXCLUSION / FK
--                      constraint (pg_constraint.conindid) — cannot be dropped.
--   is_exclusion     : exclusion-constraint index.
--   is_replident     : the table's REPLICA IDENTITY index — logical replication
--                      and UPDATE/DELETE row identity depend on it; never "unused".
--   is_partial       : has a WHERE predicate (serves a narrow path).
--   is_expression    : indexes an expression, not bare columns.
--   method           : access method (btree/gin/gist/brin/hash/spgist) — a
--                      non-btree index can serve a query shape zero-scan can't rule out.
--   columns          : bare key columns, in order (expression columns skipped),
--                      so code-correlation can search for the real identifiers.
SELECT s.schemaname            AS schema,
       s.relname               AS "table",
       s.indexrelname          AS "index",
       coalesce(s.idx_scan, 0) AS scans,
       pg_relation_size(s.indexrelid) AS bytes,
       coalesce(i.indexdef, '') AS definition,
       am.amname               AS method,
       ix.indisprimary         AS is_primary,
       ix.indisunique          AS is_unique,
       ix.indisexclusion       AS is_exclusion,
       ix.indisreplident       AS is_replident,
       (ix.indpred IS NOT NULL) AS is_partial,
       (ix.indexprs IS NOT NULL) AS is_expression,
       EXISTS (SELECT 1 FROM pg_constraint co WHERE co.conindid = s.indexrelid) AS backs_constraint,
       coalesce((
         SELECT array_agg(a.attname::text ORDER BY k.ord)
         FROM unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord)
         JOIN pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = k.attnum
         WHERE k.attnum <> 0
       ), '{}') AS columns
FROM pg_stat_user_indexes s
JOIN pg_index ix ON ix.indexrelid = s.indexrelid
JOIN pg_class ci ON ci.oid = s.indexrelid
JOIN pg_am am ON am.oid = ci.relam
LEFT JOIN pg_indexes i
  ON i.schemaname = s.schemaname AND i.tablename = s.relname AND i.indexname = s.indexrelname
ORDER BY bytes DESC
LIMIT 200;
