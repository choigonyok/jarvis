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
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/choigonyok/jarvis/calendar-svc/internal/api"
	"github.com/choigonyok/jarvis/calendar-svc/internal/holiday"
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

	// 일정과 커플 공간이 어디 사는지. 기본은 이 Postgres 의 couple 스키마(local)다.
	// uniple 은 옮겨 오기 전까지(그리고 되돌릴 때) 쓰고, postgres 는 예전 표(commitments)다.
	// Postgres 는 어느 쪽이든 필요하다 - uniple 의 회전하는 로그인 토큰도 여기에 적어 둔다.
	notifier := notify.New(os.Getenv("NOTIFY_URL"), dsn, token, log)
	uniCfg := uniple.Config{
		BaseURL:     svc.Env("UNIPLE_URL", "https://api.uniple.app"),
		AppURL:      svc.Env("UNIPLE_APP_URL", "https://uniple.app"),
		AnonKey:     os.Getenv("UNIPLE_ANON_KEY"),
		SeedRefresh: os.Getenv("UNIPLE_REFRESH_TOKEN"),
		BlockID:     os.Getenv("UNIPLE_BLOCK_ID"),
	}

	// calendar-svc import-uniple [-replace]: uniple 의 커플 공간을 로컬로 한 번 옮기고 끝난다.
	// uniple 세션은 이 프로세스와 함께 쓸 수 없으니(토큰이 쓸 때마다 회전) 서비스를 멈추고 돌린다.
	if len(os.Args) > 1 && os.Args[1] == "import-uniple" {
		os.Exit(importUniple(ctx, dsn, uniCfg, notifier, len(os.Args) > 2 && os.Args[2] == "-replace", log))
	}

	var backend api.Backend = st
	var couple api.Couple
	switch kind := svc.Env("CALENDAR_BACKEND", "local"); kind {
	case "local":
		u, err := uniple.OpenLocal(ctx, dsn, uniple.Config{BlockID: os.Getenv("COUPLE_BLOCK_ID")}, uniple.LocalConfig{Notify: notifier})
		if err != nil {
			log.Error("커플 공간을 열지 못했습니다", "err", err)
			os.Exit(1)
		}
		defer u.Close()
		backend, couple = u, u
		log.Info("일정 저장소: 이 Postgres(couple)")
	case "uniple":
		u, err := uniple.Open(ctx, dsn, uniCfg)
		if err != nil {
			log.Error("uniple 을 열지 못했습니다", "err", err)
			os.Exit(1)
		}
		defer u.Close()
		backend, couple = u, u
		log.Info("일정 저장소: uniple")
	case "postgres":
		log.Info("일정 저장소: 예전 표(commitments)")
	default:
		log.Error("CALENDAR_BACKEND 는 local, uniple, postgres 중 하나입니다", "got", kind)
		os.Exit(1)
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

	// 공휴일은 공개 피드에서 하루 한 번 읽는다. HOLIDAYS_ICS_URL=off 면 이 서비스가 가진 목록만.
	holidaysURL := svc.Env("HOLIDAYS_ICS_URL", holiday.GoogleKR)
	if holidaysURL == "off" {
		holidaysURL = ""
	}
	holidays := holiday.New(holidaysURL, log)
	go func() {
		today := time.Now().Format(time.DateOnly)
		_, _ = holidays.Between(context.WithoutCancel(ctx), today, today)
	}()

	if svc.Serve(ctx, svc.Server{
		Name: "calendar-svc", Addr: svc.Env("LISTEN_ADDR", ":8093"), Log: log,
		Handler: api.New(backend, token, os.Getenv("API_GUEST_TOKEN"), log, notifier).WithLoops(loops).WithCouple(couple).WithHolidays(holidays).Handler(),
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

// importUniple moves the couple's space from uniple into this database and
// says what it moved.
func importUniple(ctx context.Context, dsn string, cfg uniple.Config, notifier *notify.Client, replace bool, log *slog.Logger) int {
	remote, err := uniple.Open(ctx, dsn, cfg)
	if err != nil {
		log.Error("uniple 을 열지 못했습니다", "err", err)
		return 1
	}
	defer remote.Close()
	dst, err := uniple.OpenLocal(ctx, dsn, uniple.Config{}, uniple.LocalConfig{Notify: notifier})
	if err != nil {
		log.Error("로컬 커플 공간을 열지 못했습니다", "err", err)
		return 1
	}
	defer dst.Close()
	got, err := uniple.Import(ctx, remote, dst, replace, log)
	report, _ := json.MarshalIndent(got, "", "  ")
	fmt.Println(string(report))
	if err != nil {
		log.Error("옮기지 못했습니다", "err", err)
		return 1
	}
	log.Info("uniple 의 커플 공간을 옮겼습니다", "photos", got.Photos, "missing", len(got.Missing))
	return 0
}
