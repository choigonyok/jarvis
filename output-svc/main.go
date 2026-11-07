// Command output-svc is the way out: it reads what mind decided should go out
// (the effects), and out of it come the console's inbox, a push to the phone
// when someone has to act, and an evening digest of the rest.
//
// The services only say what happened and how much it matters; whether that
// becomes a push now, a push after quiet hours, a line in tonight's digest or
// just an inbox entry is decided here, against the person's settings.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	_ "time/tzdata"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/gocommon/svc"
	"github.com/choigonyok/jarvis/gocommon/svc/pg"
	"github.com/choigonyok/jarvis/output-svc/internal/api"
	"github.com/choigonyok/jarvis/output-svc/internal/calls"
	"github.com/choigonyok/jarvis/output-svc/internal/live"
	"github.com/choigonyok/jarvis/output-svc/internal/notify"
	"github.com/choigonyok/jarvis/output-svc/internal/push"
	"github.com/choigonyok/jarvis/output-svc/internal/store"
)

func main() {
	log := svc.Logger()
	token := svc.Token(log)
	dsn := svc.Need(log, "DATABASE_URL")
	ctx, stop := svc.Signals()
	defer stop()

	pool, err := pg.Open(ctx, dsn, 6, log)
	if err != nil {
		log.Error("데이터베이스에 연결하지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	loc, err := time.LoadLocation(svc.Env("TZ", "Asia/Seoul"))
	if err != nil {
		loc = time.FixedZone("KST", 9*60*60)
	}

	st := store.New(pool)
	hub := live.New()
	pusher := push.New(push.Keys{
		Public:  os.Getenv("VAPID_PUBLIC_KEY"),
		Private: os.Getenv("VAPID_PRIVATE_KEY"),
		Subject: svc.Env("VAPID_SUBJECT", "https://jarvis.choigonyok.com"),
	}, st, hub, log)
	if !pusher.Ready() {
		log.Warn("VAPID 키가 없어 푸시는 보내지 않고 알림함에만 쌓습니다")
	}
	notes := notify.New(st, pusher, loc)
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
				if err := notes.Tick(ctx); err != nil {
					log.Error("예약 알림을 보내지 못했습니다", "err", err)
					loops.Fail("tick", err)
				} else {
					loops.OK("tick")
				}
			}
		}
	}()

	server := api.New(notes, st, pusher, hub, token, log).WithLoops(loops)
	// What went out is a record too. Its text is not in it: what is in an
	// inbox stays there.
	sent := record.New(dsn, loops, log)
	go sent.Run(ctx)
	notes.Sent = func(who string, ns []store.Notification, group string) {
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
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	hand := &calls.Hand{Pool: pool, Records: sent, Log: log, HTTP: &http.Client{}, Services: map[string]calls.Service{
		"calendar": {URL: os.Getenv("CALENDAR_URL"), Auth: bearer},
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

	// The console's /stream lifts the write limit for itself (svc.Stream).
	if svc.Serve(ctx, svc.Server{Name: "output-svc", Addr: svc.Env("LISTEN_ADDR", ":8097"), Handler: server.Handler(),
		Read: 10 * time.Second, Write: 60 * time.Second, Log: log}) != nil {
		os.Exit(1)
	}
}
