-- Partitioned-table rollup. pg_stat_user_tables lists every leaf partition as its
-- own relation, so a 200-partition table floods the top-N and each partition's
-- seq_scan looks harmless while the PARENT is scanned end to end. Climb pg_inherits
-- from each leaf partition to its root (topmost non-partition ancestor) and
-- aggregate size + scan counts + partition count. Per-partition detail stays in
-- the tables section; this is the parent-level view seq_scan_heavy needs.
WITH RECURSIVE climb AS (
  SELECT c.oid AS leaf, c.oid AS node, c.relispartition
  FROM pg_class c
  WHERE c.relkind = 'r' AND c.relispartition
  UNION ALL
  SELECT climb.leaf, i.inhparent, pc.relispartition
  FROM climb
  JOIN pg_inherits i ON i.inhrelid = climb.node
  JOIN pg_class pc   ON pc.oid = i.inhparent
  WHERE climb.relispartition
),
roots AS (
  SELECT leaf, node AS root FROM climb WHERE NOT relispartition
)
SELECT n.nspname                            AS schema,
       rc.relname                           AS "table",
       count(*)                             AS partitions,
       sum(pg_total_relation_size(s.relid)) AS total_bytes,
       sum(s.n_live_tup)                    AS live_tuples,
       sum(s.seq_scan)                      AS seq_scans,
       sum(coalesce(s.idx_scan, 0))         AS index_scans
FROM roots r
JOIN pg_stat_user_tables s ON s.relid = r.leaf
JOIN pg_class rc     ON rc.oid = r.root
JOIN pg_namespace n  ON n.oid = rc.relnamespace
GROUP BY 1, 2
ORDER BY total_bytes DESC
LIMIT 20;
