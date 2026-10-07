package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// The IANA database, embedded. The runtime image has no tzdata, so
	// without this a TZ like Asia/Seoul would silently resolve to UTC - and
	// every "오늘" the agent computes would be a day off after midnight.
	_ "time/tzdata"

	"github.com/choigonyok/jarvis/agent/internal/adapter/permission"
	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/httpapi"
	"github.com/choigonyok/jarvis/agent/internal/module/assets"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/module/market"
	"github.com/choigonyok/jarvis/agent/internal/module/spending"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		log.Error("설정을 읽지 못했습니다", "err", err)
		os.Exit(1)
	}

	// One bus, three producers, one stream out. A proposal raised with no
	// conversation behind it reaches the browser the same way a chat turn does.
	events := bus.New()
	// The guest agent keeps its own conversation; it never reads the operator's.
	transcript := thread.NewStore(cfg.ChatURL, cfg.APIToken, events).ForThread(cfg.Thread)
	proposals := proposal.NewStore(events)
	modules := module.NewRegistry()

	// What was said and what was decided outlive this container. Read before
	// anything can write: a boot that silently started an empty transcript
	// over a full file would erase it on the first turn.
	if err := transcript.Connect(log); err != nil {
		log.Error("대화 서비스에 연결하지 못했습니다", "err", err)
		os.Exit(1)
	}
	if err := proposals.Persist(cfg.ProposalsPath, log); err != nil {
		log.Error("제안을 읽지 못했습니다", "err", err)
		os.Exit(1)
	}

	// --- 모듈 -------------------------------------------------------------
	// Adding a feature is one Add call. Removing one is deleting the line -
	// nothing below this block names a specific module.
	calendarStore := calendar.NewStore(cfg.CalendarURL, cfg.APIToken, events)
	if cfg.Guest {
		// The guest is the calendar's other person: their entries are "나" here.
		calendarStore.FromPartnerSide()
	}
	calendarModule := calendar.New(calendarStore)
	modules.Add(calendarModule)

	gate := permission.NewGate(proposals, modules, transcript, cfg.Debug, log)
	mcpMounts := map[string]http.Handler{
		"/mcp":                        gate.Handler(),
		"/mcp/" + calendar.ServerName: calendarModule.Handler(),
	}
	cfg.ModuleMCPURLs[calendar.ServerName] = cfg.PublicMCPURL + "/" + calendar.ServerName

	// The operator's money: assets and spending. The guest agent is built
	// without them - no module, no MCP server, no tool - so there is nothing
	// for its model to call, whatever it is asked.
	var spendingModule *spending.Module
	var marketModule *market.Module
	var photos *uploads.Store
	if cfg.UploadsDir != "" {
		if photos, err = uploads.New(cfg.UploadsDir); err != nil {
			log.Error("사진 폴더를 열지 못했습니다", "dir", cfg.UploadsDir, "err", err)
			os.Exit(1)
		}
	}
	if !cfg.Guest {
		assetsModule := assets.New(assets.NewStore(cfg.AssetsURL, cfg.APIToken))
		modules.Add(assetsModule)
		spendingModule = spending.New(spending.NewStore(cfg.SpendingURL, cfg.APIToken))
		modules.Add(spendingModule)

		mcpMounts["/mcp/"+assets.ServerName] = assetsModule.Handler()
		mcpMounts["/mcp/"+spending.ServerName] = spendingModule.Handler()
		// Mounted but never put in the chat's MCP config: only the
		// background order lookup is handed this server.
		mcpMounts["/mcp/"+spending.EnrichServerName] = spendingModule.EnrichHandler()
		cfg.ModuleMCPURLs[assets.ServerName] = cfg.PublicMCPURL + "/" + assets.ServerName
		cfg.ModuleMCPURLs[spending.ServerName] = cfg.PublicMCPURL + "/" + spending.ServerName

		// 중고나라. 판매 글은 사진이 있어야 하므로 사진 폴더가 있을 때만 켠다.
		if photos != nil {
			marketModule = market.New(market.NewStore(cfg.MarketURL, cfg.APIToken), photos)
			modules.Add(marketModule)
			mcpMounts["/mcp/"+market.ServerName] = marketModule.Handler()
			// Background only, like spending-enrich.
			mcpMounts["/mcp/"+market.WorkerServerName] = marketModule.WorkerHandler()
			cfg.ModuleMCPURLs[market.ServerName] = cfg.PublicMCPURL + "/" + market.ServerName
		}
	}

	startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
	if err := modules.Start(startCtx); err != nil {
		cancelStart()
		log.Error("모듈을 시작하지 못했습니다", "err", err)
		os.Exit(1)
	}
	cancelStart()

	runner := claudecode.New(cfg, transcript, log)

	// 묶음 가맹점(쿠팡·네이버페이) 결제가 새로 들어왔을 때만 깨어나, 대화와
	// 분리된 세션으로 주문목록을 읽어 상품별로 나눈다.
	enrichCtx, stopEnrich := context.WithCancel(context.Background())
	defer stopEnrich()
	if _, ok := cfg.ExtraMCPURLs[spending.BrowserServer]; spendingModule != nil && cfg.Enrich && ok {
		go spendingModule.RunEnricher(enrichCtx, runner, spending.EnrichConfig{
			EnrichURL: cfg.PublicMCPURL + "/" + spending.EnrichServerName,
			Interval:  cfg.EnrichInterval,
			Cooldown:  cfg.EnrichCooldown,
			Timeout:   cfg.EnrichTimeout,
		}, log)
	} else if spendingModule != nil && cfg.Enrich {
		log.Warn("브라우저 MCP 가 없어 묶음 결제 조회를 켜지 않습니다", "need", spending.BrowserServer)
	}
	// 승인된 판매 글 등록, 탭에서 요청한 변경, 한 시간마다 현황 확인.
	if _, ok := cfg.ExtraMCPURLs[market.BrowserServer]; marketModule != nil && cfg.Market && ok {
		marketModule.Notify(proposals)
		go marketModule.RunWorker(enrichCtx, runner, market.WorkerConfig{
			WorkerURL: cfg.PublicMCPURL + "/" + market.WorkerServerName,
			Interval:  cfg.MarketInterval,
			Cooldown:  cfg.MarketCooldown,
			Timeout:   cfg.MarketTimeout,
		}, log)
	} else if marketModule != nil && cfg.Market {
		log.Warn("브라우저 MCP 가 없어 중고나라 작업을 켜지 않습니다", "need", market.BrowserServer)
	}
	// A card does not hold a turn open; its decision starts the next one.
	gate.SetFollowUp(runner)
	proposals.OnDecide(gate.Resolve)

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Deps{
			Role:            map[bool]string{true: "guest", false: "owner"}[cfg.Guest],
			Thread:          transcript,
			Proposals:       proposals,
			Calendar:        calendarStore,
			Runner:          runner,
			Bus:             events,
			MCP:             mcpMounts,
			AllowedOrigin:   cfg.AllowedOrigin,
			InterceptToken:  cfg.ExtraMCPToken,
			BackgroundLogin: cfg.BackgroundLogin,
			APIToken:        cfg.APIToken,
			ApprovalWait:    cfg.ApprovalWait,
			Uploads:         photos,
			Modules:         modules,
			Log:             log,
		}).Handler(),
		// No WriteTimeout: /events streams and /mcp blocks on a human.
		ReadHeaderTimeout: 10 * time.Second,
	}

	restored, _ := transcript.Snapshot()

	go func() {
		log.Info("jarvis agent listening",
			"addr", cfg.Addr,
			"now", time.Now().Format("2006-01-02 15:04 MST"),
			"workspace", cfg.Workspace,
			"modules", modules.Names(),
			"turns", len(restored),
			"proposals", len(proposals.List()),
			"permission_tool", permission.QualifiedToolName(),
			"auto_allowed", cfg.AllowedTools)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("서버가 죽었습니다", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = modules.Stop(ctx)
	log.Info("종료했습니다")
}
