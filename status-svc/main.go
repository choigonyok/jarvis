// Command status-svc answers "is anything broken?" for the whole stack, per
// feature rather than per container: are KakaoTalk messages arriving, can the
// agent's browser still open Coupang's order history, does uniple still let
// the calendar in.
//
// It only reads. Verdicts rest on signals that already exist - a service's own
// status endpoint, a timestamp in Postgres - except where the only honest
// answer is to try: whether an order page opens is asked of the agent's
// browser, in a tab it closes again.
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

	"github.com/choigonyok/jarvis/status-svc/internal/api"
	"github.com/choigonyok/jarvis/status-svc/internal/browser"
	"github.com/choigonyok/jarvis/status-svc/internal/check"
	"github.com/choigonyok/jarvis/status-svc/internal/notify"
	"github.com/choigonyok/jarvis/status-svc/internal/probes"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The database is one of the things being watched, so failing to reach it
	// is a verdict, not a reason to exit. The pool connects lazily.
	var pool *pgxpool.Pool
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			log.Error("DATABASE_URL 을 해석하지 못했습니다", "err", err)
			os.Exit(1)
		}
		cfg.MaxConns = 3
		if pool, err = pgxpool.NewWithConfig(ctx, cfg); err != nil {
			log.Error("데이터베이스 풀을 만들지 못했습니다", "err", err)
			os.Exit(1)
		}
		defer pool.Close()
	}

	token := os.Getenv("API_TOKEN")
	runner := check.NewRunner(probes.Groups, probes.All(probes.Config{
		Token:       token,
		KakaoURL:    getenv("KAKAOTALK_URL", "http://localhost:8090"),
		SpendingURL: getenv("SPENDING_URL", "http://localhost:8095"),
		AssetsURL:   getenv("ASSETS_URL", "http://localhost:8092"),
		CalendarURL: getenv("CALENDAR_URL", "http://localhost:8093"),
		ChatURL:     getenv("CHAT_URL", "http://localhost:8094"),
		WorkoutURL:  getenv("WORKOUT_URL", "http://localhost:8091"),
		AgentURL:    getenv("AGENT_URL", "http://localhost:8080"),
		BrowserAddr: getenv("BROWSER_ADDR", "localhost:8931"),
		Browser:     browser.New(getenv("BROWSER_MCP_URL", "http://localhost:8931/"), os.Getenv("BROWSER_MCP_TOKEN")),
		DB:          pool,
	}), log)
	// A probe going wrong is the one kind of news nobody would otherwise
	// notice: the page looks fine while payments quietly stop arriving.
	// Broken now is a push, a warning waits for the evening digest, and
	// recovering is only noted. A first verdict that is broken counts too, but
	// once a day at most - a restart is not news.
	if notifier := notify.New(os.Getenv("NOTIFY_URL"), token, log); notifier != nil {
		runner.OnChange(func(p check.Probe, prev check.State, v check.Verdict) {
			day := time.Now().Format("2006-01-02")
			switch {
			case v.State == check.Fail:
				notifier.Send(notify.Event{Source: "status", Kind: "status.fail", Tier: "now", Level: "alert",
					Title: p.Name + " 이상", Body: v.Summary, URL: "/status", Key: "status:" + p.ID + ":fail:" + day})
			case v.State == check.Warn && prev != check.Unknown:
				notifier.Send(notify.Event{Source: "status", Kind: "status.warn", Tier: "digest", Level: "warn",
					Title: p.Name + " 주의", Body: v.Summary, URL: "/status", Key: "status:" + p.ID + ":warn:" + day})
			case v.State == check.OK && (prev == check.Fail || prev == check.Warn):
				notifier.Send(notify.Event{Source: "status", Kind: "status.ok", Tier: "log",
					Title: p.Name + " 다시 정상", Body: v.Summary, URL: "/status"})
			}
		})
	}
	go runner.Run(ctx)

	addr := getenv("LISTEN_ADDR", ":8096")
	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(runner, token).Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 120 * time.Second, // /refresh waits for the slow probes
	}
	go func() {
		log.Info("status-svc 가 듣기 시작했습니다", "addr", addr)
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

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
