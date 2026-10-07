// Command chat-svc owns the transcript.
//
// What stayed in the agent is what a running process is for: the live SSE
// stream, the "is a run in flight" flag, and the Claude Code session it is
// driving. None of that survives a restart by design - a process that just
// started is not in the middle of a turn - so it has no business being in a
// database.
//
// What is here is what the operator would notice losing: what was said.
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

	"github.com/choigonyok/jarvis/chat-svc/internal/api"
	"github.com/choigonyok/jarvis/chat-svc/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL 이 없습니다.")
		os.Exit(1)
	}
	addr := getenv("LISTEN_ADDR", ":8094")
	token := os.Getenv("API_TOKEN")
	// 벽시계를 찍는 시간대. 질의마다 넘기므로 컨테이너의 TZ 에 의존하지 않는다.
	tz := getenv("TZ", "Asia/Seoul")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openWithRetry(ctx, dsn, 30, time.Second, log)
	if err != nil {
		log.Error("데이터베이스에 연결하지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := st.Resume(ctx); err != nil {
		log.Warn("id 순번을 이어받지 못했습니다", "err", err)
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(st, token, tz, log).Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Info("chat-svc 가 듣기 시작했습니다", "addr", addr, "tz", tz)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 서버", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("chat-svc 를 내립니다")
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
