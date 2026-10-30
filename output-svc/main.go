// Command output-svc is the way out: it reads what mind decided should go out
// (the effects), and out of it come the console's inbox, a push to the phone
// when someone has to act, and an evening digest of the rest.
//
// The services only say what happened and how much it matters; whether that
// becomes a push now, a push after quiet hours, a line in tonight's digest or
// just an inbox entry is decided here, against the person's settings.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/output-svc/internal/api"
	"github.com/choigonyok/jarvis/output-svc/internal/calls"
	"github.com/choigonyok/jarvis/output-svc/internal/live"
	"github.com/choigonyok/jarvis/output-svc/internal/notify"
	"github.com/choigonyok/jarvis/output-svc/internal/push"
	"github.com/choigonyok/jarvis/output-svc/internal/store"
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

	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Error("DATABASE_URL 을 해석하지 못했습니다", "err", err)
		os.Exit(1)
	}
	cfg.MaxConns = 6
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Error("데이터베이스 풀을 만들지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	loc, err := time.LoadLocation(getenv("TZ", "Asia/Seoul"))
	if err != nil {
		loc = time.FixedZone("KST", 9*60*60)
	}

	st := store.New(pool)
	hub := live.New()
	pusher := push.New(push.Keys{
		Public:  os.Getenv("VAPID_PUBLIC_KEY"),
		Private: os.Getenv("VAPID_PRIVATE_KEY"),
		Subject: getenv("VAPID_SUBJECT", "https://jarvis.choigonyok.com"),
	}, st, hub, log)
	if !pusher.Ready() {
		log.Warn("VAPID 키가 없어 푸시는 보내지 않고 알림함에만 쌓습니다")
	}
	svc := notify.New(st, pusher, loc)
	loops := health.NewLoops()
	loops.Add("tick", "미뤄 둔 알림·저녁 요약 보내기", 10*time.Minute)

	// Held pushes go out when quiet hours end; the digest at its time.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := svc.Tick(ctx); err != nil {
					log.Error("예약 알림을 보내지 못했습니다", "err", err)
					loops.Fail("tick", err)
				} else {
					loops.OK("tick")
				}
			}
		}
	}()

	server := api.New(svc, st, pusher, hub, os.Getenv("API_TOKEN"), log).WithLoops(loops)
	// What went out is a record too. Its text is not in it: what is in an
	// inbox stays there.
	sent := record.New(os.Getenv("DATABASE_URL"), loops, log)
	go sent.Run(ctx)
	svc.Sent = func(who string, ns []store.Notification, group string) {
		actor := "output"
		if who == store.Guest {
			actor = store.Guest
		}
		for _, n := range ns {
			sent.Write(record.Record{Kind: "notify.sent", Source: "output", SourceKey: fmt.Sprintf("sent:%d", n.ID), Actor: actor,
				Channel: "push", Subject: n.Kind, Tier: record.Durable,
				Payload: map[string]any{"id": n.ID, "kind": n.Kind, "tier": n.Tier, "key": n.DedupeKey, "group": group}})
		}
	}
	// The hand: what mind decided to write, carried out against the adapters'
	// fixed APIs, each once.
	token := os.Getenv("API_TOKEN")
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	hand := &calls.Hand{Pool: pool, Records: sent, Log: log, HTTP: &http.Client{}, Services: map[string]calls.Service{
		"calendar": {URL: os.Getenv("CALENDAR_URL"), Auth: bearer},
		"spending": {URL: os.Getenv("SPENDING_URL"), Auth: bearer},
		"jobs":     {URL: os.Getenv("JOBS_URL"), Auth: bearer},
		"assets":   {URL: os.Getenv("ASSETS_URL"), Auth: bearer},
		"immich":   {URL: os.Getenv("IMMICH_URL"), Auth: func(r *http.Request) { r.Header.Set("x-api-key", os.Getenv("IMMICH_API_KEY")) }},
	}}
	for _, c := range []*queue.Consumer{
		{Pool: pool, Topic: queue.Effects, Name: "output", Kinds: []string{"*"}, Handle: server.Take, Log: log},
		{Pool: pool, Topic: queue.Effects, Name: "output-calls", Kinds: []string{calls.KindCall}, Handle: hand.Take, Log: log},
		{Pool: pool, Topic: queue.GuestEffects, Name: "output-guest", Kinds: []string{"*"}, Handle: server.Take, Log: log},
		// Read from the records, past mind: that mind is down cannot be said
		// through mind, and a card a payment waits on does not wait for a
		// redeploy. Passing these on is not a judgement (the design's list
		// of exceptions).
		{Pool: pool, Topic: queue.Records, Name: "output-status", Kinds: []string{"status.*", "approval.*"}, Handle: server.Take, Log: log},
	} {
		go c.Run(ctx)
	}

	addr := getenv("LISTEN_ADDR", ":8097")
	srv := &http.Server{
		Addr:         addr,
		Handler:      server.Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	go func() {
		log.Info("output-svc 가 듣기 시작했습니다", "addr", addr)
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

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
