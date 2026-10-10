package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	"github.com/choigonyok/jarvis/agent/internal/module/jobs"
	"github.com/choigonyok/jarvis/agent/internal/module/memory"
	"github.com/choigonyok/jarvis/agent/internal/module/spending"
	"github.com/choigonyok/jarvis/agent/internal/notify"
	"github.com/choigonyok/jarvis/agent/internal/suggest"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
	"github.com/choigonyok/jarvis/agent/internal/usage"
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
	var jobsModule *jobs.Module
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

		// 작업. 대화에서 맡긴 일을 백그라운드에서, 작업이 정한 주기로 한다.
		if photos != nil && cfg.Jobs {
			jobsModule = jobs.New(jobs.NewStore(cfg.JobsURL, cfg.APIToken), photos, proposals, transcript, log)
			modules.Add(jobsModule)
			mcpMounts["/mcp/"+jobs.ServerName] = jobsModule.Handler()
			// Background only: /mcp/job-run/<run id>, never in the chat's config.
			mcpMounts["/mcp/job-run"] = jobsModule.RunHandler()
			cfg.ModuleMCPURLs[jobs.ServerName] = cfg.PublicMCPURL + "/" + jobs.ServerName
		}
	}

	// 장기 기억. 주인은 "owner", 손님은 "guest" 그룹만 읽는다.
	var memoryModule *memory.Module
	if cfg.MemoryURL != "" {
		memoryModule = memory.New(memory.NewStore(cfg.MemoryURL, cfg.APIToken,
			map[bool]string{true: "guest", false: "owner"}[cfg.Guest]))
		modules.Add(memoryModule)
		mcpMounts["/mcp/"+memory.ServerName] = memoryModule.Handler()
		cfg.ModuleMCPURLs[memory.ServerName] = cfg.PublicMCPURL + "/" + memory.ServerName
	}

	startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
	if err := modules.Start(startCtx); err != nil {
		cancelStart()
		log.Error("모듈을 시작하지 못했습니다", "err", err)
		os.Exit(1)
	}
	cancelStart()

	runner := claudecode.New(cfg, transcript, log)
	// Subscription use, read off every run's stream; kept beside the proposals.
	usageTracker := usage.New(filepath.Join(filepath.Dir(cfg.ProposalsPath), "usage.json"))
	runner.SetUsage(usageTracker)
	if memoryModule != nil {
		runner.SetRecall(memoryModule.Recall)
	}

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
	if _, ok := cfg.ExtraMCPURLs[jobs.BrowserServer]; jobsModule != nil && ok {
		go jobsModule.Run(enrichCtx, runner, jobs.SchedulerConfig{
			RunURL:     cfg.PublicMCPURL + "/job-run",
			Interval:   cfg.JobsInterval,
			Cooldown:   cfg.JobsCooldown,
			Timeout:    cfg.JobsTimeout,
			UploadsDir: cfg.UploadsDir,
		})
	} else if jobsModule != nil {
		log.Warn("브라우저 MCP 가 없어 작업을 실행하지 않습니다", "need", jobs.BrowserServer)
	}
	// A reading older than an hour is refreshed with the smallest request.
	go runner.WatchUsage(enrichCtx, time.Hour)

	// Speaking first: events-svc delivers what could be worth a suggestion,
	// and the assistant judges it in the background (internal/suggest). The
	// guest's agent does not suggest - it has none of the operator's data.
	var suggester *suggest.Engine
	if cfg.EventsURL != "" && !cfg.Guest {
		ledger, err := suggest.OpenLedger(cfg.SuggestionsPath)
		if err != nil {
			log.Error("제안 기록을 읽지 못했습니다", "err", err)
			os.Exit(1)
		}
		suggester = suggest.New(ledger, proposals, modules, runner, suggest.Config{
			MCPBase: cfg.PublicMCPURL, EventsURL: cfg.EventsURL, Token: cfg.APIToken,
			Out: notify.New(cfg.EventsURL, cfg.DatabaseURL, cfg.APIToken, log),
			Say: func(lead, id string) { transcript.Raise([]string{lead}, id) },
		}, log)
		mcpMounts["/mcp/"+suggest.ServerName] = suggester.Handler()
		proposals.OnDecide(suggester.OnDecide)
		go suggester.Run(enrichCtx)
		go suggester.Subscribe(enrichCtx, strings.TrimRight(cfg.SelfURL, "/")+"/consume")
	}
	var consume http.HandlerFunc
	if suggester != nil {
		consume = suggester.Consume
	}
	// A card does not hold a turn open; its decision starts the next one.
	gate.SetFollowUp(runner)
	proposals.OnDecide(gate.Resolve)

	// Every card waiting on the operator reaches the phone. The guest's cards
	// are the guest's: they do not ring the operator.
	if notifier := notify.New(cfg.NotifyURL, cfg.DatabaseURL, cfg.APIToken, log); notifier != nil && !cfg.Guest {
		proposals.OnOpen(func(p proposal.Proposal) {
			kind, title := "approval.pending", "결재 대기"
			if p.Origin == proposal.OriginSuggest {
				kind, title = "suggestion.new", "제안"
			}
			if p.Origin == proposal.OriginNotice {
				// A background job asking for something done by hand (log in again).
				kind, title = "job.request", "작업이 기다려요"
			}
			body := p.Card.Title
			if p.Card.Body != "" {
				body += " - " + p.Card.Body
			}
			notifier.Send(notify.Event{
				Source: "agent", Kind: kind, Tier: "now", Level: "warn",
				Title: title, Body: body, URL: "/", Key: "proposal:" + p.ID,
			})
		})
	}

	// The guest is not shown what the operator's API key costs.
	var apiUsage func(context.Context) (any, error)
	if memoryModule != nil && !cfg.Guest {
		apiUsage = memoryModule.APIUsage
	}

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
			Usage:           usageTracker,
			APIUsage:        apiUsage,
			Consume:         consume,
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
