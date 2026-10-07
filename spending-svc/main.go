// Command spending-svc keeps the household book: card charges read from the
// alerts card companies send over KakaoTalk, sorted into categories, summed
// by month, and measured against a budget.
//
// It reads the KakaoTalk collector rather than the KakaoTalk database. The
// collector already owns the hard part (the key, the live WAL copy); this
// service only needs the messages, and reading them through the collector's
// API means one process on this machine touches the encrypted file, not two.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	// The image has tzdata too, but a month boundary at the wrong hour would
	// put a night's spending in the wrong month, so the binary carries it.
	_ "time/tzdata"

	"github.com/choigonyok/jarvis/spending-svc/internal/api"
	"github.com/choigonyok/jarvis/spending-svc/internal/ingest"
	"github.com/choigonyok/jarvis/spending-svc/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL 이 없습니다. 이 서비스는 Postgres 없이 할 수 있는 일이 없습니다.")
		os.Exit(1)
	}
	addr := getenv("LISTEN_ADDR", ":8095")
	token := os.Getenv("API_TOKEN")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openWithRetry(ctx, dsn, 30, time.Second, log)
	if err != nil {
		log.Error("데이터베이스에 연결하지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// 키워드를 고치면 지난 내역에도 적용되도록, 추측으로 정한 분류는 부팅 때 다시 매긴다.
	if n, err := st.ReclassifyRules(ctx); err != nil {
		log.Warn("분류를 다시 매기지 못했습니다", "err", err)
	} else if n > 0 {
		log.Info("분류를 다시 매겼습니다", "changed", n)
	}

	in := ingest.New(
		getenv("KAKAOTALK_URL", "http://localhost:8090"),
		token,
		strings.Split(os.Getenv("SPENDING_EXTRA_CHANNELS"), ","),
		st,
		duration("SPENDING_POLL_INTERVAL", 3*time.Second),
		// 카톡은 하루에 몇 번은 뭔가 온다. 그보다 오래 아무것도 없으면 맥의
		// 카카오톡이 꺼졌을 가능성이 높고, 그동안의 결제 알림도 들어오지 않는다.
		duration("SPENDING_STALE_AFTER", 12*time.Hour),
		log,
	)
	go in.Run(ctx)

	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(st, in, token, log).Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		log.Info("spending-svc 가 듣기 시작했습니다", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 서버", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("spending-svc 를 내립니다")
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
