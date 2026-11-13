// Package assemble puts the agent together: every module, the look and
// change boards, the MCP servers and what may be called without a card, the
// runner and the permission gate, the hooks that tie them, and the loops that
// run on their own. It is one function for the agent (cmd/agent) and for the
// conversation's tests (internal/convo) alike, so what the tests put in front
// of the model is what the agent puts there, by construction - the tests
// hand in their fakes through Deps and nothing else.
package assemble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/agent/internal/adapter/permission"
	"github.com/choigonyok/jarvis/agent/internal/browsercheck"
	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/httpapi"
	"github.com/choigonyok/jarvis/agent/internal/memorywatch"
	"github.com/choigonyok/jarvis/agent/internal/mindcmd"
	"github.com/choigonyok/jarvis/agent/internal/module/assets"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
	"github.com/choigonyok/jarvis/agent/internal/module/checklist"
	"github.com/choigonyok/jarvis/agent/internal/module/goal"
	"github.com/choigonyok/jarvis/agent/internal/module/jobs"
	"github.com/choigonyok/jarvis/agent/internal/module/memory"
	"github.com/choigonyok/jarvis/agent/internal/module/messages"
	mindmod "github.com/choigonyok/jarvis/agent/internal/module/mind"
	photolib "github.com/choigonyok/jarvis/agent/internal/module/photos"
	"github.com/choigonyok/jarvis/agent/internal/module/place"
	"github.com/choigonyok/jarvis/agent/internal/module/remind"
	"github.com/choigonyok/jarvis/agent/internal/module/show"
	"github.com/choigonyok/jarvis/agent/internal/module/spending"
	"github.com/choigonyok/jarvis/agent/internal/module/vault"
	"github.com/choigonyok/jarvis/agent/internal/module/web"
	"github.com/choigonyok/jarvis/agent/internal/policy"
	"github.com/choigonyok/jarvis/agent/internal/shot"
	"github.com/choigonyok/jarvis/agent/internal/steps"
	"github.com/choigonyok/jarvis/agent/internal/suggest"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/tool"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
	"github.com/choigonyok/jarvis/agent/internal/usage"
	"github.com/choigonyok/jarvis/agent/internal/words"
	"github.com/choigonyok/jarvis/gocommon/checks"
	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/notify"
	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/record"
)

// answerWait is how long an answer to one of mind's cards waits for what came
// of it.
const answerWait = 90 * time.Second

// saidWithin is how fresh what the operator said must be to count as said just
// now, beside a card the assistant is about to raise.
const saidWithin = 15 * time.Minute

// Deps is what is handed in besides the configuration: where everything
// logs, and the conversation's tests' fakes. The agent leaves the fakes unset.
type Deps struct {
	Log *slog.Logger
	// Tools, when set, also hears each tool the conversation calls, as it
	// calls it: the conversation's tests read what was done off it.
	Tools func(name string, input json.RawMessage)
	// Waiting, when set, is the cards put in front of each turn instead of
	// those waiting in the store: a scenario's.
	Waiting func() []string
}

// Assembly is the agent put together, before anything of it runs (Start).
type Assembly struct {
	Thread    *thread.Store
	Proposals *proposal.Store
	Modules   *module.Registry
	Runner    *claudecode.Runner
	API       *httpapi.Server
	// Mounts is every MCP server this agent serves, by path: the permission
	// gate at /mcp and the modules' servers and the two boards beneath it -
	// those the conversation is given (config.ModuleMCPURLs) and those only
	// a background task is.
	Mounts map[string]http.Handler
	// Boards are the look and change boards, by mount path.
	Boards map[string]*tool.Board
	// Policy is what may not happen, asked before every tool call.
	Policy *policy.Policy

	cfg     *config.Config
	log     *slog.Logger
	records *record.Writer
	// What Start sets running: the modules that run loops of their own, and
	// the loops put together here.
	assets    *assets.Module
	spending  *spending.Module
	jobs      *jobs.Module
	jobsWeb   *jobs.WebTools
	checker   *browsercheck.Checker
	watcher   *memorywatch.Watcher
	suggester *suggest.Engine
	// pool is the queue's database, open from Start to Stop when there is a
	// queue to read; recordsDone closes once the record writer is done.
	pool        *pgxpool.Pool
	stopRecords context.CancelFunc
	recordsDone chan struct{}
}

// Assemble puts the agent together from cfg: it reads what was said and
// decided, builds and ties every module, and adds to cfg the MCP servers the
// conversation is given and the tools it may call without a card. Nothing
// runs until Start.
func Assemble(cfg *config.Config, d Deps) (*Assembly, error) {
	log := d.Log
	if log == nil {
		log = slog.Default()
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
		return nil, err
	}
	if err := proposals.Persist(cfg.ProposalsPath, log); err != nil {
		return nil, fmt.Errorf("제안을 읽지 못했습니다: %w", err)
	}
	// Every card goes up in mind's writer's words, checked against the
	// code's card (internal/words). The guest's agent has no mind to word
	// them: its cards are in the code's words, as are any while mind is down.
	if cfg.CardWords && cfg.MindURL != "" && !cfg.Guest {
		proposals.SetWriter(&words.Writer{URL: cfg.MindURL, Token: cfg.APIToken, Wait: cfg.CardWordsWait, Log: log})
	}

	// What the payment path did, as records a replay can read, and what is
	// asked of mind. The guest's agent has no database: it hands them to
	// input-svc, which takes from it only what is the guest's.
	loops := health.NewLoops()
	records := record.New(cfg.DatabaseURL, loops, log)
	if records == nil {
		records = record.NewHTTP(cfg.NotifyURL, cfg.APIToken, loops, log)
	}
	commands := &mindcmd.Client{URL: cfg.MindURL, Token: cfg.APIToken, Records: records, Actor: map[bool]string{true: "guest", false: "operator"}[cfg.Guest]}

	a := &Assembly{Thread: transcript, Proposals: proposals, Modules: modules, cfg: cfg, log: log, records: records}

	// --- 모듈 -------------------------------------------------------------
	// Adding a feature is a block here; removing one is deleting it.
	// Reading and leaving what changes are two tools, whatever there is to read
	// or change: each module puts its rows on the boards, in the order the
	// modules are added - the order the rows are said in.
	look := tool.New(tool.Look, tool.LookAbout)
	change := tool.New(tool.Change, tool.ChangeAbout)
	calendarStore := calendar.NewStore(cfg.CalendarURL, cfg.APIToken, events)
	calendarModule := calendar.New(calendarStore)
	modules.Add(calendarModule)
	calendarModule.Look(look)

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
		var err error
		if photos, err = uploads.New(cfg.UploadsDir); err != nil {
			return nil, fmt.Errorf("사진 폴더를 열지 못했습니다(%s): %w", cfg.UploadsDir, err)
		}
	}
	if !cfg.Guest {
		assetsModule = assets.New(assets.NewStore(cfg.AssetsURL, cfg.APIToken))
		modules.Add(assetsModule)
		spendingModule = spending.New(spending.NewStore(cfg.MindURL, cfg.APIToken, commands))
		modules.Add(spendingModule)
		assetsModule.Look(look)
		spendingModule.Look(look)

		// Mounted but never put in the chat's MCP config: only the
		// background order lookup is handed this server.
		mcpMounts["/mcp/"+spending.EnrichServerName] = spendingModule.EnrichHandler()

		// 로그인 정보 보관함. 저장은 카드(비밀번호 칸 - 값은 모델을 거치지 않는다),
		// 쓰는 것은 브라우저의 browser_fill_login.
		if cfg.VaultURL != "" {
			vaultModule = vault.New(vault.NewClient(cfg.VaultURL, cfg.APIToken), log)
			modules.Add(vaultModule)
			mcpMounts["/mcp/"+vault.ServerName] = vaultModule.Handler()
			cfg.ModuleMCPURLs[vault.ServerName] = cfg.PublicMCPURL + "/" + vault.ServerName
			vaultModule.Look(look)
		}

	}

	// 브라우저 화면을 사진으로: 작업의 보고(capture), 대화의 show_image(capture). 손님은
	// 브라우저가 없다.
	var camera *shot.Camera
	if u, ok := cfg.ExtraMCPURLs[jobs.BrowserServer]; ok && !cfg.Guest {
		camera = shot.New(u, cfg.ExtraMCPToken)
	}

	// 작업. 대화에서 맡긴 일을 백그라운드에서, 작업이 정한 주기로 한다. 손님의 것은
	// 손님 작업(mind 가 손님 토큰으로 묶는다)이고, 웹사이트도 사진도 없다 - 대화에
	// 첨부한 손님 사진도 작업에는 넘기지 않는다(mind 가 손님 작업의 사진을 받지 않는다).
	if cfg.Jobs && (photos != nil || cfg.Guest) {
		jobPhotos := photos
		if cfg.Guest {
			jobPhotos = nil
		}
		jobsModule = jobs.New(jobs.NewStore(cfg.MindURL+jobs.Prefix, cfg.APIToken).Via(commands), jobPhotos, proposals, transcript, log)
		if cfg.Guest {
			jobsModule.NoBrowser()
		}
		if camera != nil {
			jobsModule.SetCamera(camera)
		}
		modules.Add(jobsModule)
		// A run that buys reads the saved shipping address and raises the
		// vault's cards itself, and reads the card alerts to tell whether a
		// payment went through.
		if vaultModule != nil {
			jobsModule.SetVault(vaultModule)
		}
		if !cfg.Guest {
			jobsModule.SetCharges(chargeLog{store: spending.NewStore(cfg.MindURL, cfg.APIToken, commands)})
		}
		jobsModule.Look(look)
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
		memoryModule.Look(look)
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

	// 사진 보여 주기: 읽은 페이지의 그림, 운영자가 보낸 사진·작업 사진을 대화에 띄운다.
	// 웹 그림은 '본 주소' 장부에 있는 https 주소만 가져온다. 손님에게도 - 손님의 사진 폴더로.
	if photos != nil {
		var seen func(string) bool
		if webModule != nil {
			seen = webModule.Seen
		}
		showModule := show.New(photos, transcript, seen, log)
		if camera != nil {
			showModule.SetCamera(camera)
		}
		modules.Add(showModule)
		mcpMounts["/mcp/"+show.ServerName] = showModule.Handler()
		cfg.ModuleMCPURLs[show.ServerName] = cfg.PublicMCPURL + "/" + show.ServerName
		cfg.Allow(show.Tools...)
	}

	// 사진 보관함(사진 탭): 담긴 것·장소·날로 찾아 그 사진을 대화에서 보여 준다. 운영자의 것이라 손님은 없다.
	var library *photolib.Module
	if cfg.PhotosURL != "" && !cfg.Guest && photos != nil {
		library = photolib.New(cfg.PhotosURL, cfg.MindURL, cfg.APIToken, photos, log)
		modules.Add(library)
		library.Look(look)
	}

	// Where the operator is (context-svc), read only. Not the guest's.
	var placeModule *place.Module
	if cfg.ContextURL != "" && !cfg.Guest {
		placeModule = place.New(cfg.ContextURL, cfg.APIToken)
		if webModule != nil {
			placeModule.UseNaver(webModule.Client())
		}
		modules.Add(placeModule)
		placeModule.Look(look)
	}

	// mind-svc's gate: its cards become approval cards here, and approving
	// one is this module asking mind to do it. The guest has no mind - only
	// their own reminders and checklists, which mind keeps by the guest's
	// token apart from the operator's.
	var mindClient *mindmod.Client
	var checklistModule *checklist.Module
	if cfg.MindURL != "" && !cfg.Guest {
		// An answer to a card runs the thing: it may take as long as the
		// thing does.
		answers := *commands
		answers.Wait = answerWait
		mindModule := mindmod.New(mindmod.Client{URL: cfg.MindURL, Token: cfg.APIToken, Send: &answers})
		modules.Add(mindModule)
		c := mindModule.Client()
		mindClient = &c
		// What mind knows beyond the situation in front of every turn - the
		// loops, the plans, its notes, what it asked and why - and the rules
		// the operator set, which everything that reads mind keeps to.
		mindModule.Look(look)
		mindModule.LookRules(look)
		mindModule.Change(change)
		if jobsModule != nil {
			jobsModule.SetRules(c.RuleLines)
		}

		// The messages themselves (KakaoTalk, iMessage, this conversation), as
		// mind keeps them: for what long-term memory did not take as a fact.
		messagesModule := messages.New(cfg.MindURL, cfg.APIToken)
		modules.Add(messagesModule)
		messagesModule.Look(look)
	}
	if cfg.MindURL != "" {
		// 리마인드: "몇 시에 / 집에 가면 / 나갈 때 알려줘". mind 가 그때 폰으로 알린다.
		remindModule := remind.New(cfg.MindURL, cfg.APIToken, commands)
		// A place that is no region - "수원시에 도착하면" - found on the map, and
		// waited for by the phone's fixes. The guest's are their regions' names.
		if placeModule != nil {
			remindModule.UseMap(placeModule)
		}
		modules.Add(remindModule)
		remindModule.Look(look)
		remindModule.Change(change)

		// 체크리스트: 준비물·할 일. mind 가 체크 상태를 갖고, 대화에는 체크할 수 있는 카드로.
		checklistModule = checklist.New(cfg.MindURL, cfg.APIToken, commands, transcript, events, log)
		modules.Add(checklistModule)
		checklistModule.Look(look)
		checklistModule.Change(change)

		// 목표: 되풀이할 일. mind 가 날마다의 기록을 갖고, 말로 한 것도 탭으로 한 것과 같다.
		goalModule := goal.New(cfg.MindURL, cfg.APIToken, commands)
		modules.Add(goalModule)
		goalModule.Look(look)
		goalModule.Change(change)
	}
	// The boards' servers read the boards as they stand: every module has
	// added its rows by now.
	a.Boards = map[string]*tool.Board{}
	for _, board := range []struct {
		name string
		b    *tool.Board
	}{{tool.Look, look}, {tool.Change, change}} {
		mcpMounts["/mcp/"+board.name] = board.b.Handler()
		a.Boards["/mcp/"+board.name] = board.b
		cfg.ModuleMCPURLs[board.name] = cfg.PublicMCPURL + "/" + board.name
		cfg.Allow(tool.Qualified(board.name))
	}
	if !cfg.Guest {
		// What each feature asks is in the workspace's skills; reading one
		// changes nothing.
		cfg.Allow("Skill")
	}
	a.Mounts = mcpMounts
	a.assets, a.spending, a.jobs = assetsModule, spendingModule, jobsModule

	// The CLI's memory is a working directory (runner args), so it has to
	// exist before the first note is written into it.
	if cfg.MemoryDir != "" {
		if err := os.MkdirAll(cfg.MemoryDir, 0o755); err != nil {
			log.Warn("기억 폴더를 만들지 못했습니다", "dir", cfg.MemoryDir, "err", err)
		}
	}
	runner := claudecode.New(*cfg, transcript, log)
	a.Runner = runner
	// Each tool the conversation calls, said in a line on the console while
	// the turn is under way: where a long search has got to, before a word
	// of the answer.
	runner.OnTool(func(name string, input json.RawMessage) {
		transcript.Step(steps.Line(name, input))
		if d.Tools != nil {
			d.Tools(name, input)
		}
	})
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
	// Every card still waiting is in front of each turn: the operator asks
	// about what is on the screen, whoever raised it.
	waiting := func() []string { return proposals.Waiting(time.Now()) }
	if d.Waiting != nil {
		waiting = d.Waiting
	}
	runner.SetWaiting(waiting)

	// Money taken out of an investment account that no deposit here explains
	// is asked about, one card per withdrawal (assets/ask.go); what was spent
	// reaches the 가계부 through assets-svc.
	if assetsModule != nil && spendingModule != nil && !cfg.Guest {
		assetsModule.Ask(proposals, log)
		assetsModule.Attach(transcript.AttachProposal)
	}
	// A job finds and reads the web with the conversation's own tools - no
	// browser, no site to add.
	if webModule != nil {
		a.jobsWeb = &jobs.WebTools{Name: web.ServerName, URL: cfg.PublicMCPURL + "/" + web.ServerName, Tools: web.Tools}
	}
	// 쇼핑몰 로그인 확인. 브라우저를 쓰는 것은 에이전트뿐이므로, 대화·작업이 없을
	// 때 여기서 연다. 결과는 기록으로 남기고 상태 탭이 그것을 읽는다.
	if u, ok := cfg.ExtraMCPURLs[spending.BrowserServer]; ok && !cfg.Guest {
		a.checker = loginCheck(cfg, runner, vaultModule, spendingModule, u, log)
	}

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
			a.watcher = &memorywatch.Watcher{
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
		}
	}

	// What mind told at its moment, in the conversation as well as on the
	// phone: the reminder's words, why now, the list it carries.
	layNotice := func(n suggest.Notice) {
		if transcript.Told(thread.OriginNotice, n.Key) {
			return
		}
		paragraphs := []string{n.Title}
		if n.Body != "" {
			paragraphs = append(paragraphs, n.Body)
		}
		transcript.Card(thread.Origin{Kind: thread.OriginNotice, Title: n.Why, Ref: n.Key, Checklist: n.Checklist, Goals: n.Goals}, paragraphs)
		if n.Checklist != "" && checklistModule != nil {
			checklistModule.Publish(context.Background(), n.Checklist)
		}
	}
	// Speaking first: mind-svc's gate decides what is worth raising (its
	// ledger, budget and trust), and its cards become approval cards here
	// (internal/suggest). The guest's agent does not suggest - it has none of
	// the operator's data - but the guest's own reminders lay their card in
	// the guest's conversation, as the operator's do in theirs. It reads the
	// guest's effects, which is all its database role can see.
	wakeJobs := func() {
		if jobsModule != nil {
			jobsModule.Wake()
		}
	}
	redrawList := func(id string) {
		if checklistModule != nil {
			checklistModule.Publish(context.Background(), id)
		}
	}
	entryChanged := func(change json.RawMessage) { events.Publish(bus.Event{Type: "calendar", Calendar: change}) }
	if cfg.QueueURL != "" && cfg.Guest {
		a.suggester = suggest.New(proposals, suggest.Config{Guest: true, Notice: layNotice, Redraw: redrawList, Entry: entryChanged, WakeJobs: wakeJobs}, log)
	}
	if cfg.QueueURL != "" && !cfg.Guest {
		a.suggester = suggest.New(proposals, suggest.Config{
			Out:      notify.New(cfg.NotifyURL, cfg.DatabaseURL, cfg.APIToken, log),
			Say:      func(lead, id string) { transcript.Raise([]string{lead}, id) },
			Mind:     mindClient,
			Notice:   layNotice,
			Redraw:   redrawList,
			Entry:    entryChanged,
			WakeJobs: wakeJobs,
			Photo: func(ctx context.Context, id string) (string, error) {
				if library == nil {
					return "", errors.New("사진 보관함이 연결되지 않았습니다")
				}
				name, _, err := library.Keep(ctx, id)
				return name, err
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
		proposals.OnDecide(a.suggester.OnDecide)
		// A calendar card the conversation raises takes the place of mind's
		// waiting card for the same change.
		proposals.OnOpen(a.suggester.OnOpen)
		// And before it goes up, mind says whether the operator turned down the
		// like of it - with what they said just now, the newer word.
		if mindClient != nil {
			suggester := a.suggester
			gate.SetConsult(func(ctx context.Context, act action.Action) string {
				return suggester.Consult(ctx, act, transcript.LastSaid(saidWithin))
			})
		}
	}
	// A card does not hold a turn open; its decision starts the next one.
	gate.SetFollowUp(runner)
	// The guest's file tools reach its own photos and memory and nothing else:
	// a call inside them goes by (the CLI's memory writes need no card of
	// their own anyway), and one anywhere else is refused, never asked.
	var guestFolders policy.Folders
	if cfg.Guest {
		guestFolders = policy.Folders{cfg.UploadsDir, cfg.MemoryDir}
	}
	// A card stands only before a change or money: a job made or instructed
	// (jobs.CardFree) and the browser's own tools - reading, pressing,
	// typing, the PIN at a keypad a payment card already let through - go by.
	// The browser's guard stops a payment on the network and raises its card;
	// a job asks before it posts, sends or deletes.
	gate.SetAutoAllow(func(tool string, input json.RawMessage) bool {
		if cfg.Guest {
			if file, inside := guestFolders.Judge(tool, input); file {
				return inside
			}
		}
		if jobs.BrowserTool(tool) {
			// The conversation is about to drive the browser: a job on it gives
			// way now, and only now - a turn that only talks leaves it be.
			if !runner.TakeBrowser(browserHandover) {
				log.Warn("브라우저를 쓰던 백그라운드 작업이 제때 비켜 주지 않았습니다", "tool", tool)
			}
			return true
		}
		return jobsModule != nil && jobsModule.CardFree(tool, input)
	})
	proposals.OnDecide(gate.Resolve)

	// Every card waiting on someone reaches their phone. The guest's cards are
	// the guest's: they ring the guest's devices (audience guest), never the
	// operator's.
	if notifier := notify.New(cfg.NotifyURL, cfg.DatabaseURL, cfg.APIToken, log); notifier != nil {
		ringCards(cfg, proposals, jobsModule, notifier)
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
	if jobsModule != nil {
		jobPayments = jobsModule
		// A job waiting for the browser starts when a turn or another task
		// gives it up, not on the scheduler's next look.
		runner.OnFree(jobsModule.Wake)
	}

	// What may not happen, asked by the CLI's hook before every tool call
	// and by the gate before a card.
	mounts := make([]string, 0, len(mcpMounts))
	for path := range mcpMounts {
		mounts = append(mounts, path)
	}
	servers := policy.Servers(mounts, slices.Collect(maps.Keys(cfg.ExtraMCPURLs)), permission.ServerName, jobs.RunServerName)
	a.Policy = &policy.Policy{Servers: servers, Workspace: cfg.Workspace, MemoryDir: cfg.MemoryDir, Folders: guestFolders}
	if cfg.BuiltinTools != nil && *cfg.BuiltinTools != "default" {
		a.Policy.Builtins = strings.Split(*cfg.BuiltinTools, ",")
	}

	a.API = httpapi.New(httpapi.Deps{
		Role:            map[bool]string{true: "guest", false: "owner"}[cfg.Guest],
		Thread:          transcript,
		Proposals:       proposals,
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
		Vault:           vaultConsole(vaultModule),
		FollowUp:        runner,
		Jobs:            jobPayments,
		SavedAddress:    savedAddress(vaultModule),
		FlowSitesPath:   cfg.FlowSitesPath,
		Checklists:      checklistConsole(checklistModule),
		Records:         records,
		Loops:           loops,
		Policy:          a.Policy,
		Log:             log,
	})
	gate.SetRefuse(a.API.Refuse)
	// A saved address is good until a move: mind knows of one, the vault
	// holds the address and neither sees the other's half.
	if vaultModule != nil && mindClient != nil {
		slots := *mindClient
		vaultModule.CheckAddress(func(ctx context.Context, saved string) string {
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			got, err := slots.Slot(cctx, "shipping.address", saved)
			if err != nil || got.Holds {
				return ""
			}
			return got.Why
		})
	}
	return a, nil
}

// ringCards has every card waiting on someone reach their phone, every job
// that is over too, and a card decided or put away take its notification off.
func ringCards(cfg *config.Config, proposals *proposal.Store, jobsModule *jobs.Module, notifier *notify.Client) {
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
		if mindmod.KindOf(p.Action) == spending.KindCategory {
			// The book asking what a charge was for: muted with the rest of 가계부.
			kind, title = "spending.ask", "가계부"
		}
		if p.Action.Kind == assets.KindOutflow {
			// Where a withdrawal went: muted with the rest of 자산.
			kind, title = "assets.ask", "자산"
		}
		// What the card is, as its title says it, over the gist of its
		// body; what kind of card it is goes in the kind (the inbox says
		// it, and it is what is muted).
		body := notify.Brief(p.Card.Body, 140)
		if body == "" {
			body = title
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
			Title: notify.Brief(p.Card.Title, 80), Body: body, URL: "/?proposal=" + p.ID, Key: keyPrefix + p.ID,
			Audience: audience,
		})
	})
	// Every job that is over reaches the phone - done, or stopped for
	// failing - whatever the conversation is doing; the thread has its
	// card too. A tap opens it in the 작업 tab.
	if jobsModule != nil {
		endKey := "job-end:"
		if cfg.Guest {
			endKey = "guest-job-end:"
		}
		// Which job and how it ended over the gist of its last word.
		jobsModule.OnEnd(func(e jobs.EndNote) {
			kind, title, level := "job.done", "끝냈어요: ", "info"
			if !e.Done {
				kind, title, level = "job.failed", "멈췄어요: ", "warn"
			}
			notifier.Send(notify.Event{
				Source: "agent", Kind: kind, Tier: "now", Level: level,
				Title: title + notify.Brief(e.Job.Title, 60), Body: notify.Brief(e.Text, 140),
				URL: fmt.Sprintf("/jobs?focus=job:%d", e.Job.ID), Key: fmt.Sprintf("%s%d", endKey, e.Run),
				Audience: audience,
			})
		})
	}
	// Decided, the card's notification is done with: output-svc marks it
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

// Start starts the modules, and then everything of the agent that runs on
// its own until ctx is done: the background order lookup and jobs, the
// withdrawals asked about, the login check, the usage probe, the memory
// watcher, the streams it reads and the record writer.
func (a *Assembly) Start(ctx context.Context) error {
	cfg, log := a.cfg, a.log
	startCtx, cancelStart := context.WithTimeout(ctx, 10*time.Second)
	err := a.Modules.Start(startCtx)
	cancelStart()
	if err != nil {
		return fmt.Errorf("모듈을 시작하지 못했습니다: %w", err)
	}
	if a.suggester != nil {
		pool, err := pgxpool.New(ctx, cfg.QueueURL)
		if err != nil {
			return fmt.Errorf("스트림을 읽을 데이터베이스 풀을 만들지 못했습니다: %w", err)
		}
		a.pool = pool
	}

	// 묶음 가맹점(쿠팡·네이버페이) 결제가 새로 들어왔을 때만 깨어나, 대화와
	// 분리된 세션으로 주문목록을 읽어 상품별로 나눈다.
	if _, ok := cfg.ExtraMCPURLs[spending.BrowserServer]; a.spending != nil && cfg.Enrich && ok {
		go a.spending.RunEnricher(ctx, a.Runner, spending.EnrichConfig{
			EnrichURL: cfg.PublicMCPURL + "/" + spending.EnrichServerName,
			Interval:  cfg.EnrichInterval,
			Cooldown:  cfg.EnrichCooldown,
			Timeout:   cfg.EnrichTimeout,
			Model:     cfg.EnrichModel,
		}, log)
	} else if a.spending != nil && cfg.Enrich {
		log.Warn("브라우저 MCP 가 없어 묶음 결제 조회를 켜지 않습니다", "need", spending.BrowserServer)
	}
	if a.assets != nil && a.spending != nil && !cfg.Guest {
		go a.assets.RunAsker(ctx, assets.AskConfig{Every: 10 * time.Minute, MaxOpen: 12, Categories: a.spending.Categories})
	}
	a.runJobs(ctx)
	if a.checker != nil {
		go a.checker.Run(ctx)
		if a.spending != nil {
			// 네이버페이 주문 내역은 자동으로 열어 보지 않는다(로그인 확인은 쿠팡만 한다).
			go a.checker.Say(ctx, []browsercheck.Quiet{{
				ID: "naverpay", Name: "네이버페이 주문 내역",
				Rule: "네이버페이 주문 내역은 자동으로 열어 보지 않아요(로그인 확인은 쿠팡만 해요). 상품 상세 조회가 로그인을 요구받았다고 기록하면 이상, 아니면 측정하지 않아요.",
				Fix:  "화면 탭에서 에이전트 브라우저로 naver.com에 로그인하세요. '로그인 상태 유지'를 켜야 재시작 뒤에도 남아요.",
			}}, a.spending.Lookups, &checks.Reporter{Source: "agent", Records: a.records})
		}
	}
	// A reading older than an hour is refreshed with the smallest request.
	go a.Runner.WatchUsage(ctx, time.Hour)
	if a.watcher != nil {
		go a.watcher.Run(ctx)
	}
	if a.pool != nil {
		go a.suggester.Consumer(a.pool).Run(ctx)
	}

	// The record writer outlives the loops: it is stopped last (Stop), with
	// what it still holds written.
	recordsCtx, stopRecords := context.WithCancel(context.Background())
	a.stopRecords, a.recordsDone = stopRecords, make(chan struct{})
	go func() {
		defer close(a.recordsDone)
		a.records.Run(recordsCtx)
	}()

	// What is said and how cards are answered come in at the way in, and mind
	// passes them on (mind-chat): they wait on the effects for this agent. A
	// consumer apart from the cards' own, so a card being raised holds up no
	// answer.
	if a.pool != nil {
		talk := &queue.Consumer{Pool: a.pool, Topic: queue.Effects, Name: "agent-chat", Kinds: []string{record.KindMessage, record.KindAnswer}, Handle: a.API.Take, Log: log}
		if cfg.Guest {
			talk.Topic, talk.Name = queue.GuestEffects, "agent-chat-guest"
		}
		go talk.Run(ctx)
	}
	return nil
}

// runJobs starts the jobs' scheduler, on the shared browser held to each
// job's sites - or, for the guest, whose jobs open no website, without it.
func (a *Assembly) runJobs(ctx context.Context) {
	cfg, log := a.cfg, a.log
	if a.jobs == nil {
		return
	}
	if cfg.Guest {
		// The guest's jobs open no website: nothing to hold them to.
		go a.jobs.Run(ctx, a.Runner, jobs.SchedulerConfig{
			RunURL:     cfg.PublicMCPURL + "/job-run",
			Interval:   cfg.JobsInterval,
			Cooldown:   cfg.JobsCooldown,
			Timeout:    cfg.JobsTimeout,
			Model:      cfg.JobsModel,
			RetryModel: cfg.JobsRetryModel,
			Web:        a.jobsWeb,
		})
		return
	}
	u, ok := cfg.ExtraMCPURLs[jobs.BrowserServer]
	if !ok {
		log.Warn("브라우저 MCP 가 없어 작업을 실행하지 않습니다", "need", jobs.BrowserServer)
		return
	}
	guard, err := jobs.NewBridgeGuard(u, cfg.ExtraMCPToken)
	if err != nil {
		log.Error("작업을 사이트 안에 묶을 수 없어 작업을 실행하지 않습니다", "err", err)
		return
	}
	go a.jobs.Run(ctx, a.Runner, jobs.SchedulerConfig{
		RunURL:     cfg.PublicMCPURL + "/job-run",
		Interval:   cfg.JobsInterval,
		Cooldown:   cfg.JobsCooldown,
		Timeout:    cfg.JobsTimeout,
		UploadsDir: cfg.UploadsDir,
		Guard:      guard,
		Model:      cfg.JobsModel,
		RetryModel: cfg.JobsRetryModel,
		Web:        a.jobsWeb,
	})
}

// Stop stops the modules and lets the record writer write what it still
// holds, within ctx; then the queue's database is let go.
func (a *Assembly) Stop(ctx context.Context) {
	_ = a.Modules.Stop(ctx)
	if a.stopRecords != nil {
		a.stopRecords()
		select {
		case <-a.recordsDone:
		case <-ctx.Done():
		}
	}
	if a.pool != nil {
		a.pool.Close()
	}
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
