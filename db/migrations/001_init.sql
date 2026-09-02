CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Static vessel metadata, one row per vessel.
CREATE TABLE IF NOT EXISTS vessels (
    mmsi        BIGINT PRIMARY KEY,
    name        TEXT        NOT NULL DEFAULT '',
    vessel_type INTEGER     NOT NULL DEFAULT 0,
    first_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Raw samples. No primary key on purpose: ingest is at-least-once and uses COPY,
-- and every downstream query takes last()/max()/avg(), so a rare duplicate is harmless.
CREATE TABLE IF NOT EXISTS vessel_samples (
    ts             TIMESTAMPTZ      NOT NULL,
    mmsi           BIGINT           NOT NULL,
    lat            DOUBLE PRECISION NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lon            DOUBLE PRECISION NOT NULL CHECK (lon BETWEEN -180 AND 180),
    sog            REAL             NOT NULL DEFAULT 0,
    cog            REAL             NOT NULL DEFAULT 0,
    heading        REAL             NOT NULL DEFAULT 0,
    engine_rpm     REAL             NOT NULL DEFAULT 0,
    fuel_rate_lph  REAL             NOT NULL DEFAULT 0,
    engine_temp_c  REAL             NOT NULL DEFAULT 0,
    fuel_level_pct REAL             NOT NULL DEFAULT 0
);

SELECT create_hypertable('vessel_samples', 'ts',
                         chunk_time_interval => INTERVAL '1 hour',
                         if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS vessel_samples_mmsi_ts_idx ON vessel_samples (mmsi, ts DESC);

-- Keep the laptop demo small: raw data older than 12 hours is dropped.
SELECT add_retention_policy('vessel_samples', INTERVAL '12 hours', if_not_exists => TRUE);
