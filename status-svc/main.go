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
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/notify"
	"github.com/choigonyok/jarvis/status-svc/internal/api"
	"github.com/choigonyok/jarvis/status-svc/internal/check"
	"github.com/choigonyok/jarvis/status-svc/internal/probes"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// An empty token would leave every route open to anything on the
	// network. Only a local trial may run that way, and must say so.
	if os.Getenv("API_TOKEN") == "" && os.Getenv("API_TOKEN_OPTIONAL") != "true" {
		log.Error("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true")
		os.Exit(1)
	}
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
		MindURL:     getenv("MIND_URL", "http://localhost:8102"),
		MemoryURL:   getenv("MEMORY_URL", "http://localhost:8100"),
		ContextURL:  getenv("CONTEXT_URL", "http://localhost:8103"),
		VaultURL:    getenv("VAULT_URL", "http://localhost:8105"),
		EventsURL:   getenv("EVENTS_URL", "http://localhost:8101"),
		// NOTIFY_URL is where this service's own alerts go (the event log).
		NotifyURL:     getenv("NOTIFY_SVC_URL", "http://localhost:8097"),
		JobsURL:       getenv("JOBS_URL", "http://localhost:8098"),
		IMessageURL:   getenv("IMESSAGE_URL", "http://localhost:8099"),
		SearchURL:     getenv("SEARCH_URL", "http://localhost:8104"),
		AgentGuestURL: getenv("AGENT_GUEST_URL", "http://localhost:8081"),
		ImmichURL:     getenv("IMMICH_URL", "http://localhost:2283"),
		TTSURL:        getenv("TTS_URL", "http://localhost:8106"),
		OCRURL:        getenv("OCR_URL", "http://localhost:8107"),
		FalkorAddr:    getenv("FALKORDB_ADDR", "localhost:6379"),
		BrowserAddr:   getenv("BROWSER_ADDR", "localhost:8931"),
		HostFile:      getenv("HOST_FACTS", "/host/host.json"),
		// 운영자의 결정(2026-10-16): 무인 재시작을 위해 FileVault 를 끈 채로 둔다.
		FileVaultOffAccepted: os.Getenv("HOST_FILEVAULT_OFF_ACCEPTED") == "true",
		DB:                   pool,
	}), log)
	// A probe going wrong is the one kind of news nobody would otherwise
	// notice: the page looks fine while payments quietly stop arriving.
	// Broken now is a push, a warning waits for the evening digest, and
	// recovering is only noted. A first verdict that is broken counts too, but
	// once a day at most - a restart is not news.
	if notifier := notify.New(os.Getenv("NOTIFY_URL"), os.Getenv("DATABASE_URL"), token, log); notifier != nil {
		runner.OnChange(func(p check.Probe, prev check.State, v check.Verdict) {
			day := time.Now().Format("2006-01-02")
			// A tap opens the check itself, not the top of the page.
			at := "/status?focus=" + url.QueryEscape("check:"+p.ID)
			switch {
			case v.State == check.Fail:
				// Only if it stays broken: a deploy restarts every service and
				// trips the probes for a minute, which is not news.
				ev := notify.Event{Source: "status", Kind: "status.fail", Tier: "now", Level: "alert",
					Title: p.Name + " 이상", Body: v.Summary, URL: at, Key: "status:" + p.ID + ":fail:" + day}
				time.AfterFunc(failHold, func() {
					if runner.State(p.ID) == check.Fail {
						notifier.Send(ev)
					}
				})
			case v.State == check.Warn && prev != check.Unknown:
				notifier.Send(notify.Event{Source: "status", Kind: "status.warn", Tier: "digest", Level: "warn",
					Title: p.Name + " 주의", Body: v.Summary, URL: at, Key: "status:" + p.ID + ":warn:" + day})
			case v.State == check.OK && (prev == check.Fail || prev == check.Warn):
				notifier.Send(notify.Event{Source: "status", Kind: "status.ok", Tier: "log",
					Title: p.Name + " 다시 정상", Body: v.Summary, URL: at})
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

// failHold is how long a probe has to stay broken before it is pushed.
const failHold = 5 * time.Minute

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
