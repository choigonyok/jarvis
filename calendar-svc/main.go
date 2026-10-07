// Command calendar-svc owns the calendar's storage.
//
// The agent still owns everything about *deciding*: the approval card, the MCP
// tool schema the model sees, and the declaration of which actions are
// reversible. Those are properties of the conversation and the gate, and moving
// them out here would have split one decision across two processes.
//
// What moved is the data. The agent's calendar module now talks to this over
// HTTP instead of to a JSON file, and its Actuator/ContextSource interfaces did
// not change.
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

	"github.com/choigonyok/jarvis/calendar-svc/internal/api"
	"github.com/choigonyok/jarvis/calendar-svc/internal/store"
	"github.com/choigonyok/jarvis/calendar-svc/internal/uniple"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL 이 없습니다.")
		os.Exit(1)
	}
	addr := getenv("LISTEN_ADDR", ":8093")
	token := os.Getenv("API_TOKEN")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openWithRetry(ctx, dsn, 30, time.Second, log)
	if err != nil {
		log.Error("데이터베이스에 연결하지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// 재시작 후 발급하는 id 가 같은 밀리초에 저장된 기존 id 와 부딪히지 않게
	// 순번을 이어받는다.
	if err := st.NextID(ctx); err != nil {
		log.Warn("id 순번을 이어받지 못했습니다", "err", err)
	}

	// 일정이 어디 사는지. 기본은 Postgres, uniple 이면 커플 앱의 공유
	// 캘린더다. Postgres 는 어느 쪽이든 필요하다 - uniple 의 회전하는 로그인
	// 토큰을 여기에 적어 둔다.
	var backend api.Backend = st
	if getenv("CALENDAR_BACKEND", "postgres") == "uniple" {
		u, err := uniple.Open(ctx, dsn, uniple.Config{
			BaseURL:     getenv("UNIPLE_URL", "https://api.uniple.app"),
			AnonKey:     os.Getenv("UNIPLE_ANON_KEY"),
			SeedRefresh: os.Getenv("UNIPLE_REFRESH_TOKEN"),
			BlockID:     os.Getenv("UNIPLE_BLOCK_ID"),
		})
		if err != nil {
			log.Error("uniple 을 열지 못했습니다", "err", err)
			os.Exit(1)
		}
		defer u.Close()
		backend = u
		log.Info("일정 저장소: uniple")
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(backend, token, log).Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Info("calendar-svc 가 듣기 시작했습니다", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 서버", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("calendar-svc 를 내립니다")
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
