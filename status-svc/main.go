// Command status-svc answers "is anything broken?" for the whole stack. It only
// reads, and has no code of its own for any feature: each service's /health, how
// far each consumer has read, when each source last had news against what the
// registry expects of it, and what a service said of something only it can see.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
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
	cfg := probes.Config{
		Token:       token,
		KakaoURL:    getenv("KAKAOTALK_URL", "http://localhost:8090"),
		SpendingURL: getenv("SPENDING_URL", "http://localhost:8102/api/spending"),
		AssetsURL:   getenv("ASSETS_URL", "http://localhost:8092"),
		CalendarURL: getenv("CALENDAR_URL", "http://localhost:8093"),
		AgentURL:    getenv("AGENT_URL", "http://localhost:8080"),
		MindURL:     getenv("MIND_URL", "http://localhost:8102"),
		MemoryURL:   getenv("MEMORY_URL", "http://localhost:8100"),
		ContextURL:  getenv("CONTEXT_URL", "http://localhost:8103"),
		VaultURL:    getenv("VAULT_URL", "http://localhost:8105"),
		InputURL:    getenv("INPUT_URL", "http://localhost:8101"),
		// NOTIFY_URL is where this service's own alerts go (input-svc).
		NotifyURL:     getenv("NOTIFY_SVC_URL", "http://localhost:8097"),
		PhotosURL:     getenv("PHOTOS_URL", "http://localhost:8107"),
		JobsURL:       getenv("JOBS_URL", "http://localhost:8102/api/jobs"),
		IMessageURL:   getenv("IMESSAGE_URL", "http://localhost:8099"),
		SearchURL:     getenv("SEARCH_URL", "http://localhost:8104"),
		AgentGuestURL: getenv("AGENT_GUEST_URL", "http://localhost:8081"),
		ImmichURL:     getenv("IMMICH_URL", "http://localhost:2283"),
		TTSURL:        getenv("TTS_URL", "http://localhost:8106"),
		OCRURL:        getenv("OCR_URL", "http://localhost:8107"),
		FalkorAddr:    getenv("FALKORDB_ADDR", "localhost:6379"),
		BrowserAddr:   getenv("BROWSER_ADDR", "localhost:8931"),
		DB:            pool,
	}
	runner := check.NewRunner(probes.Groups, probes.All(cfg), log)
	stack := probes.NewStack(cfg)
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			if rows, ok := stack.Rows(ctx); ok {
				runner.Sync(rows)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	// A probe going wrong is the one kind of news nobody would otherwise
	// notice: the page looks fine while payments quietly stop arriving.
	// Broken now is a push, a warning waits for the evening digest, and
	// recovering is only noted. A first verdict that is broken counts too, but
	// once a day at most - a restart is not news.
	if notifier := notify.New(os.Getenv("NOTIFY_URL"), os.Getenv("DATABASE_URL"), token, log); notifier != nil {
		// told is which probes the operator was told are broken: only those
		// are told mended. A blip that never rang is not news either way -
		// a fortnight of "다시 정상" lines in the inbox said nothing
		// (2026-10-27: 149 of them).
		var mu sync.Mutex
		told := map[string]bool{}
		runner.OnChange(func(p check.Probe, prev check.State, v check.Verdict) {
			day := time.Now().Format("2006-01-02")
			// A tap opens the check itself, not the top of the page.
			at := "/status?focus=" + url.QueryEscape("check:"+p.ID)
			switch {
			case v.State == check.Fail:
				// Only if it stays broken, on a fresh look: a deploy restarts
				// every service and trips the probes for a minute, and the
				// home network drops out for a few - neither is news. The
				// verdict of five minutes ago is not asked; the probe is.
				ev := notify.Event{Source: "status", Kind: "status.fail", Tier: "now", Level: "alert",
					Title: p.Name + " 이상", Body: v.Summary, URL: at, Key: "status:" + p.ID + ":fail:" + day}
				time.AfterFunc(failHold, func() {
					if runner.Confirm(ctx, p.ID) != check.Fail {
						return
					}
					mu.Lock()
					told[p.ID] = true
					mu.Unlock()
					notifier.Send(ev)
				})
			case v.State == check.Warn && prev != check.Unknown:
				mu.Lock()
				told[p.ID] = true
				mu.Unlock()
				notifier.Send(notify.Event{Source: "status", Kind: "status.warn", Tier: "digest", Level: "warn",
					Title: p.Name + " 주의", Body: v.Summary, URL: at, Key: "status:" + p.ID + ":warn:" + day})
			case v.State == check.OK && (prev == check.Fail || prev == check.Warn):
				mu.Lock()
				was := told[p.ID]
				delete(told, p.ID)
				mu.Unlock()
				if !was {
					return
				}
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
