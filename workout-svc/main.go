// Command workout-svc owns the training log.
//
// It used to be two files in the web service - a route handler and a JSON
// document on a volume. It is here for the same reason the KakaoTalk collector
// is its own service: the log is data with its own lifetime, and a Next.js
// process that gets rebuilt on every UI change is a poor custodian of it.
//
// The wire contract is unchanged. The web service proxies /api/workout here,
// and the browser cannot tell the difference.
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

	"github.com/choigonyok/jarvis/workout-svc/internal/api"
	"github.com/choigonyok/jarvis/workout-svc/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL 이 없습니다. 이 서비스는 Postgres 없이 할 수 있는 일이 없습니다.")
		os.Exit(1)
	}
	addr := getenv("LISTEN_ADDR", ":8091")
	token := os.Getenv("API_TOKEN")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// compose 가 db-migrate 뒤에 띄우지만, 재시작 순서가 뒤집히는 경우가 있어
	// 여기서도 잠깐 기다린다. 첫 연결 실패로 죽으면 restart 루프가 된다.
	st, err := openWithRetry(ctx, dsn, 30, time.Second, log)
	if err != nil {
		log.Error("데이터베이스에 연결하지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(st, token, log).Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Info("workout-svc 가 듣기 시작했습니다", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 서버", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("workout-svc 를 내립니다")
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}

func openWithRetry(
	ctx context.Context, dsn string, tries int, wait time.Duration, log *slog.Logger,
) (*store.Store, error) {
	var last error
	for i := 0; i < tries; i++ {
		st, err := store.Open(ctx, dsn)
		if err == nil {
			return st, nil
		}
		last = err
		if i == 0 {
			log.Info("데이터베이스를 기다립니다", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, last
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
