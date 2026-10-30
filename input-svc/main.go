// Command input-svc is the one way in: every producer leaves what happened in
// input.outbox, and this alone takes it out, checks the envelope, and stores
// it as a record, together with a delivery for each consumer that wants its
// kind. It decides nothing: what a record means is mind's to say, and it does
// not look inside a payload.
//
// It also keeps the tables: the raw tier past its retention, the months ahead
// partitioned. What consumers do with the stream is gocommon/queue; there is
// no process between them and the tables.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/input-svc/internal/api"
	chat "github.com/choigonyok/jarvis/input-svc/internal/chat/store"
	"github.com/choigonyok/jarvis/input-svc/internal/store"
	workout "github.com/choigonyok/jarvis/input-svc/internal/workout/store"
)

const (
	drainBatch = 200
	// drainEvery is how late a record gets if a wake-up is missed.
	drainEvery = time.Second
	// aheadMonths is how far ahead the records stay partitioned.
	aheadMonths = 6
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// An empty token would leave every route open to anything on the
	// network. Only a local trial may run that way, and must say so.
	if os.Getenv("API_TOKEN") == "" && os.Getenv("API_TOKEN_OPTIONAL") != "true" {
		log.Error("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Error("DATABASE_URL 을 해석하지 못했습니다", "err", err)
		os.Exit(1)
	}
	cfg.MaxConns = 6
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Error("데이터베이스 풀을 만들지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	st := store.New(pool)
	sealer, err := seal.FromBase64(os.Getenv("MESSAGE_KEY"))
	if err != nil {
		log.Error("MESSAGE_KEY 를 쓰지 못했습니다", "err", err)
		os.Exit(1)
	}
	turns := chat.New(pool)
	if err := turns.Resume(ctx); err != nil {
		log.Warn("id 순번을 이어받지 못했습니다", "err", err)
	}
	views := api.Views{Chat: turns, Workout: workout.New(pool), Seal: sealer, TZ: getenv("TZ", "Asia/Seoul")}
	loops := health.NewLoops()
	loops.Add("drain", "outbox 를 기록으로", 5*time.Minute)
	loops.Add("keep", "보존기간·파티션", 26*time.Hour)
	wake := make(chan struct{}, 1)
	go queue.Listen(ctx, pool, queue.OutboxChannel, func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}, log)
	go drain(ctx, st, wake, loops, log)
	go keep(ctx, st, time.Duration(atoi(getenv("INPUT_RAW_RETAIN_DAYS", "90")))*24*time.Hour, loops, log)

	addr := getenv("LISTEN_ADDR", ":8101")
	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(st, os.Getenv("API_TOKEN"), os.Getenv("API_GUEST_TOKEN"), views, loops, log).Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		log.Info("input-svc 가 듣기 시작했습니다", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 서버", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}

// drain keeps the outbox empty. It is woken by what lands there, and looks
// anyway every drainEvery.
func drain(ctx context.Context, st *store.Store, wake <-chan struct{}, loops *health.Loops, log *slog.Logger) {
	t := time.NewTicker(drainEvery)
	defer t.Stop()
	for {
		n, err := st.Drain(ctx, drainBatch)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Warn("outbox 를 비우지 못했습니다", "err", err)
			loops.Fail("drain", err)
		case err == nil:
			loops.OK("drain")
		}
		if err == nil && n >= drainBatch {
			continue // there is more
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-t.C:
		}
	}
}

// keep does the tables' housework, once a day.
func keep(ctx context.Context, st *store.Store, raw time.Duration, loops *health.Loops, log *slog.Logger) {
	for {
		err := errors.Join(prune(ctx, st, raw, log), partitions(ctx, st, log))
		switch {
		case err != nil && ctx.Err() == nil:
			log.Warn("표를 정리하지 못했습니다", "err", err)
			loops.Fail("keep", err)
		case err == nil:
			loops.OK("keep")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

func prune(ctx context.Context, st *store.Store, raw time.Duration, log *slog.Logger) error {
	n, err := st.Prune(ctx, raw)
	if err == nil && n > 0 {
		log.Info("보존기간이 지난 원본 기록을 지웠습니다", "count", n)
	}
	return err
}

func partitions(ctx context.Context, st *store.Store, log *slog.Logger) error {
	n, err := st.Partitions(ctx, aheadMonths)
	if err == nil && n > 0 {
		log.Info("기록 파티션을 만들었습니다", "count", n)
	}
	return err
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 90
	}
	return n
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
