// Command jobs-svc keeps the operator's jobs: work handed over in
// conversation that the agent carries out in the background, on whatever
// cadence the job itself decides - with what each run did, what it remembers
// for the next, what it is keeping track of, and which sites need a login.
//
// It never does the work. The agent's scheduler takes due jobs from here and
// runs them; this service is the record the 작업 tab reads.
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

	"github.com/choigonyok/jarvis/jobs-svc/internal/api"
	"github.com/choigonyok/jarvis/jobs-svc/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// An empty token would leave every route open to anything on the
	// network. Only a local trial may run that way, and must say so.
	if os.Getenv("API_TOKEN") == "" && os.Getenv("API_TOKEN_OPTIONAL") != "true" {
		log.Error("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true")
		os.Exit(1)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL 이 없습니다. 이 서비스는 Postgres 없이 할 수 있는 일이 없습니다.")
		os.Exit(1)
	}
	addr := getenv("LISTEN_ADDR", ":8098")
	token := os.Getenv("API_TOKEN")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openWithRetry(ctx, dsn, 30, time.Second, log)
	if err != nil {
		log.Error("데이터베이스에 연결하지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// 손님의 토큰(손님 에이전트와, 손님 작업 탭에서 게이트웨이가 쓰는 것)으로는 손님 작업만.
	guestToken := os.Getenv("API_GUEST_TOKEN")
	jobsAPI := api.New(st, token, guestToken,
		// 작업이 끝나고 이만큼 지나면 첨부 사진 파일을 지운다.
		duration("JOBS_PURGE_AFTER", 30*24*time.Hour),
		log)
	// 작업 탭에서 지시하거나 "지금 실행"을 누르면 그 작업 주인의 에이전트 스케줄러를
	// 바로 깨운다. 없으면(또는 닿지 않으면) 스케줄러가 자기 긴 주기에 본다.
	wakes := map[string]func(){}
	if url := os.Getenv("AGENT_WAKE_URL"); url != "" {
		wakes[store.Owner] = waker(url, token, log)
	}
	if url := os.Getenv("AGENT_GUEST_WAKE_URL"); url != "" && guestToken != "" {
		wakes[store.Guest] = waker(url, guestToken, log)
	}
	jobsAPI.SetWake(func(who string) {
		if wake := wakes[who]; wake != nil {
			wake()
		}
	})
	srv := &http.Server{
		Addr:         addr,
		Handler:      jobsAPI.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		log.Info("jobs-svc 가 듣기 시작했습니다", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 서버", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("jobs-svc 를 내립니다")
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

// waker is a knock on the agent's door: POST url with the services' token.
// Nothing rides on it - a knock that does not land leaves the job to the
// scheduler's own clock.
func waker(url, token string, log *slog.Logger) func() {
	client := &http.Client{Timeout: 5 * time.Second}
	return func() {
		req, err := http.NewRequest(http.MethodPost, url, nil)
		if err != nil {
			return
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.Do(req)
		if err != nil {
			log.Debug("에이전트를 깨우지 못했습니다", "err", err)
			return
		}
		res.Body.Close()
	}
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
