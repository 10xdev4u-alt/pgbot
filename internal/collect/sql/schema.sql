-- Schema fingerprint from pg_catalog (no extra grants). One row per object:
-- (kind, identity, definition, invalid). Column definitions encode type,
-- NOT NULL and WHETHER a default exists — never the default EXPRESSION, which
-- can carry literal values. Only user schemas.
SELECT 'index' AS kind,
       n.nspname || '.' || t.relname || '.' || i.relname AS identity,
       pg_get_indexdef(i.oid)                            AS definition,
       (NOT idx.indisvalid)                              AS invalid
FROM pg_index idx
JOIN pg_class i ON i.oid = idx.indexrelid
JOIN pg_class t ON t.oid = idx.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
UNION ALL
SELECT 'table',
       n.nspname || '.' || c.relname,
       c.relkind::text || c.relpersistence::text,
       false
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p') AND n.nspname NOT IN ('pg_catalog', 'information_schema')
UNION ALL
SELECT 'column',
       n.nspname || '.' || c.relname || '.' || a.attname,
       format_type(a.atttypid, a.atttypmod) || ' notnull=' || a.attnotnull::text || ' hasdefault=' || a.atthasdef::text,
       false
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p') AND a.attnum > 0 AND NOT a.attisdropped
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
UNION ALL
SELECT 'constraint',
       n.nspname || '.' || rel.relname || '.' || con.conname,
       pg_get_constraintdef(con.oid),
       false
FROM pg_constraint con
JOIN pg_class rel ON rel.oid = con.conrelid
JOIN pg_namespace n ON n.oid = rel.relnamespace
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
UNION ALL
SELECT 'extension', extname, extversion, false FROM pg_extension
UNION ALL
SELECT 'sequence', n.nspname || '.' || c.relname, 'sequence', false
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'S' AND n.nspname NOT IN ('pg_catalog', 'information_schema');
