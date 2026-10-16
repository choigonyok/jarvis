// Command vault-svc keeps the operator's logins, sealed, and hands one to the
// browser when it is on that login's own site.
//
// A login reaches it from the operator - a card in the conversation with a
// password field, whose value goes from the card straight here, never to the
// model and never into the transcript. It leaves only to the browser
// container, which types it into the page it is on; the model asks for "the
// login of this page" and is told it was filled, never what it was.
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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/vault-svc/internal/api"
	"github.com/choigonyok/jarvis/vault-svc/internal/seal"
	"github.com/choigonyok/jarvis/vault-svc/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	token, release := os.Getenv("API_TOKEN"), os.Getenv("VAULT_RELEASE_TOKEN")
	if token == "" || release == "" || token == release {
		log.Error("API_TOKEN 과 VAULT_RELEASE_TOKEN 이 둘 다 있어야 하고, 서로 달라야 합니다")
		os.Exit(1)
	}
	sealer, err := seal.FromBase64(os.Getenv("VAULT_KEY"))
	if err != nil {
		log.Error("보관함 키를 읽지 못했습니다", "err", err)
		os.Exit(1)
	}
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

	addr := getenv("LISTEN_ADDR", ":8105")
	srv := &http.Server{
		Addr:         addr,
		Handler:      (&api.Server{Store: store.New(pool), Sealer: sealer, Token: token, ReleaseToken: release, Log: log}).Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("vault-svc 가 듣기 시작했습니다", "addr", addr, "key", sealer.ID())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("서버가 멈췄습니다", "err", err)
			stop()
		}
	}()
	<-ctx.Done()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
