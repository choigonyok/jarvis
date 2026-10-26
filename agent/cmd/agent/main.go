package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
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
	"github.com/choigonyok/jarvis/agent/internal/memorywatch"
	"github.com/choigonyok/jarvis/agent/internal/module/assets"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/module/checklist"
	"github.com/choigonyok/jarvis/agent/internal/module/jobs"
	"github.com/choigonyok/jarvis/agent/internal/module/memory"
	"github.com/choigonyok/jarvis/agent/internal/module/messages"
	mindmod "github.com/choigonyok/jarvis/agent/internal/module/mind"
	"github.com/choigonyok/jarvis/agent/internal/module/place"
	"github.com/choigonyok/jarvis/agent/internal/module/remind"
	"github.com/choigonyok/jarvis/agent/internal/module/spending"
	"github.com/choigonyok/jarvis/agent/internal/module/vault"
	"github.com/choigonyok/jarvis/agent/internal/module/web"
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
	var assetsModule *assets.Module
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
		assetsModule = assets.New(assets.NewStore(cfg.AssetsURL, cfg.APIToken))
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

	}

	// 작업. 대화에서 맡긴 일을 백그라운드에서, 작업이 정한 주기로 한다. 손님의 것은
	// 손님 작업(jobs-svc 가 손님 토큰으로 묶는다)이고, 웹사이트도 사진도 없다 - 대화에
	// 첨부한 손님 사진도 작업에는 넘기지 않는다(jobs-svc 가 손님 작업의 사진을 받지 않는다).
	if cfg.Jobs && (photos != nil || cfg.Guest) {
		jobPhotos := photos
		if cfg.Guest {
			jobPhotos = nil
		}
		jobsModule = jobs.New(jobs.NewStore(cfg.JobsURL, cfg.APIToken), jobPhotos, proposals, transcript, log)
		if cfg.Guest {
			jobsModule.NoBrowser()
			cfg.Allow(jobs.QualifiedToolName("list_jobs"))
		}
		modules.Add(jobsModule)
		mcpMounts["/mcp/"+jobs.ServerName] = jobsModule.Handler()
		// Background only: /mcp/job-run/<run id>, never in the chat's config.
		mcpMounts["/mcp/job-run"] = jobsModule.RunHandler()
		cfg.ModuleMCPURLs[jobs.ServerName] = cfg.PublicMCPURL + "/" + jobs.ServerName
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

	// 웹 찾기: 네이버 검색, 페이지 읽기(그림 속 글자까지), 네이버 지도. 찾고 읽는 것은
	// search-svc 가 하고, 여기에는 도구와 '본 주소' 장부가 있다. 손님에게도 - 운영자의 것은
	// 아무것도 열지 않는다.
	var webModule *web.Module
	if cfg.SearchOn() {
		webModule = web.New(web.NewClient(cfg.SearchURL, cfg.SearchToken))
		modules.Add(webModule)
		mcpMounts["/mcp/"+web.ServerName] = webModule.Handler()
		cfg.ModuleMCPURLs[web.ServerName] = cfg.PublicMCPURL + "/" + web.ServerName
		cfg.Allow(web.Tools...)
	}

	// Where the operator is (context-svc), read only. Not the guest's.
	if cfg.ContextURL != "" && !cfg.Guest {
		placeModule := place.New(cfg.ContextURL, cfg.APIToken)
		if webModule != nil {
			placeModule.UseNaver(webModule.Client())
		}
		modules.Add(placeModule)
		mcpMounts["/mcp/"+place.ServerName] = placeModule.Handler()
		cfg.ModuleMCPURLs[place.ServerName] = cfg.PublicMCPURL + "/" + place.ServerName
		cfg.Allow(place.Tools...)
	}

	// mind-svc's gate: its cards become approval cards here, and approving
	// one is this module asking mind to do it. The guest has no mind - only
	// their own reminders and checklists, which mind keeps by the guest's
	// token apart from the operator's.
	var mindClient *mindmod.Client
	var checklistModule *checklist.Module
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
	if cfg.MindURL != "" {
		// 리마인드: "몇 시에 / 집에 가면 / 나갈 때 알려줘". mind 가 그때 폰으로 알린다.
		remindModule := remind.New(cfg.MindURL, cfg.APIToken)
		modules.Add(remindModule)
		mcpMounts["/mcp/"+remind.ServerName] = remindModule.Handler()
		cfg.ModuleMCPURLs[remind.ServerName] = cfg.PublicMCPURL + "/" + remind.ServerName
		cfg.Allow(remind.Tools...)

		// 체크리스트: 준비물·할 일. mind 가 체크 상태를 갖고, 대화에는 체크할 수 있는 카드로.
		checklistModule = checklist.New(cfg.MindURL, cfg.APIToken, transcript, events, log)
		modules.Add(checklistModule)
		mcpMounts["/mcp/"+checklist.ServerName] = checklistModule.Handler()
		cfg.ModuleMCPURLs[checklist.ServerName] = cfg.PublicMCPURL + "/" + checklist.ServerName
		cfg.Allow(checklist.Tools...)
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
	if webModule != nil {
		// What went past - the operator's words, every tool's result - is
		// what read may open.
		runner.OnSeen(webModule.Saw)
	}
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
	// A charge the rules left in 기타 is judged by the model first - a
	// merchant whose trade is plain is filed without a card - and the card
	// (spending/ask.go) is for what it could not tell either.
	if spendingModule != nil && !cfg.Guest {
		spendingModule.Ask(proposals, log)
		spendingModule.Attach(transcript.AttachProposal)
		go spendingModule.RunAsker(enrichCtx, spending.AskConfig{Every: 10 * time.Minute, MaxOpen: 3, Judge: runner, Model: cfg.EnrichModel})
	}
	// Money taken out of an investment account that no deposit here explains
	// is asked about, one card per withdrawal (assets/ask.go); what was spent
	// reaches the 가계부 through assets-svc.
	if assetsModule != nil && spendingModule != nil && !cfg.Guest {
		assetsModule.Ask(proposals, log)
		assetsModule.Attach(transcript.AttachProposal)
		go assetsModule.RunAsker(enrichCtx, assets.AskConfig{Every: 10 * time.Minute, MaxOpen: 12, Categories: spendingModule.Categories})
	}
	// A job finds and reads the web with the conversation's own tools - no
	// browser, no site to add.
	var jobsWeb *jobs.WebTools
	if webModule != nil {
		jobsWeb = &jobs.WebTools{Name: web.ServerName, URL: cfg.PublicMCPURL + "/" + web.ServerName, Tools: web.Tools}
	}
	if jobsModule != nil && cfg.Guest {
		// The guest's jobs open no website: nothing to hold them to.
		go jobsModule.Run(enrichCtx, runner, jobs.SchedulerConfig{
			RunURL:     cfg.PublicMCPURL + "/job-run",
			Interval:   cfg.JobsInterval,
			Cooldown:   cfg.JobsCooldown,
			Timeout:    cfg.JobsTimeout,
			Model:      cfg.JobsModel,
			RetryModel: cfg.JobsRetryModel,
			Web:        jobsWeb,
		})
	} else if _, ok := cfg.ExtraMCPURLs[jobs.BrowserServer]; jobsModule != nil && ok {
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
				Web:        jobsWeb,
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
			// What the relogin task last said it saw, by site - the reason a
			// login did not take ("보안문자", "비밀번호 오류") belongs in the
			// notice. The checker calls Relogin and then StillOut from its one
			// loop, so nothing else touches this map.
			reloginSaid := map[string]string{}
			checker.Relogin = relogin(runner, vaultModule, u, cfg.ExtraMCPToken, func(id, said string) {
				log.Info("재로그인 작업이 말한 결과", "site", id, "result", clip(said, 300))
				reloginSaid[id] = said
			})
			checker.CanRelogin = func(ctx context.Context, s browsercheck.Site) bool {
				domain, err := site.Domain(s.LoginHost)
				return err == nil && vaultModule.Has(ctx, domain)
			}
			loginNote := notify.New(cfg.NotifyURL, cfg.DatabaseURL, cfg.APIToken, log)
			checker.StillOut = func(s browsercheck.Site, why string) {
				if said := strings.TrimSpace(reloginSaid[s.ID]); said != "" {
					why += " · 재로그인 작업: " + clip(said, 160)
				}
				loginNote.Send(notify.Event{
					Source: "agent", Kind: "browser.login", Tier: "now", Level: "warn",
					Title: s.ID + " 자동 로그인이 안 됐어요",
					Body:  why + " - 화면 탭에서 직접 로그인해 주세요.",
					URL:   "/screen", Key: "relogin:" + s.ID + ":" + time.Now().Format("2006-01-02T15"),
				})
			}
		}
		// Signed in again (a relogin, or by hand on the 화면 tab): the book's
		// lookups that stopped at the login page go on by themselves.
		if spendingModule != nil {
			checker.SignedIn = func(ctx context.Context, s browsercheck.Site) {
				if n, err := spendingModule.Resume(ctx, s.ID); err != nil {
					log.Warn("로그인 뒤 멈춘 주문 조회를 다시 잇지 못했습니다", "site", s.ID, "err", err)
				} else if n > 0 {
					log.Info("로그인돼 있어 멈췄던 주문 조회를 다시 잇습니다", "site", s.ID, "count", n)
				}
			}
		}
		browserChecks = checker.Handler()
		go checker.Run(enrichCtx)
	}
	// A reading older than an hour is refreshed with the smallest request.
	go runner.WatchUsage(enrichCtx, time.Hour)

	// The model writes its memory without a card (config.MemoryDir); every
	// change reaches the operator instead, with the words written - the
	// guest's agent's, the guest. What was last told is kept beside the
	// proposals, where the model cannot write.
	if cfg.MemoryDir != "" {
		audience, keyPrefix := "", "memory:"
		if cfg.Guest {
			audience, keyPrefix = "guest", "guest-memory:"
		}
		if memoryNote := notify.New(cfg.NotifyURL, cfg.DatabaseURL, cfg.APIToken, log); memoryNote != nil {
			watcher := &memorywatch.Watcher{
				Dir:      cfg.MemoryDir,
				SeenPath: filepath.Join(filepath.Dir(cfg.ProposalsPath), "memory-seen.json"),
				Every:    15 * time.Second,
				Log:      log,
				Tell: func(entries []memorywatch.Entry) {
					title, body := memorywatch.Message(entries)
					log.Info("자비스 기억이 바뀌어 알립니다", "files", len(entries))
					memoryNote.Send(notify.Event{
						Source: "agent", Kind: "memory.change", Tier: "now", Level: "info",
						// Into the inbox, where the whole body is shown.
						Title: title, Body: body, URL: "/?inbox=1",
						Key:      keyPrefix + strconv.FormatInt(time.Now().UnixNano(), 36),
						Audience: audience,
					})
				},
			}
			go watcher.Run(enrichCtx)
		}
	}

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
			// What mind told at its moment, in the conversation as well as on
			// the phone: the reminder's words, why now, the list it carries.
			Notice: func(n suggest.Notice) {
				if transcript.Told(thread.OriginNotice, n.Key) {
					return
				}
				paragraphs := []string{n.Title}
				if n.Body != "" {
					paragraphs = append(paragraphs, n.Body)
				}
				transcript.Card(thread.Origin{Kind: thread.OriginNotice, Title: n.Why, Ref: n.Key, Checklist: n.Checklist}, paragraphs)
				if n.Checklist != "" && checklistModule != nil {
					checklistModule.Publish(context.Background(), n.Checklist)
				}
			},
			Show: func(id, lead, key string) {
				if checklistModule == nil {
					return
				}
				if _, err := checklistModule.ShowAs(context.Background(), id, lead, key); err != nil {
					log.Warn("mind 가 띄운 체크리스트를 보이지 못했습니다", "checklist", id, "err", err)
				}
			},
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
	// The guest's file tools reach its own photos and memory and nothing else:
	// a call inside them goes by (the CLI's memory writes need no card of
	// their own anyway), and one anywhere else is refused, never asked.
	var guestFolders permission.Folders
	if cfg.Guest {
		guestFolders = permission.Folders{cfg.UploadsDir, cfg.MemoryDir}
		gate.SetRefuse(func(tool string, input json.RawMessage) string {
			if file, inside := guestFolders.Judge(tool, input); file && !inside {
				return "이 계정에서는 첨부한 사진과 자비스 기억 밖의 파일을 열거나 쓸 수 없습니다. 다시 시도하지 마세요."
			}
			return ""
		})
	}
	// A card stands only before a change or money (2026-10-20): a job made or
	// instructed (jobs.CardFree) and the browser's own tools - reading,
	// pressing, typing, the PIN at a keypad a payment card already let
	// through - go by. The browser's guard stops a payment on the network and
	// raises its card; a job asks before it posts, sends or deletes.
	gate.SetAutoAllow(func(tool string, input json.RawMessage) bool {
		if cfg.Guest {
			if file, inside := guestFolders.Judge(tool, input); file {
				return inside
			}
		}
		return jobs.BrowserTool(tool) || jobsModule != nil && jobsModule.CardFree(tool, input)
	})
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
			if p.Action.Kind == spending.KindCategory {
				// The book asking what a charge was for: muted with the rest of 가계부.
				kind, title = "spending.ask", "가계부"
			}
			if p.Action.Kind == assets.KindOutflow {
				// Where a withdrawal went: muted with the rest of 자산.
				kind, title = "assets.ask", "자산"
			}
			body := p.Card.Title
			if p.Card.Body != "" {
				body += " - " + p.Card.Body
			}
			// A quiet card (mind's gate: not worth a buzz now) waits for the
			// evening digest; the card itself is up meanwhile.
			tier, level := "now", "warn"
			if p.Quiet {
				tier, level = "digest", "info"
			}
			notifier.Send(notify.Event{
				Source: "agent", Kind: kind, Tier: tier, Level: level,
				// Straight to the card, not the bottom of the thread.
				Title: title, Body: body, URL: "/?proposal=" + p.ID, Key: keyPrefix + p.ID,
				Audience: audience,
			})
		})
		// Decided, the card's notification is done with: notify-svc marks it
		// read on this event (its key is the one the notification above carried).
		closed := func(p proposal.Proposal, decision string) {
			notifier.Emit(map[string]any{
				"source": "agent", "type": "proposal.decided", "subject": p.ID,
				"data": map[string]any{"proposalId": p.ID, "decision": decision, "notifyKey": keyPrefix + p.ID},
				"key":  "proposal:decided:" + p.ID,
			})
		}
		proposals.OnDecide(func(p proposal.Proposal, d proposal.Decision) { closed(p, string(d)) })
		// Put away without a decision (replaced by a corrected card): the same.
		proposals.OnClose(func(p proposal.Proposal) { closed(p, "closed") })
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

	// A job's run that reaches a payment raises a card for that job.
	var jobPayments httpapi.JobPayments
	var wakeJobs func()
	if jobsModule != nil {
		jobPayments = jobsModule
		wakeJobs = jobsModule.Wake
		// A job waiting for the browser starts when a turn or another task
		// gives it up, not on the scheduler's next look.
		runner.OnFree(jobsModule.Wake)
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
			Jobs:            jobPayments,
			WakeJobs:        wakeJobs,
			Checklists:      checklistConsole(checklistModule),
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

// checklistConsole is the lists the console's cards show and tick, or none
// (a nil module must not become a non-nil interface).
func checklistConsole(m *checklist.Module) httpapi.Checklists {
	if m == nil {
		return nil
	}
	return m
}

// relogin signs a logged-out site back in with the login the vault keeps: a
// background task, held to that site's domain, that opens the page, has the
// browser fill the login (browser_fill_login - the model never sees it, and
// it turns on the form's 자동 로그인 so the login outlives the browser) and
// presses the button. Anything beyond a plain login - a code by text, a
// captcha, a new-device check - it leaves for the operator. said hears the
// task's own one-line account of what it saw.
func relogin(runner *claudecode.Runner, v *vault.Module, browserURL, browserToken string, said func(site, result string)) func(context.Context, browsercheck.Site) error {
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
		result, err := runner.Background(ctx, claudecode.Task{
			Name: "재로그인 " + s.ID,
			Prompt: fmt.Sprintf("%s(%s) 로그인이 풀렸습니다. 다시 로그인하세요.\n"+
				"1. browser_navigate 로 %s 를 엽니다. 로그인 화면으로 넘어갑니다.\n"+
				"2. browser_fill_login 으로 저장된 아이디·비밀번호를 넣습니다(값은 당신에게 보이지 않습니다). 자동 로그인 칸은 이 도구가 켭니다.\n"+
				"3. 로그인 버튼을 한 번 누릅니다. 로그인 뒤 원래 페이지로 넘어가는 데 몇 초 걸리니, 몇 초 간격으로 browser_get_state 를 두세 번 보며 주소가 로그인 화면을 벗어났는지 확인합니다. "+
				"'잠시만 기다려 주세요' 같은 확인 화면은 저절로 넘어갈 때까지 기다립니다(누르지 않습니다).\n"+
				"4. 넘어간 것을 본 뒤에 연 탭을 닫고, 결과를 한 줄로 씁니다: 로그인됨, 또는 무엇이 막았는지(보안문자, 문자 인증, 오류 문구 그대로).", s.ID, domain, s.Orders),
			System: "당신은 Jarvis의 백그라운드 재로그인입니다. 아무도 보지 않는 채로, 운영자의 계정이 로그인된 브라우저를 씁니다.\n" +
				"- 로그인 말고는 아무것도 누르지 않습니다. 주문·결제·설정·광고는 건드리지 않습니다.\n" +
				"- 문자·이메일 인증, 보안문자, 새 기기 확인, 비밀번호 변경 안내가 나오면 더 누르지 말고 멈춘 뒤 그 사실을 한 줄로 씁니다.\n" +
				"- 로그인 버튼은 한 번만 누릅니다. 실패하면 다시 넣거나 다시 누르지 않습니다(계정이 잠길 수 있습니다).",
			Extra:   []string{jobs.BrowserServer},
			Allowed: tools,
			Timeout: 5 * time.Minute,
			Browser: true,
		})
		if said != nil {
			said(s.ID, result)
		}
		return err
	}
}

// clip shortens s to n runes for a log line or a notice.
func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// builtinTools is the --tools value for the log: "default" when unset.
func builtinTools(p *string) string {
	if p == nil {
		return "default"
	}
	return *p
}
