// Command events-svc is the event log every service writes to and every
// consumer reads from. Services say what happened (POST /events); consumers
// register what they want (PUT /subscriptions/{name}) and get it delivered,
// in order, at least once, until they say they took it.
//
// It decides nothing. Whether an event becomes a push is notify-svc's call,
// whether it is worth a suggestion is the agent's - each next to the state
// that call needs (the person's notification settings, the record of what
// was already suggested and how it went).
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

	"github.com/choigonyok/jarvis/events-svc/internal/api"
	"github.com/choigonyok/jarvis/events-svc/internal/bus"
	"github.com/choigonyok/jarvis/events-svc/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
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

	token := os.Getenv("API_TOKEN")
	st := store.New(pool)
	b := bus.New(st, token, log)
	go b.Run(ctx)
	go drain(ctx, st, b.Notify, log)
	go prune(ctx, st, time.Duration(atoi(getenv("EVENTS_RETAIN_DAYS", "90")))*24*time.Hour, log)

	addr := getenv("LISTEN_ADDR", ":8101")
	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(st, b.Notify, func() { b.Sync(ctx) }, token, log).Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		log.Info("events-svc 가 듣기 시작했습니다", "addr", addr)
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

// drain keeps the outbox empty: what services wrote there becomes events.
// A second is as late as an event gets when events-svc is up.
func drain(ctx context.Context, st *store.Store, wake func(), log *slog.Logger) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		n, err := st.Drain(ctx, 200, api.Parse, func(id int64, err error) {
			log.Warn("outbox 의 이벤트를 버립니다", "id", id, "err", err)
		})
		switch {
		case err != nil && ctx.Err() == nil:
			log.Warn("outbox 를 비우지 못했습니다", "err", err)
		case n > 0:
			wake()
			continue // there may be more
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// prune drops what every consumer has long since taken, once a day.
func prune(ctx context.Context, st *store.Store, keep time.Duration, log *slog.Logger) {
	for {
		if n, err := st.Prune(ctx, keep); err != nil && ctx.Err() == nil {
			log.Warn("오래된 이벤트를 지우지 못했습니다", "err", err)
		} else if n > 0 {
			log.Info("오래된 이벤트를 지웠습니다", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
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
