-- One row per vessel per bucket. last(x, ts) = the most recent value inside the bucket.
-- timescaledb.materialized_only = false turns on real-time aggregation, so queries also see
-- rows that are newer than the last refresh.

CREATE MATERIALIZED VIEW IF NOT EXISTS samples_1s
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT mmsi,
       time_bucket(INTERVAL '1 second', ts) AS bucket,
       last(lat, ts)            AS lat,
       last(lon, ts)            AS lon,
       last(sog, ts)            AS sog,
       last(cog, ts)            AS cog,
       last(heading, ts)        AS heading,
       avg(engine_rpm)::REAL    AS engine_rpm,
       avg(fuel_rate_lph)::REAL AS fuel_rate_lph,
       max(engine_temp_c)       AS engine_temp_c,
       last(fuel_level_pct, ts) AS fuel_level_pct
FROM vessel_samples
GROUP BY mmsi, bucket
WITH NO DATA;

CREATE MATERIALIZED VIEW IF NOT EXISTS samples_1m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT mmsi,
       time_bucket(INTERVAL '1 minute', ts) AS bucket,
       last(lat, ts) AS lat, last(lon, ts) AS lon,
       last(sog, ts) AS sog, last(cog, ts) AS cog, last(heading, ts) AS heading,
       avg(engine_rpm)::REAL AS engine_rpm, avg(fuel_rate_lph)::REAL AS fuel_rate_lph,
       max(engine_temp_c) AS engine_temp_c, last(fuel_level_pct, ts) AS fuel_level_pct
FROM vessel_samples
GROUP BY mmsi, bucket
WITH NO DATA;

CREATE MATERIALIZED VIEW IF NOT EXISTS samples_10m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT mmsi,
       time_bucket(INTERVAL '10 minutes', ts) AS bucket,
       last(lat, ts) AS lat, last(lon, ts) AS lon,
       last(sog, ts) AS sog, last(cog, ts) AS cog, last(heading, ts) AS heading,
       avg(engine_rpm)::REAL AS engine_rpm, avg(fuel_rate_lph)::REAL AS fuel_rate_lph,
       max(engine_temp_c) AS engine_temp_c, last(fuel_level_pct, ts) AS fuel_level_pct
FROM vessel_samples
GROUP BY mmsi, bucket
WITH NO DATA;
