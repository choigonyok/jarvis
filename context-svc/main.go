// Command context-svc knows where the operator is and in what state: the
// phone's OwnTracks app posts fixes and region transitions, iOS Shortcuts
// and the Mac collector post device states (CarPlay, focus, the app in
// front). From these it keeps stays in regions (visits) and the current
// state in one place; the raw reports are kept for a while only.
//
// It decides nothing about what to do with it. mind reads the visits as
// what happened; the agent reads the current state as a fact for the
// conversation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/choigonyok/jarvis/context-svc/internal/api"
	"github.com/choigonyok/jarvis/context-svc/internal/host"
	"github.com/choigonyok/jarvis/context-svc/internal/sky"
	"github.com/choigonyok/jarvis/context-svc/internal/store"
	"github.com/choigonyok/jarvis/gocommon/checks"
	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/notify"
	"github.com/choigonyok/jarvis/gocommon/record"
	"github.com/choigonyok/jarvis/gocommon/watch"
	"github.com/choigonyok/jarvis/gocommon/weather"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// An empty token would leave every route open to anything on the
	// network. Only a local trial may run that way, and must say so.
	if os.Getenv("API_TOKEN") == "" && os.Getenv("API_TOKEN_OPTIONAL") != "true" {
		log.Error("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true")
		os.Exit(1)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL 이 없습니다.")
		os.Exit(1)
	}
	cfg := api.Config{
		Token: os.Getenv("API_TOKEN"), SignalToken: os.Getenv("SIGNAL_TOKEN"),
		User: os.Getenv("OWNTRACKS_USER"), Pass: os.Getenv("OWNTRACKS_PASSWORD"),
		GuestUser: os.Getenv("OWNTRACKS_GUEST_USER"), GuestPass: os.Getenv("OWNTRACKS_GUEST_PASSWORD"),
		GuestToken: os.Getenv("API_GUEST_TOKEN"),
		Public:     getenv("CONTEXT_PUBLIC_URL", "https://jarvis-be.choigonyok.com/context"),
		DataKey:    os.Getenv("DATA_GO_KR_KEY"), SubwayKey: os.Getenv("SEOUL_SUBWAY_KEY"),
	}
	if cfg.User == "" || cfg.Pass == "" {
		log.Warn("OWNTRACKS_USER/OWNTRACKS_PASSWORD 가 없어 폰의 위치를 받지 않습니다")
	}
	if cfg.DataKey == "" || cfg.SubwayKey == "" {
		log.Warn("DATA_GO_KR_KEY/SEOUL_SUBWAY_KEY 가 비어 출발 탭의 실시간 버스·지하철 정보가 일부 없습니다")
	}
	days := 30
	if n, err := strconv.Atoi(os.Getenv("CONTEXT_RAW_DAYS")); err == nil && n > 0 {
		days = n
	}
	cfg.RawDays = days

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := openWithRetry(ctx, dsn, 30, time.Second, log)
	if err != nil {
		log.Error("데이터베이스에 연결하지 못했습니다", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	events := notify.New(os.Getenv("INPUT_URL"), dsn, cfg.Token, log)

	// Raw coordinates are kept for days, not forever: the visits made from
	// them are what lasts.
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if n, err := st.Purge(ctx, time.Now().AddDate(0, 0, -days)); err != nil {
				log.Warn("오래된 원본을 지우지 못했습니다", "err", err)
			} else if n > 0 {
				log.Info("오래된 원본을 지웠습니다", "rows", n, "days", days)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	addr := getenv("LISTEN_ADDR", ":8103")
	server := api.New(st, cfg, events, log)
	go server.Watch(ctx, time.Minute)
	handler := server.Handler()
	// Where the phones are, the regions and the ways taken: what mind knows
	// of them comes from here. It is the same as these routes answer.
	loops := health.NewLoops()
	server.SetLoops(loops)
	records := record.New(dsn, loops, log)
	go records.Run(ctx)
	now := watch.Local(handler, cfg.Token, "/now")
	go (&host.Watch{File: getenv("HOST_FACTS", "/host/host.json"), FileVaultOffAccepted: os.Getenv("HOST_FILEVAULT_OFF_ACCEPTED") == "true",
		Location: func(ctx context.Context) (*time.Time, string, error) {
			raw, err := now(ctx)
			if err != nil {
				return nil, "", err
			}
			body, err := json.Marshal(raw)
			if err != nil {
				return nil, "", err
			}
			var s struct {
				FixAt   *time.Time `json:"fixAt"`
				Summary string     `json:"summary"`
			}
			return s.FixAt, s.Summary, json.Unmarshal(body, &s)
		}}).Run(ctx, &checks.Reporter{Source: "context", Records: records}, loops)
	(&watch.Watcher{Service: "context", Records: records, Loops: loops, Log: log}).Run(ctx,
		watch.Source{Kind: "context.now", Label: "운영자 폰이 있는 곳", Every: 30 * time.Second, Read: watch.Local(handler, cfg.Token, "/now", "visits")},
		watch.Source{Kind: "context.now.guest", Label: "손님 폰이 있는 곳", Every: 30 * time.Second, Read: watch.Local(handler, cfg.Token, "/now/guest", "visits")},
		watch.Source{Kind: "context.visits", Label: "체류", Every: time.Minute, Read: func(ctx context.Context) (any, error) {
			return watch.Local(handler, cfg.Token, "/visits?from="+time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339), "visits")(ctx)
		}},
		watch.Source{Kind: "context.regions", Label: "영역", Every: 5 * time.Minute, Read: watch.Local(handler, cfg.Token, "/regions", "regions")},
		watch.Source{Kind: "context.travel", Label: "다닌 길", Every: time.Hour, Read: watch.Local(handler, cfg.Token, "/travel?days=60", "routes")},
	)
	// The weather: read here, and nowhere else. Home is the 집 region made on
	// the phone, else WEATHER_HOME, else 서울.
	if os.Getenv("WEATHER") != "false" {
		fixed, err := weather.ParsePoint(getenv("WEATHER_HOME", "37.5665,126.9780,서울"))
		if err != nil {
			log.Warn("WEATHER_HOME 을 읽지 못해 서울로 둡니다", "err", err)
			fixed = weather.Point{Name: "서울", Lat: 37.5665, Lon: 126.978}
		}
		forecasts := &sky.Reader{
			Source: &sky.OpenMeteo{Agent: "jarvis-context/1.0 (+https://github.com/choigonyok/jarvis)", Zone: getenv("TZ", "Asia/Seoul")},
			Fixed:  fixed, MindURL: os.Getenv("MIND_URL"), Token: cfg.Token,
			Home: func(ctx context.Context) (weather.Point, bool, error) {
				regions, err := st.Regions(ctx)
				if err != nil {
					return weather.Point{}, false, err
				}
				for _, r := range regions {
					if r.Kind == "home" {
						return weather.Point{Name: sky.HomeKey, Lat: r.Lat, Lon: r.Lon}, true, nil
					}
				}
				return weather.Point{}, false, nil
			},
		}
		(&watch.Watcher{Service: "context", Records: records, Loops: loops, Log: log}).Run(ctx,
			watch.Source{Kind: "weather.forecast", Label: "날씨 예보", Every: time.Hour, Read: forecasts.Read})
	}
	srv := &http.Server{Addr: addr, Handler: handler, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second}
	go func() {
		log.Info("context-svc 가 듣기 시작했습니다", "addr", addr)
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

func openWithRetry(ctx context.Context, dsn string, tries int, wait time.Duration, log *slog.Logger) (*store.Store, error) {
	var last error
	for i := 0; i < tries; i++ {
		st, err := store.Open(ctx, dsn)
		if err == nil {
			return st, nil
		}
		last = err
		if i == 0 {
			log.Info("데이터베이스를 기다립니다", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, last
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
