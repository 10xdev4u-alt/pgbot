-- Parameters whose active value differs from the compiled-in default — the
-- interesting, human-set knobs.
SELECT name, setting
FROM pg_settings
WHERE setting IS DISTINCT FROM boot_val
ORDER BY name;
