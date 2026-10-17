package main

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/choigonyok/jarvis/agent/internal/browsercheck"
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
	"github.com/choigonyok/jarvis/agent/internal/module/messages"
	mindmod "github.com/choigonyok/jarvis/agent/internal/module/mind"
	"github.com/choigonyok/jarvis/agent/internal/module/place"
	"github.com/choigonyok/jarvis/agent/internal/module/spending"
	"github.com/choigonyok/jarvis/agent/internal/module/vault"
	"github.com/choigonyok/jarvis/agent/internal/suggest"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
	"github.com/choigonyok/jarvis/agent/internal/usage"
	"github.com/choigonyok/jarvis/gocommon/notify"
	"github.com/choigonyok/jarvis/gocommon/site"
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
	var vaultModule *vault.Module
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
		// A module's lookups skip the card whatever JARVIS_ALLOWED_TOOLS says.
		cfg.Allow(assets.Tools...)
		cfg.Allow(spending.Tools...)
		// Mounted but never put in the chat's MCP config: only the
		// background order lookup is handed this server.
		mcpMounts["/mcp/"+spending.EnrichServerName] = spendingModule.EnrichHandler()
		cfg.ModuleMCPURLs[assets.ServerName] = cfg.PublicMCPURL + "/" + assets.ServerName
		cfg.ModuleMCPURLs[spending.ServerName] = cfg.PublicMCPURL + "/" + spending.ServerName

		// 로그인 정보 보관함. 저장은 카드(비밀번호 칸 - 값은 모델을 거치지 않는다),
		// 쓰는 것은 브라우저의 browser_fill_login.
		if cfg.VaultURL != "" {
			vaultModule = vault.New(vault.NewClient(cfg.VaultURL, cfg.APIToken), log)
			modules.Add(vaultModule)
			mcpMounts["/mcp/"+vault.ServerName] = vaultModule.Handler()
			cfg.ModuleMCPURLs[vault.ServerName] = cfg.PublicMCPURL + "/" + vault.ServerName
			cfg.Allow(vault.Tools...)
		}

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
		cfg.Allow(memory.Tools...)
	}

	// Where the operator is (context-svc), read only. Not the guest's.
	if cfg.ContextURL != "" && !cfg.Guest {
		placeModule := place.New(cfg.ContextURL, cfg.APIToken)
		modules.Add(placeModule)
		mcpMounts["/mcp/"+place.ServerName] = placeModule.Handler()
		cfg.ModuleMCPURLs[place.ServerName] = cfg.PublicMCPURL + "/" + place.ServerName
		cfg.Allow(place.Tools...)
	}

	// mind-svc's gate: its cards become approval cards here, and approving
	// one is this module asking mind to do it. The guest has no mind.
	var mindClient *mindmod.Client
	if cfg.MindURL != "" && !cfg.Guest {
		mindModule := mindmod.New(mindmod.Client{URL: cfg.MindURL, Token: cfg.APIToken})
		modules.Add(mindModule)
		c := mindModule.Client()
		mindClient = &c

		// The messages themselves (KakaoTalk, iMessage, this conversation), as
		// mind keeps them: for what long-term memory did not take as a fact.
		messagesModule := messages.New(cfg.MindURL, cfg.APIToken)
		modules.Add(messagesModule)
		mcpMounts["/mcp/"+messages.ServerName] = messagesModule.Handler()
		cfg.ModuleMCPURLs[messages.ServerName] = cfg.PublicMCPURL + "/" + messages.ServerName
		cfg.Allow(messages.Tools...)
	}

	startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
	if err := modules.Start(startCtx); err != nil {
		cancelStart()
		log.Error("모듈을 시작하지 못했습니다", "err", err)
		os.Exit(1)
	}
	cancelStart()

	// The CLI's memory is a working directory (runner args), so it has to
	// exist before the first note is written into it.
	if cfg.MemoryDir != "" {
		if err := os.MkdirAll(cfg.MemoryDir, 0o755); err != nil {
			log.Warn("기억 폴더를 만들지 못했습니다", "dir", cfg.MemoryDir, "err", err)
		}
	}
	runner := claudecode.New(cfg, transcript, log)
	// Subscription use, read off every run's stream; kept beside the proposals.
	usageTracker := usage.New(filepath.Join(filepath.Dir(cfg.ProposalsPath), "usage.json"))
	runner.SetUsage(usageTracker)
	if memoryModule != nil {
		runner.SetRecall(memoryModule.Recall)
	}
	// A card this conversation raised - an approval asked in chat, a payment
	// stopped while it drove the browser - is continued in the session that
	// raised it, so a typed message does not start a new one under it. One
	// left two hours is taken as forgotten: a chat card never expires, and
	// one of those must not keep the session growing for good.
	runner.SetHold(func(now time.Time) bool {
		_, open := proposals.FindPendingWhere(func(p proposal.Proposal) bool {
			return (p.Origin == proposal.OriginChat || p.Origin == proposal.OriginIntercept) && now.Sub(p.Opened()) < 2*time.Hour
		})
		return open
	})

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
			Model:     cfg.EnrichModel,
		}, log)
	} else if spendingModule != nil && cfg.Enrich {
		log.Warn("브라우저 MCP 가 없어 묶음 결제 조회를 켜지 않습니다", "need", spending.BrowserServer)
	}
	if _, ok := cfg.ExtraMCPURLs[jobs.BrowserServer]; jobsModule != nil && ok {
		guard, err := jobs.NewBridgeGuard(cfg.ExtraMCPURLs[jobs.BrowserServer], cfg.ExtraMCPToken)
		if err != nil {
			log.Error("작업을 사이트 안에 묶을 수 없어 작업을 실행하지 않습니다", "err", err)
		} else {
			go jobsModule.Run(enrichCtx, runner, jobs.SchedulerConfig{
				RunURL:     cfg.PublicMCPURL + "/job-run",
				Interval:   cfg.JobsInterval,
				Cooldown:   cfg.JobsCooldown,
				Timeout:    cfg.JobsTimeout,
				UploadsDir: cfg.UploadsDir,
				Guard:      guard,
				Model:      cfg.JobsModel,
				RetryModel: cfg.JobsRetryModel,
			})
		}
	} else if jobsModule != nil {
		log.Warn("브라우저 MCP 가 없어 작업을 실행하지 않습니다", "need", jobs.BrowserServer)
	}
	// 쇼핑몰 로그인 확인. 브라우저를 쓰는 것은 에이전트뿐이므로, 대화·작업이 없을
	// 때 여기서 연다. status-svc 는 결과만 읽는다(/browser-checks).
	var browserChecks http.Handler
	if u, ok := cfg.ExtraMCPURLs[spending.BrowserServer]; ok && !cfg.Guest {
		checker := &browsercheck.Checker{
			Client: browsercheck.New(u, cfg.ExtraMCPToken),
			Sites: []browsercheck.Site{
				{ID: "coupang", Orders: "https://mc.coupang.com/ssr/desktop/order/list", LoginHost: "login.coupang.com"},
			},
			// 다른 탭이 로그인 화면이면(누가 비밀번호를 치는 중일 수 있다) 열지 않는다.
			BusyHosts: []string{"login.coupang.com", "nid.naver.com", "pay.naver.com"},
			Every:     30 * time.Minute,
			Idle:      runner.Idle,
			Log:       log,
		}
		// 풀린 로그인은 보관함의 정보로 다시 한다. 안 되면(문자 인증 등) 알린다.
		if vaultModule != nil {
			checker.Relogin = relogin(runner, vaultModule, u, cfg.ExtraMCPToken)
			checker.CanRelogin = func(ctx context.Context, s browsercheck.Site) bool {
				domain, err := site.Domain(s.LoginHost)
				return err == nil && vaultModule.Has(ctx, domain)
			}
			loginNote := notify.New(cfg.NotifyURL, cfg.DatabaseURL, cfg.APIToken, log)
			checker.StillOut = func(s browsercheck.Site, why string) {
				loginNote.Send(notify.Event{
					Source: "agent", Kind: "browser.login", Tier: "now", Level: "warn",
					Title: s.ID + " 자동 로그인이 안 됐어요",
					Body:  why + " - 화면 탭에서 직접 로그인해 주세요.",
					URL:   "/screen", Key: "relogin:" + s.ID + ":" + time.Now().Format("2006-01-02T15"),
				})
			}
		}
		browserChecks = checker.Handler()
		go checker.Run(enrichCtx)
	}
	// A reading older than an hour is refreshed with the smallest request.
	go runner.WatchUsage(enrichCtx, time.Hour)

	// Speaking first: mind-svc's gate decides what is worth raising (its
	// ledger, budget and trust), and its cards become approval cards here
	// (internal/suggest). The guest's agent does not suggest - it has none of
	// the operator's data.
	var suggester *suggest.Engine
	if cfg.EventsURL != "" && !cfg.Guest {
		suggester = suggest.New(proposals, suggest.Config{
			EventsURL: cfg.EventsURL, Token: cfg.APIToken,
			Out:  notify.New(cfg.EventsURL, cfg.DatabaseURL, cfg.APIToken, log),
			Say:  func(lead, id string) { transcript.Raise([]string{lead}, id) },
			Mind: mindClient,
		}, log)
		proposals.OnDecide(suggester.OnDecide)
		go suggester.Subscribe(enrichCtx, strings.TrimRight(cfg.SelfURL, "/")+"/consume")
	}
	var consume http.HandlerFunc
	if suggester != nil {
		consume = suggester.Consume
	}
	// A card does not hold a turn open; its decision starts the next one.
	gate.SetFollowUp(runner)
	proposals.OnDecide(gate.Resolve)

	// Every card waiting on someone reaches their phone. The guest's cards are
	// the guest's: they ring the guest's devices (audience guest), never the
	// operator's.
	if notifier := notify.New(cfg.NotifyURL, cfg.DatabaseURL, cfg.APIToken, log); notifier != nil {
		audience, keyPrefix := "", "proposal:"
		if cfg.Guest {
			audience, keyPrefix = "guest", "guest-proposal:"
		}
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
				// Straight to the card, not the bottom of the thread.
				Title: title, Body: body, URL: "/?proposal=" + p.ID, Key: keyPrefix + p.ID,
				Audience: audience,
			})
		})
	}

	// The guest is not shown what the operator's API key costs.
	var apiUsage func(context.Context) (any, error)
	if memoryModule != nil && !cfg.Guest {
		apiUsage = memoryModule.APIUsage
	}

	var mindUsage func(context.Context) (any, error)
	if mindClient != nil {
		mindUsage = mindClient.Usage
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
			MCPToken:        cfg.InternalMCPToken,
			AllowedOrigin:   cfg.AllowedOrigin,
			InterceptToken:  cfg.ExtraMCPToken,
			BackgroundLogin: cfg.BackgroundLogin,
			APIToken:        cfg.APIToken,
			ApprovalWait:    cfg.ApprovalWait,
			Uploads:         photos,
			Modules:         modules,
			Usage:           usageTracker,
			APIUsage:        apiUsage,
			MindUsage:       mindUsage,
			Consume:         consume,
			BrowserChecks:   browserChecks,
			Vault:           vaultConsole(vaultModule),
			FollowUp:        runner,
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
			"auto_allowed", cfg.AllowedTools,
			"builtin_tools", builtinTools(cfg.BuiltinTools),
			"cache_ttl", cfg.CacheTTL,
			"session_idle", cfg.SessionIdle.String())
		if len(cfg.DroppedTools) > 0 {
			log.Warn("JARVIS_ALLOWED_TOOLS 에서 카드를 건너뛰면 안 되는 도구를 뺐습니다", "dropped", cfg.DroppedTools)
		}
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

// vaultConsole is the saved logins' console view, or nothing without a vault.
func vaultConsole(m *vault.Module) http.Handler {
	if m == nil {
		return nil
	}
	return m.ConsoleHandler()
}

// relogin signs a logged-out site back in with the login the vault keeps: a
// background task, held to that site's domain, that opens the page, has the
// browser fill the login (browser_fill_login - the model never sees it) and
// presses the button. Anything beyond a plain login - a code by text, a
// captcha, a new-device check - it leaves for the operator.
func relogin(runner *claudecode.Runner, v *vault.Module, browserURL, browserToken string) func(context.Context, browsercheck.Site) error {
	return func(ctx context.Context, s browsercheck.Site) error {
		domain, err := site.Domain(s.LoginHost)
		if err != nil {
			return err
		}
		if !v.Has(ctx, domain) {
			return fmt.Errorf("%s 에 저장된 로그인 정보가 없습니다", domain)
		}
		guard, err := jobs.NewBridgeGuard(browserURL, browserToken)
		if err != nil {
			return err
		}
		if err := guard.Restrict(ctx, []string{domain}, 6*time.Minute); err != nil {
			return fmt.Errorf("브라우저를 %s 안에 묶지 못했습니다: %w", domain, err)
		}
		defer func() { _ = guard.Release(context.WithoutCancel(ctx)) }()
		tools := []string{}
		for _, t := range []string{"browser_navigate", "browser_get_state", "browser_click", "browser_fill_login", "browser_screenshot", "browser_list_tabs", "browser_switch_tab", "browser_close_tab"} {
			tools = append(tools, "mcp__"+jobs.BrowserServer+"__"+t)
		}
		_, err = runner.Background(ctx, claudecode.Task{
			Name: "재로그인 " + s.ID,
			Prompt: fmt.Sprintf("%s(%s) 로그인이 풀렸습니다. 다시 로그인하세요.\n"+
				"1. browser_navigate 로 %s 를 엽니다. 로그인 화면으로 넘어갑니다.\n"+
				"2. browser_fill_login 으로 저장된 아이디·비밀번호를 넣습니다(값은 당신에게 보이지 않습니다).\n"+
				"3. 로그인 버튼을 한 번 누르고 browser_get_state 로 로그인됐는지 봅니다.\n"+
				"4. 연 탭을 닫고, 결과를 한 줄로 씁니다.", s.ID, domain, s.Orders),
			System: "당신은 Jarvis의 백그라운드 재로그인입니다. 아무도 보지 않는 채로, 운영자의 계정이 로그인된 브라우저를 씁니다.\n" +
				"- 로그인 말고는 아무것도 누르지 않습니다. 주문·결제·설정·광고는 건드리지 않습니다.\n" +
				"- 문자·이메일 인증, 보안문자, 새 기기 확인, 비밀번호 변경 안내가 나오면 더 누르지 말고 멈춘 뒤 그 사실을 한 줄로 씁니다.\n" +
				"- 로그인 버튼은 한 번만 누릅니다. 실패하면 다시 넣거나 다시 누르지 않습니다(계정이 잠길 수 있습니다).",
			Extra:   []string{jobs.BrowserServer},
			Allowed: tools,
			Timeout: 5 * time.Minute,
		})
		return err
	}
}

// builtinTools is the --tools value for the log: "default" when unset.
func builtinTools(p *string) string {
	if p == nil {
		return "default"
	}
	return *p
}
