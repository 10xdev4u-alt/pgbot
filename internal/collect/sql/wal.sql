-- pg_stat_wal (PG14+), double-sampled for byte/record rates.
SELECT wal_records, wal_bytes, wal_buffers_full
FROM pg_stat_wal;
