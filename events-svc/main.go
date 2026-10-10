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

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
