// Command mind-svc is jarvis rebuilt as one loop: sense → observation log →
// interpret → world model → attention and skills → the gate → act, and back.
//
// Two ways to run it:
//
//	go run .                 everything in memory: a console at :8102 with a
//	                         chat simulator, no database, no real calendar
//	DATABASE_URL=... go run  the stack: Postgres (migration 035), events-svc
//	                         as the log, the chat collectors as sensors
//
// and one way to check the model's reading against written-down scenarios:
//
//	go run . eval            runs internal/eval/scenarios with the real model
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/api"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/eval"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/sensor"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	loc, err := time.LoadLocation(getenv("TZ", "Asia/Seoul"))
	if err != nil {
		loc = time.FixedZone("KST", 9*60*60)
	}
	client := llm.NewAnthropic(firstOf(os.Getenv("MIND_ANTHROPIC_API_KEY"), os.Getenv("ANTHROPIC_API_KEY")), os.Getenv("MIND_MODEL"), number("MIND_DAILY_USD", 1.0))

	if len(os.Args) > 1 && os.Args[1] == "eval" {
		os.Exit(runEval(ctx, client, loc))
	}

	cfg := interpret.Config{
		Owner:    getenv("MIND_OWNER_NAME", "나"),
		Idle:     duration("MIND_IDLE", 3*time.Minute),
		IdleSelf: duration("MIND_IDLE_SELF", 45*time.Second),
		IdleSim:  duration("MIND_IDLE_SIM", 4*time.Second),
		Noise:    regexp.MustCompile(getenv("MIND_EXCLUDE_ROOMS", `카드|은행|뱅크|페이|증권|알림|광고|쿠폰|배송|택배|채널`)),
	}
	token := os.Getenv("API_TOKEN")
	dsn := os.Getenv("DATABASE_URL")

	var (
		st       store.Store
		logOut   obs.Log
		memLog   *obs.MemoryLog
		events   *obs.EventsLog
		calendar act.Calendar = act.NewMemoryCalendar()
		notifier act.Notifier = &act.MemoryNotifier{}
		clk                   = clock.New()
		movable               = clk
		mode                  = "memory"
		settle                = duration("MIND_SETTLE", 3*time.Second)
	)
	if dsn == "" {
		st = store.NewMemory()
		memLog = obs.NewMemoryLog(log)
		logOut = memLog
		log.Info("메모리 모드: 데이터베이스·실제 캘린더 없이 돕니다")
	} else {
		mode, movable = "stack", nil
		if token == "" && os.Getenv("API_TOKEN_OPTIONAL") != "true" {
			log.Error("API_TOKEN 이 비어 있습니다. 스택에서는 관측 로그와 콘솔을 지키는 토큰이 필요합니다")
			os.Exit(1)
		}
		settle = duration("MIND_SETTLE", time.Minute)
		pcfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			log.Error("DATABASE_URL 을 해석하지 못했습니다", "err", err)
			os.Exit(1)
		}
		pcfg.MaxConns = 6
		pool, err := pgxpool.NewWithConfig(ctx, pcfg)
		if err != nil {
			log.Error("데이터베이스 풀을 만들지 못했습니다", "err", err)
			os.Exit(1)
		}
		defer pool.Close()
		key, err := contentKey()
		if err != nil {
			log.Error("MIND_CONTENT_KEY 를 읽지 못했습니다", "err", err)
			os.Exit(1)
		}
		// 스택에서는 메시지 본문을 평문으로 두지 않는다(S-10): 디스크 암호화 없이 도는 맥미니에서는
		// 이 봉인이 대화 원문의 보호다. 버리는 로컬 스택만 MIND_ALLOW_PLAINTEXT=true 로 연다.
		if key == nil && os.Getenv("MIND_ALLOW_PLAINTEXT") != "true" {
			log.Error("MIND_CONTENT_KEY 가 비어 있습니다. 메시지 본문을 봉인할 키가 필요합니다 (openssl rand -base64 32)")
			os.Exit(1)
		}
		st = store.NewPostgres(pool, key)
		// Quiet: the skills and the gate run and the console shows what they
		// would say, but no notice reaches a phone - the agent's cards still
		// do that, until the two gates are one.
		quiet := os.Getenv("MIND_QUIET") != "false"
		if url := os.Getenv("EVENTS_URL"); url != "" {
			events = obs.NewEventsLog(url, token, pool)
			logOut = events
			if !quiet {
				notifier = nil // through the log, to notify-svc
			}
		} else {
			memLog = obs.NewMemoryLog(log)
			logOut = memLog
		}
		if getenv("MIND_CALENDAR", "memory") == "http" {
			calendar = act.HTTPCalendar{URL: getenv("CALENDAR_URL", "http://calendar:8093"), Token: token}
			log.Warn("실제 캘린더(calendar-svc)에 씁니다")
		}
	}

	// The books: every model call's cost by day and purpose, kept so the
	// daily budget holds across a restart and the header can show it.
	today := time.Now().In(loc).Format("2006-01-02")
	if us, err := st.Usages(ctx, today); err == nil {
		spent := 0.0
		for _, u := range us {
			spent += u.USD
		}
		client.Seed(spent)
	}
	client.OnUse = func(u llm.Use) {
		_ = st.AddUsage(context.Background(), model.Usage{Day: u.At.In(loc).Format("2006-01-02"), Purpose: u.Purpose,
			Calls: 1, USD: u.USD, In: int64(u.In), Out: int64(u.Out)})
	}

	// Long-term memory: memory-svc when there is one; on a laptop without
	// one, a stand-in in this process so the console still shows facts going
	// in and coming back judged.
	var mem memory.Client
	if url := os.Getenv("MIND_MEMORY_URL"); url != "" {
		mem = memory.NewHTTP(url, token)
	} else if mode == "memory" {
		mem = memory.NewFake(clk.Now)
	} else {
		log.Warn("MIND_MEMORY_URL 이 없어 기억할 사실은 장부에만 쌓습니다")
	}

	// Assets: the allocation, read only.
	var assets mind.Assets
	// Orders: an approved rebalance card's orders go to assets-svc, the only
	// holder of the brokerage keys.
	var orders act.Orders
	if url := os.Getenv("ASSETS_URL"); mode == "stack" && url != "" {
		assets = mind.AssetsHTTP{URL: url, Token: token}
		orders = act.AssetsOrders{URL: url, Token: token}
	}
	// Travel: how long the usual ways take, from the stays (context-svc).
	var travel mind.Travel
	if url := os.Getenv("CONTEXT_URL"); mode == "stack" && url != "" {
		travel = mind.TravelHTTP{URL: url, Token: token}
	}
	// Jobs: a cart job goes to jobs-svc (the agent's browser runs it).
	var jobs act.Jobs
	if url := os.Getenv("JOBS_URL"); mode == "stack" && url != "" {
		jobs = act.JobsSvc{URL: url, Token: token}
	}
	// Photos: albums go to Immich when it is there.
	var photos act.Photos
	if url, key := os.Getenv("IMMICH_URL"), os.Getenv("IMMICH_API_KEY"); mode == "stack" && url != "" && key != "" {
		photos = act.Immich{URL: url, Key: key, Public: os.Getenv("IMMICH_PUBLIC_URL")}
	}

	m := mind.New(mind.Options{
		Store: st, Log: logOut, LLM: client, Calendar: calendar, Notifier: notifier,
		Loc: loc, Now: clk.Now, Logger: log, Interpret: cfg, Settle: settle,
		Policy: gate.DefaultPolicy(), URL: getenv("MIND_UI_URL", "http://localhost:8102/"), Mode: mode,
		MessageRetain: time.Duration(atoiOr(getenv("MIND_MESSAGE_RETAIN_DAYS", "365"), 365)) * 24 * time.Hour,
		Memory:        mem, Photos: photos, Jobs: jobs, Assets: assets, Orders: orders, Travel: travel,
		DailyUSD: number("MIND_DAILY_USD", 1.0),
	})
	if !client.Ready() {
		log.Warn("LLM 키(MIND_ANTHROPIC_API_KEY)가 없어 대화를 해석하지 않습니다. 메시지는 쌓아 둡니다.")
	}
	if memLog != nil {
		go memLog.Run(ctx, m.Handle)
	}
	if events != nil {
		go events.Subscribe(ctx, "mind", getenv("MIND_SELF_URL", "http://mind:8102/consume"), []string{obs.MessageObserved, "action.*", mind.AssetsBand}, log)
	}
	if mode == "stack" {
		exclude := cfg.Noise
		history := &sensor.History{Store: st, Reader: m.Conv, Noise: exclude, Out: log,
			Batch: int(number("MIND_HISTORY_BATCH", 40)), Wait: 30 * time.Minute}
		for name, url := range map[string]string{"kakao": os.Getenv("KAKAOTALK_URL"), "imessage": os.Getenv("IMESSAGE_URL")} {
			if url == "" {
				continue
			}
			s := &sensor.Messenger{Name: name, URL: url, Token: token, Store: st, Log: logOut, Exclude: exclude,
				Backfill: duration("MIND_BACKFILL", 6*time.Hour), Every: duration("MIND_POLL", 5*time.Second), Out: log,
				Payments: regexp.MustCompile(getenv("MIND_PAYMENT_ROOMS", `^(카카오페이|토스)$`)), Occur: m}
			history.Messengers = append(history.Messengers, s)
			go s.Run(ctx)
		}
		// Structured sources, read by rule into occurrences: spending, workouts,
		// the calendar (whose entries also become plans), photos, assets, jobs.
		structured := &sensor.Structured{Store: st, Observer: m, World: m.World, Token: token, Loc: loc, Out: log,
			SpendingURL: os.Getenv("SPENDING_URL"), WorkoutURL: os.Getenv("WORKOUT_URL"), CalendarURL: os.Getenv("MIND_CALENDAR_READ_URL"),
			ImmichURL: os.Getenv("IMMICH_URL"), ImmichKey: os.Getenv("IMMICH_API_KEY"), ContextURL: os.Getenv("CONTEXT_URL"),
			AssetsURL: os.Getenv("ASSETS_URL"), JobsURL: os.Getenv("JOBS_URL"), PhonenoteURL: os.Getenv("PHONENOTE_URL")}
		// One reader for each source: memory remembers what is read here
		// (memory-svc's own collectors for these sources are then off -
		// MEMORY_SOURCES).
		if h, ok := mem.(*memory.HTTP); ok && os.Getenv("MIND_RECORDS") != "false" {
			structured.Records = h
		}
		go structured.Run(ctx, duration("MIND_STRUCTURED_POLL", 2*time.Minute))
		if url := os.Getenv("CHAT_URL"); url != "" {
			s := &sensor.Chat{URL: url, Token: token, Store: st, Log: logOut,
				Backfill: duration("MIND_BACKFILL", 6*time.Hour), Every: duration("MIND_CHAT_POLL", 20*time.Second), Out: log}
			history.Chat = s
			go s.Run(ctx)
		}
		// The past the sensors skipped on their first start, read once for
		// facts. After a minute, so a first start's sensors have drawn the
		// line between past and present.
		if os.Getenv("MIND_HISTORY") == "true" && client.Ready() {
			go func() {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Minute):
				}
				if err := history.Run(ctx); err != nil && ctx.Err() == nil {
					log.Warn("지난 대화를 읽지 못했습니다", "err", err)
				}
			}()
		}
	}
	m.Run(ctx, time.Second)

	addr := getenv("LISTEN_ADDR", ":8102")
	srv := &http.Server{
		Addr:         addr,
		Handler:      api.New(m, token, mode == "memory", movable, log).Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 3 * time.Minute, // a replay runs the model
	}
	go func() {
		log.Info("mind-svc 가 듣기 시작했습니다", "addr", addr, "mode", mode, "model", client.Model())
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

// runEval reads every scenario with the real model and reports.
func runEval(ctx context.Context, client llm.Client, loc *time.Location) int {
	if !client.Ready() {
		fmt.Fprintln(os.Stderr, "MIND_ANTHROPIC_API_KEY 가 필요합니다")
		return 2
	}
	results, err := eval.RunAll(ctx, client, loc)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	failed := 0
	for _, r := range results {
		mark := "통과"
		if !r.Pass() {
			mark, failed = "실패", failed+1
		}
		fmt.Printf("%s  %s\n", mark, r.Name)
		for _, f := range r.Failures {
			fmt.Printf("      - %s\n", f)
		}
	}
	fmt.Printf("\n%s\n(Brier 는 낮을수록 좋음, 늘 50%%면 0.25)", eval.Summary(results))
	fmt.Printf("\n%d개 중 %d개 통과 · 이번 실행 비용 약 $%.4f\n", len(results), len(results)-failed, client.Spent())
	if failed > 0 {
		return 1
	}
	return 0
}

// contentKey is the AES-256 key message text is sealed with in Postgres.
// Without one the text is stored as it is.
func contentKey() ([]byte, error) {
	v := os.Getenv("MIND_CONTENT_KEY")
	if v == "" {
		return nil, nil
	}
	k, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(k) != 32 {
		return nil, errors.New("base64 로 된 32바이트여야 합니다 (openssl rand -base64 32)")
	}
	return k, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func duration(key string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil && d >= 0 {
		return d
	}
	return fallback
}

func number(key string, fallback float64) float64 {
	if f, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return f
	}
	return fallback
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
