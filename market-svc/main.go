// Command market-svc keeps the operator's Joongna listings: drafts approved in
// conversation, the changes waiting to reach Joongna, and what the shop page
// last showed (status, views, likes, chats).
//
// It never talks to Joongna itself. The agent's background worker does that
// with the shared browser and reports back here; this service is the record
// and the queue.
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

	"github.com/choigonyok/jarvis/market-svc/internal/api"
	"github.com/choigonyok/jarvis/market-svc/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL 이 없습니다. 이 서비스는 Postgres 없이 할 수 있는 일이 없습니다.")
		os.Exit(1)
	}
	addr := getenv("LISTEN_ADDR", ":8097")
	token := os.Getenv("API_TOKEN")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openWithRetry(ctx, dsn, 30, time.Second, log)
	if err != nil {
		log.Error("데이터베이스에 연결하지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	srv := &http.Server{
		Addr: addr,
		Handler: api.New(st, token,
			duration("MARKET_SYNC_EVERY", time.Hour),
			// 판매가 끝나고 이만큼 지나면 사진 파일을 지운다.
			duration("MARKET_PURGE_AFTER", 30*24*time.Hour),
			log).Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		log.Info("market-svc 가 듣기 시작했습니다", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 서버", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("market-svc 를 내립니다")
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}

func openWithRetry(ctx context.Context, dsn string, tries int, wait time.Duration, log *slog.Logger) (*store.Store, error) {
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

func duration(key string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil && d > 0 {
		return d
	}
	return def
}
