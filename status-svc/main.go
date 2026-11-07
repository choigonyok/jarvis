// Command status-svc answers "is anything broken?" for the whole stack. It only
// reads, and has no code of its own for any feature: each service's /health, how
// far each consumer has read, when each source last had news against what the
// registry expects of it, and what a service said of something only it can see.
package main

import (
	"net/url"
	"os"
	"sync"
	"time"

	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/notify"
	"github.com/choigonyok/jarvis/gocommon/svc"
	"github.com/choigonyok/jarvis/gocommon/svc/pg"
	"github.com/choigonyok/jarvis/status-svc/internal/api"
	"github.com/choigonyok/jarvis/status-svc/internal/check"
	"github.com/choigonyok/jarvis/status-svc/internal/probes"
)

func main() {
	log := svc.Logger()
	token := svc.Token(log)
	ctx, stop := svc.Signals()
	defer stop()

	// The database is one of the things being watched, so failing to reach it
	// is a verdict, not a reason to exit. The pool connects lazily.
	var pool *pgxpool.Pool
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		var err error
		if pool, err = pg.Lazy(ctx, dsn, 3); err != nil {
			log.Error("DATABASE_URL 을 해석하지 못했습니다", "err", err)
			os.Exit(1)
		}
		defer pool.Close()
	}

	cfg := probes.Config{
		Token:       token,
		KakaoURL:    svc.Env("KAKAOTALK_URL", "http://localhost:8090"),
		SpendingURL: svc.Env("SPENDING_URL", "http://localhost:8102/api/spending"),
		AssetsURL:   svc.Env("ASSETS_URL", "http://localhost:8092"),
		CalendarURL: svc.Env("CALENDAR_URL", "http://localhost:8093"),
		AgentURL:    svc.Env("AGENT_URL", "http://localhost:8080"),
		MindURL:     svc.Env("MIND_URL", "http://localhost:8102"),
		MemoryURL:   svc.Env("MEMORY_URL", "http://localhost:8100"),
		ContextURL:  svc.Env("CONTEXT_URL", "http://localhost:8103"),
		VaultURL:    svc.Env("VAULT_URL", "http://localhost:8105"),
		InputURL:    svc.Env("INPUT_URL", "http://localhost:8101"),
		// NOTIFY_URL is where this service's own alerts go (input-svc).
		NotifyURL:     svc.Env("NOTIFY_SVC_URL", "http://localhost:8097"),
		PhotosURL:     svc.Env("PHOTOS_URL", "http://localhost:8107"),
		JobsURL:       svc.Env("JOBS_URL", "http://localhost:8102/api/jobs"),
		IMessageURL:   svc.Env("IMESSAGE_URL", "http://localhost:8099"),
		SearchURL:     svc.Env("SEARCH_URL", "http://localhost:8104"),
		AgentGuestURL: svc.Env("AGENT_GUEST_URL", "http://localhost:8081"),
		ImmichURL:     svc.Env("IMMICH_URL", "http://localhost:2283"),
		TTSURL:        svc.Env("TTS_URL", "http://localhost:8106"),
		OCRURL:        svc.Env("OCR_URL", "http://localhost:8107"),
		FalkorAddr:    svc.Env("FALKORDB_ADDR", "localhost:6379"),
		BrowserAddr:   svc.Env("BROWSER_ADDR", "localhost:8931"),
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
		// a fortnight of "다시 정상" lines in the inbox said nothing.
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

	if svc.Serve(ctx, svc.Server{
		Name: "status-svc", Addr: svc.Env("LISTEN_ADDR", ":8096"), Log: log,
		Handler: api.New(runner, token).Handler(),
		Read:    10 * time.Second,
		Write:   120 * time.Second, // /refresh waits for the slow probes
	}) != nil {
		os.Exit(1)
	}
}

// failHold is how long a probe has to stay broken before it is pushed.
const failHold = 5 * time.Minute
