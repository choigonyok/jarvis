// Command notify-svc is where every service's news goes: one structured event
// format in (POST /events), and out of it the console's inbox, a push to the
// phone when someone has to act, and an evening digest of the rest.
//
// The services only say what happened and how much it matters; whether that
// becomes a push now, a push after quiet hours, a line in tonight's digest or
// just an inbox entry is decided here, against the person's settings.
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

	"github.com/choigonyok/jarvis/notify-svc/internal/api"
	"github.com/choigonyok/jarvis/notify-svc/internal/notify"
	"github.com/choigonyok/jarvis/notify-svc/internal/push"
	"github.com/choigonyok/jarvis/notify-svc/internal/store"
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
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Error("데이터베이스 풀을 만들지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	loc, err := time.LoadLocation(getenv("TZ", "Asia/Seoul"))
	if err != nil {
		loc = time.FixedZone("KST", 9*60*60)
	}

	st := store.New(pool)
	pusher := push.New(push.Keys{
		Public:  os.Getenv("VAPID_PUBLIC_KEY"),
		Private: os.Getenv("VAPID_PRIVATE_KEY"),
		Subject: getenv("VAPID_SUBJECT", "https://jarvis.choigonyok.com"),
	}, st, log)
	if !pusher.Ready() {
		log.Warn("VAPID 키가 없어 푸시는 보내지 않고 알림함에만 쌓습니다")
	}
	svc := notify.New(st, pusher, loc)

	// Held pushes go out when quiet hours end; the digest at its time.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := svc.Tick(ctx); err != nil {
					log.Error("예약 알림을 보내지 못했습니다", "err", err)
				}
			}
		}
	}()

	addr := getenv("LISTEN_ADDR", ":8097")
	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(svc, st, pusher, os.Getenv("API_TOKEN"), log).Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	go func() {
		log.Info("notify-svc 가 듣기 시작했습니다", "addr", addr)
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
