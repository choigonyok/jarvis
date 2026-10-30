// Command mind-svc is jarvis rebuilt as one loop: sense → observation log →
// interpret → world model → attention and skills → the gate → act, and back.
//
// Two ways to run it:
//
//	go run .                 everything in memory: a console at :8102 with a
//	                         chat simulator, no database, no real calendar
//	DATABASE_URL=... go run  the stack: Postgres (migration 035), the record stream
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
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/seal"
	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/api"
	"github.com/choigonyok/jarvis/mind-svc/internal/backtest"
	"github.com/choigonyok/jarvis/mind-svc/internal/checklist"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/engine"
	"github.com/choigonyok/jarvis/mind-svc/internal/eval"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/hand"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/ledger"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/sensor"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

const (
	// effectsKeep is how long an effect stays once everyone has taken it.
	effectsKeep = 30 * 24 * time.Hour
	// commandsKeep is how long what came of a command stays to be read. The
	// one who asked reads it at once; the record of it stays in the stream.
	commandsKeep = 7 * 24 * time.Hour
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
	if len(os.Args) > 1 && os.Args[1] == "backtest" {
		os.Exit(runBacktest(os.Args[2:], loc))
	}

	cfg := interpret.Config{
		Owner:    getenv("MIND_OWNER_NAME", "나"),
		Idle:     duration("MIND_IDLE", 3*time.Minute),
		IdleSelf: duration("MIND_IDLE_SELF", 45*time.Second),
		IdleSim:  duration("MIND_IDLE_SIM", 4*time.Second),
		Noise:    regexp.MustCompile(getenv("MIND_EXCLUDE_ROOMS", `카드|은행|뱅크|페이|증권|알림|광고|쿠폰|배송|택배|채널`)),
		Search:   atoiOr(os.Getenv("MIND_WEB_SEARCH"), 2),
	}
	token := os.Getenv("API_TOKEN")
	dsn := os.Getenv("DATABASE_URL")

	var (
		st       store.Store
		logOut   obs.Log
		memLog   *obs.MemoryLog
		stream   *obs.Stream
		calendar act.Calendar = act.NewMemoryCalendar()
		notifier act.Notifier = &act.MemoryNotifier{}
		clk                   = clock.New()
		movable               = clk
		mode                  = "memory"
		settle                = duration("MIND_SETTLE", 3*time.Second)
	)
	// What is said at a moment waits here, and the operator's checklists;
	// in memory on a laptop.
	var reminders moments.Store = moments.NewMemory()
	var lists checklist.Store = checklist.NewMemory()
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
		pcfg.MaxConns = 8
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
		reminders = moments.NewPostgres(pool)
		lists = checklist.NewPostgres(pool)
		// Quiet: the skills and the gate run and the console shows what they
		// would say, but no notice reaches a phone - the agent's cards still
		// do that, until the two gates are one.
		quiet := os.Getenv("MIND_QUIET") != "false"
		if url := os.Getenv("INPUT_URL"); url != "" {
			stream = obs.NewStream(url, token, pool, append(append([]string{}, mind.Kinds...), mind.GuestKinds...))
			logOut = stream
			if !quiet {
				notifier = nil // through the log, to output-svc
			}
		} else {
			memLog = obs.NewMemoryLog(log)
			logOut = memLog
		}
		if getenv("MIND_CALENDAR", "memory") == "http" {
			calendar = act.HTTPCalendar{URL: getenv("CALENDAR_URL", "http://calendar:8093"), Token: token, HTTP: handed(stream, st)}
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
	// The guest's own graph, read for the guest's taste only.
	var guestMem memory.Client
	if url := os.Getenv("MIND_MEMORY_URL"); url != "" {
		owner := memory.NewHTTP(url, token)
		mem, guestMem = owner, owner.For("guest")
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
		orders = act.AssetsOrders{URL: url, Token: token, HTTP: handed(stream, st)}
	}
	// Travel: how long the usual ways take, from the stays (context-svc);
	// here: the regions the operator is in now, for a reminder that counts
	// being home already as arriving. Both as context-svc last said.
	var travel mind.Travel
	var here, guestHere func(context.Context) ([]moments.Visit, error)
	if mode == "stack" {
		travel = mind.TravelLatest{Store: st}
		latest := mind.HereLatest{Store: st}
		here, guestHere = latest.Visits, latest.GuestVisits
	}
	// Jobs: a cart job goes to jobs-svc (the agent's browser runs it).
	var jobs act.Jobs
	if url := os.Getenv("JOBS_URL"); mode == "stack" && url != "" {
		jobs = act.JobsSvc{URL: url, Token: token, HTTP: handed(stream, st)}
	}
	// Photos: albums go to Immich when it is there.
	var photos act.Photos
	if url, key := os.Getenv("IMMICH_URL"), os.Getenv("IMMICH_API_KEY"); mode == "stack" && url != "" && key != "" {
		photos = act.Immich{URL: url, Key: key, Public: os.Getenv("IMMICH_PUBLIC_URL"), HTTP: handed(stream, st)}
	}

	// Weather: as context-svc last said it. Home is the 집 region made on the
	// phone, else MIND_WEATHER_HOME, else 서울.
	var sky *weather.Service
	if os.Getenv("MIND_WEATHER") != "false" {
		fixed, err := mind.ParsePoint(getenv("MIND_WEATHER_HOME", seoul))
		if err != nil {
			log.Warn("MIND_WEATHER_HOME 을 읽지 못해 서울로 둡니다", "err", err)
			fixed, _ = mind.ParsePoint(seoul)
		}
		home := &mind.Home{Fixed: fixed}
		if mode == "stack" {
			home.Store = st
		}
		sky = &weather.Service{Home: home.Point, Source: mind.LatestWeather{Store: st}}
	}

	var book *act.Spending
	if url := os.Getenv("SPENDING_URL"); url != "" && mode == "stack" {
		book = &act.Spending{URL: url, Token: token, HTTP: handed(stream, st)}
	}
	var emitTimer func(context.Context, model.Timer) error
	if stream != nil {
		emitTimer = mind.EmitTimer(stream)
	}
	m := mind.New(mind.Options{
		Store: st, Log: logOut, LLM: client, Calendar: calendar, Notifier: notifier, Spending: book, EmitTimer: emitTimer,
		Loc: loc, Now: clk.Now, Logger: log, Interpret: cfg, Settle: settle,
		Policy: gate.DefaultPolicy(), URL: getenv("MIND_UI_URL", "http://localhost:8102/"), Mode: mode,
		MessageRetain: time.Duration(atoiOr(getenv("MIND_MESSAGE_RETAIN_DAYS", "365"), 365)) * 24 * time.Hour,
		Memory:        mem, GuestMemory: guestMem, Photos: photos, Jobs: jobs, Assets: assets, Orders: orders, Travel: travel,
		Weather: sky, DailyUSD: number("MIND_DAILY_USD", 1.0), Reminders: reminders, Here: here, GuestHere: guestHere, Checklists: lists,
	})
	if !client.Ready() {
		log.Warn("LLM 키(MIND_ANTHROPIC_API_KEY)가 없어 대화를 해석하지 않습니다. 메시지는 쌓아 둡니다.")
	}
	// The guest's taste, read once now so the guest's first turn has it.
	m.GuestPreferences()
	if memLog != nil {
		go memLog.Run(ctx, m.Handle)
	}
	mindAPI := api.New(m, token, os.Getenv("MIND_CONSOLE_TOKEN"), mode == "memory", movable, log)
	// 손님 에이전트의 토큰: 손님 자신의 리마인드·체크리스트만 연다.
	mindAPI.SetGuestToken(os.Getenv("API_GUEST_TOKEN"))
	if stream != nil {
		mindAPI.SetStream(stream)
	}
	handler := mindAPI.Handler()
	var structured *sensor.Structured
	var messages *sensor.Messages
	if mode == "stack" {
		key, err := contentKey()
		if err != nil {
			log.Error("MIND_CONTENT_KEY 를 읽지 못했습니다", "err", err)
			os.Exit(1)
		}
		if key != nil {
			sealer, err := seal.New(key)
			if err != nil {
				log.Error("MIND_CONTENT_KEY 를 쓰지 못했습니다", "err", err)
				os.Exit(1)
			}
			messages = &sensor.Messages{Store: st, Log: logOut, Seal: sealer, Exclude: cfg.Noise,
				Payments: regexp.MustCompile(getenv("MIND_PAYMENT_ROOMS", `^(카카오페이|토스)$`)), Occur: m}
		}
		// Structured sources, read by rule into occurrences: spending, workouts,
		// photos, jobs. The calendar (whose entries also become plans), the
		// phones and the accounts come to the lane below as records.
		structured = &sensor.Structured{Store: st, Observer: m, World: m.World, Token: token, Loc: loc, Out: log, Now: time.Now,
			SpendingURL: os.Getenv("SPENDING_URL"), WorkoutURL: os.Getenv("WORKOUT_URL"),
			JobsURL: os.Getenv("JOBS_URL")}
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
			go s.Run(ctx)
		}
	}
	if stream != nil {
		take := func(ctx context.Context, msg queue.Message) error {
			o, err := obs.FromMessage(msg)
			if err != nil {
				return fmt.Errorf("%w: %v", queue.ErrRefused, err)
			}
			return m.Handle(ctx, o)
		}
		// The lanes. What a read of a conversation may hold up is its own
		// lane: answers, reminders and timers go past it (the deterministic
		// lane), and so does wording on its way out (the relay).
		command := func(ctx context.Context, msg queue.Message) error {
			switch {
			case msg.Kind == obs.TimerFired:
				return m.TimerFired(ctx, msg)
			case msg.Kind == "payment.requested":
				return m.PaymentRaised(ctx, msg)
			}
			if err := mindAPI.Command(ctx, msg); err != nil {
				return err
			}
			if msg.Kind == "intent.answer" {
				return m.HeldCard(ctx, msg)
			}
			return nil
		}
		adapters := func(ctx context.Context, msg queue.Message) error {
			if msg.Kind != sensor.KindMessage {
				return structured.Take(ctx, msg)
			}
			if messages == nil {
				return fmt.Errorf("%w: 봉인을 열 키가 없습니다", queue.ErrOutage)
			}
			return messages.Take(ctx, msg)
		}
		// Their own connections: reads for the screens never wait on them.
		lanes, err := pgxpool.New(ctx, dsn)
		if err != nil {
			log.Error("레인의 데이터베이스 풀을 만들지 못했습니다", "err", err)
			os.Exit(1)
		}
		defer lanes.Close()
		for _, c := range []*queue.Consumer{
			{Pool: lanes, Topic: queue.Records, Name: "mind", Kinds: mind.Kinds, Handle: take, Log: log},
			{Pool: lanes, Topic: queue.Guest, Name: "mind-guest", Kinds: mind.GuestKinds, Handle: take, Log: log},
			{Pool: lanes, Topic: queue.Records, Name: "mind-commands", Kinds: mind.CommandKinds, Handle: command, Log: log},
			{Pool: lanes, Topic: queue.Records, Name: "mind-results", Kinds: []string{hand.KindResult}, Handle: func(ctx context.Context, msg queue.Message) error { return hand.Result(ctx, st, msg) }, Log: log},
			{Pool: lanes, Topic: queue.Records, Name: "mind-structured", Kinds: sensor.Kinds, Handle: adapters, Log: log},
			{Pool: lanes, Topic: queue.Guest, Name: "mind-commands-guest", Kinds: mind.GuestCommandKinds, Handle: command, Log: log},
			{Pool: lanes, Topic: queue.Records, Name: "mind-relay", Kinds: []string{"*"}, Handle: stream.Relay(queue.Effects), Log: log},
			{Pool: lanes, Topic: queue.Guest, Name: "mind-relay-guest", Kinds: []string{"*"}, Handle: stream.Relay(queue.GuestEffects), Log: log},
		} {
			go c.Run(ctx)
		}
		go func() {
			for {
				if n, err := stream.PruneEffects(ctx, effectsKeep); err != nil && ctx.Err() == nil {
					log.Warn("지난 효과를 지우지 못했습니다", "err", err)
				} else if n > 0 {
					log.Info("지난 효과를 지웠습니다", "count", n)
				}
				if err := st.PruneCommands(ctx, time.Now().Add(-commandsKeep)); err != nil && ctx.Err() == nil {
					log.Warn("지난 명령 결과를 지우지 못했습니다", "err", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(24 * time.Hour):
				}
			}
		}()
	}
	m.Run(ctx, time.Second)
	if sky != nil {
		// Read once at the start, so a home that cannot be found or a
		// forecast that cannot be reached shows on the status screen now
		// and not only at the next morning's brief.
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Second):
			}
			now := time.Now().In(loc)
			if _, err := sky.HomeDays(ctx, now.Format("2006-01-02"), now.AddDate(0, 0, 1).Format("2006-01-02")); err != nil {
				log.Warn("날씨를 읽지 못했습니다", "err", err)
			}
		}()
	}

	addr := getenv("LISTEN_ADDR", ":8102")
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
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

// seoul is home for the weather when nothing says otherwise: City Hall.
const seoul = "37.5665,126.9780,서울"

// runBacktest scores exported judgements: a file of cases (scripts/harness
// cases) or standard input, or with --dump a dump of a running system, whose
// predictions are scored again from what happened (scripts/harness dump).
// --at tries thresholds on them.
func runBacktest(args []string, loc *time.Location) int {
	fs := flag.NewFlagSet("backtest", flag.ContinueOnError)
	at := fs.String("at", "", "시험할 문턱들, 쉼표로: 0.6,0.7,0.85")
	dump := fs.String("dump", "", "scripts/harness dump 가 내려받은 폴더")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var cases []backtest.Case
	if *dump != "" {
		d, err := ledger.Load(*dump)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		d.Inputs.Now, d.Inputs.Loc = time.Now(), loc
		rows := ledger.Build(d.Inputs)
		cases = ledger.Cases(rows)
		c := ledger.Count(rows)
		facts := ledger.Intervals(d.Facts, loc)
		ended, single := 0, 0
		for _, f := range facts {
			if f.To != nil {
				ended++
			}
			if f.Single {
				single++
			}
		}
		fmt.Printf("장부 %d줄 · 결과가 있는 판단 %d건 · 기다리는 것 %d건 · 무시된 카드 %d건 · 관문이 버린 것 %d건\n", c.Rows, c.Scored, c.Open, c.Ignored, c.Quiet)
		fmt.Printf("사람이 직접 한 것 %d건(그중 관문이 버렸던 것 %d건) · 실시간 채점과 달라진 예측 %d건\n", c.Manual, c.ManualQuiet, c.Rescored)
		fmt.Printf("사실 %d줄 · 하나만 맞는 사실 %d줄 · 끝난 사실 %d줄\n\n", len(facts), single, ended)
		tr := engine.Walk(rows)
		fmt.Printf("엔진 시험(앞선 답만 보고 시간순으로): %d건 · Brier 말한 대로 %.3f → 보정 %.3f (평소 비율 %.3f)\n", tr.Cases, tr.Stated, tr.Corrected, tr.Plain)
		for _, g := range []engine.Grade{engine.Silent, engine.AskBlank, engine.AskFilled, engine.Act} {
			if tr.Grades[g] > 0 {
				fmt.Printf("  %-11s %3d건, 그중 승인 %d건\n", g, tr.Grades[g], tr.Right[g])
			}
		}
		fmt.Println()
	} else {
		in := io.Reader(os.Stdin)
		if path := fs.Arg(0); path != "" {
			f, err := os.Open(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			defer f.Close()
			in = f
		}
		var err error
		if cases, err = backtest.Read(in); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	fmt.Print(backtest.Report(cases))
	for _, s := range strings.Split(*at, ",") {
		t, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			continue
		}
		c := backtest.At(cases, t)
		fmt.Printf("\n문턱 %.2f: %d건 진행, 그중 %d건 맞음(%.0f%%), 놓친 맞음 %d건", t, c.Kept, c.Hits, 100*c.Precision(), c.Missed)
	}
	fmt.Println()
	return 0
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
// handed is the client an actuator writes through: a write goes to output-svc
// as an effect, and a read as it did. Without the stream (a laptop) the
// actuator goes by itself.
func handed(stream *obs.Stream, st store.Store) *http.Client {
	if stream == nil {
		return nil
	}
	return &http.Client{Transport: &hand.Transport{Publish: stream.Effect, Store: st}, Timeout: hand.Wait + 5*time.Second}
}

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
