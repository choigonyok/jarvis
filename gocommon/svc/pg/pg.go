// Package pg opens the stack's Postgres for a service's main. It is apart
// from svc so that a service without a database does not build pgx in.
//
// The stack starts its services and Postgres together, and a service may be
// up before Postgres answers, or come back while it restarts. A service that
// needs the database waits for it at the start instead of failing its first
// rounds - 30 times, a second apart - and stops after that, so docker
// restarts it and the log says why.
package pg

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// tries and wait are how long a start waits for the database.
var (
	tries = 30
	wait  = time.Second
)

// Retry calls open until it works, up to 30 times a second apart, saying
// once that it waits. It gives up early when ctx ends.
func Retry[T any](ctx context.Context, log *slog.Logger, open func(context.Context) (T, error)) (T, error) {
	var zero T
	var last error
	for i := range tries {
		v, err := open(ctx)
		if err == nil {
			return v, nil
		}
		last = err
		if i == 0 && log != nil {
			log.Info("데이터베이스를 기다립니다", "err", err)
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(wait):
		}
	}
	return zero, last
}

// Open is a pool on dsn of at most max connections (0: pgx's own number),
// once the database answers.
func Open(ctx context.Context, dsn string, max int32, log *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := config(dsn, max)
	if err != nil {
		return nil, err
	}
	return Retry(ctx, log, func(ctx context.Context) (*pgxpool.Pool, error) {
		pool, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
		if err != nil {
			return nil, err
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			return nil, err
		}
		return pool, nil
	})
}

// Lazy is a pool on dsn that connects when it is first used: for a service
// that watches the database rather than needs it, and must come up without it.
func Lazy(ctx context.Context, dsn string, max int32) (*pgxpool.Pool, error) {
	cfg, err := config(dsn, max)
	if err != nil {
		return nil, err
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

func config(dsn string, max int32) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if max > 0 {
		cfg.MaxConns = max
	}
	return cfg, nil
}
