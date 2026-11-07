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
	"os"
	"time"

	"github.com/choigonyok/jarvis/calendar-svc/internal/api"
	"github.com/choigonyok/jarvis/calendar-svc/internal/store"
	"github.com/choigonyok/jarvis/calendar-svc/internal/uniple"
	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/notify"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/gocommon/svc"
	"github.com/choigonyok/jarvis/gocommon/svc/pg"
	"github.com/choigonyok/jarvis/gocommon/watch"
)

func main() {
	log := svc.Logger()
	token := svc.Token(log)
	dsn := svc.Need(log, "DATABASE_URL")

	ctx, stop := svc.Signals()
	defer stop()

	st, err := pg.Retry(ctx, log, func(ctx context.Context) (*store.Store, error) { return store.Open(ctx, dsn) })
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

	// 일정이 어디 사는지. 기본은 uniple(커플 앱의 공유 캘린더)이고,
	// CALENDAR_BACKEND=postgres 는 되돌릴 때를 위해 남겨 둔다. Postgres 는
	// 어느 쪽이든 필요하다 - uniple 의 회전하는 로그인 토큰을 여기에 적어 둔다.
	var backend api.Backend = st
	if svc.Env("CALENDAR_BACKEND", "uniple") == "uniple" {
		u, err := uniple.Open(ctx, dsn, uniple.Config{
			BaseURL:     svc.Env("UNIPLE_URL", "https://api.uniple.app"),
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

	// What is on the calendar goes out when it changes: the next weeks
	// often, a year each way (what long-term memory keeps) less so.
	loops := health.NewLoops()
	records := record.New(dsn, loops, log)
	go records.Run(ctx)
	(&watch.Watcher{Service: "calendar", Records: records, Loops: loops, Log: log}).Run(ctx,
		watch.Source{Kind: "calendar.near", Label: "가까운 일정 읽기", Every: 2 * time.Minute, Read: span(backend, 0, 7, 2)},
		watch.Source{Kind: "calendar.year", Label: "앞뒤 1년 일정 읽기", Every: time.Hour, Read: span(backend, 1, 0, 12)},
	)

	if svc.Serve(ctx, svc.Server{
		Name: "calendar-svc", Addr: svc.Env("LISTEN_ADDR", ":8093"), Log: log,
		Handler: api.New(backend, token, os.Getenv("API_GUEST_TOKEN"), log, notify.New(os.Getenv("NOTIFY_URL"), dsn, token, log)).WithLoops(loops).Handler(),
		Read:    15 * time.Second,
		Write:   30 * time.Second,
	}) != nil {
		os.Exit(1)
	}
}

// span reads the calendar from years and days back to months ahead.
func span(b api.Backend, years, days, months int) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		now := time.Now()
		from, to := now.AddDate(-years, 0, -days).Format("2006-01-02"), now.AddDate(0, months, 0).Format("2006-01-02")
		events, err := b.Range(ctx, from, to)
		if err != nil {
			return nil, err
		}
		return map[string]any{"from": from, "to": to, "events": events}, nil
	}
}
