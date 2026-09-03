package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

var sampleCols = []string{"ts", "mmsi", "lat", "lon", "sog", "cog", "heading",
	"engine_rpm", "fuel_rate_lph", "engine_temp_c", "fuel_level_pct"}

// CopySamples bulk-inserts with the COPY protocol, far faster than row inserts.
func (s *Store) CopySamples(ctx context.Context, rows [][]any) error {
	_, err := s.Pool.CopyFrom(ctx, pgx.Identifier{"vessel_samples"}, sampleCols, pgx.CopyFromRows(rows))
	return err
}

func (s *Store) UpsertVessel(ctx context.Context, mmsi int64, name string, vtype int32) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO vessels (mmsi, name, vessel_type) VALUES ($1, $2, $3)
		ON CONFLICT (mmsi) DO UPDATE
		SET name = EXCLUDED.name, vessel_type = EXCLUDED.vessel_type, last_seen = now()`,
		mmsi, name, vtype)
	return err
}
