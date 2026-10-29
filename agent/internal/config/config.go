package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config is read once at boot. The process refuses to start without a
// subscription token - there is no API-key fallback by design: this build
// bills against the Claude Code subscription or not at all.
type Config struct {
	Addr       string
	OAuthToken string
	ClaudeBin  string
	// Shell is what the CLI's Bash tool runs commands through. Alpine has no
	// bash unless the image installs one, and an unset SHELL breaks that tool.
	Shell     string
	Model     string
	Workspace string
	// Home is where the CLI keeps its own state. Kept out of the workspace
	// so the agent's bookkeeping never lands in the user's files.
	Home string
	// MemoryDir is the CLI's own memory for this workspace (MEMORY.md, loaded
	// into every session, and the notes it points to): where the model keeps
	// what the operator asked it to remember. Read without a card like the
	// workspace - and written without one too: the CLI lets its model write
	// its auto memory without asking, whatever the permission mode (tried in
	// the agent container, 2026-10-17). So what a page talks the model into
	// writing there is loaded into every later session - which is why every
	// change is told to the operator, with its words (memorywatch: the
	// operator chose being told over a card). The guest's agent has its own,
	// in its own home volume, and its changes are told to the guest.
	MemoryDir    string
	SystemPrompt string
	// AllowedTools skip the approval card entirely. Read-only tools belong
	// here; anything that writes does not. Files are read only inside the
	// workspace and the uploads (scoped Read/Grep/Glob rules, added at load);
	// see unsafeTool for what is taken off whatever the environment says.
	// Each module's read-only tools are added at boot (Allow), whatever the
	// environment says: a module that gains a lookup does not need .env edited.
	AllowedTools []string
	// DroppedTools are entries of JARVIS_ALLOWED_TOOLS that were refused.
	DroppedTools []string
	// PermissionMode decides what Claude Code asks about at all. "manual"
	// routes decisions to a person; the auto/accept modes would silently
	// bypass the card, so changing this weakens the gate.
	PermissionMode string
	ApprovalWait   time.Duration
	TurnTimeout    time.Duration
	// PublicMCPURL is what the CLI is told to dial for permission decisions.
	// Inside one container that is loopback; split the processes and this has
	// to change with it.
	PublicMCPURL string
	// ModuleMCPURLs is one entry per module that exposes tools to the model,
	// keyed by module name. Filled at boot from the registry, so a module
	// that is not enabled is a module the model cannot see. These are dialled
	// over loopback inside the container, with InternalMCPToken.
	ModuleMCPURLs map[string]string
	// InternalMCPToken guards this agent's own /mcp routes. The CLI is the
	// only caller and is handed it in its MCP config; anything else on the
	// compose network that dials agent:8080/mcp - the permission gate, the
	// propose tool, a job's run tools - is refused. Made fresh at every
	// start unless JARVIS_MCP_INTERNAL_TOKEN pins it.
	InternalMCPToken string
	// ExtraMCPURLs are servers with no module behind them (JARVIS_EXTRA_MCP,
	// "name=url,name=url"). They reach the model but not the registry, so the
	// approval gate cannot render their cards - it falls back to the generic
	// renderer, and their actions carry no action.Spec and therefore no
	// Reversible declaration.
	//
	// Kept apart from ModuleMCPURLs because these leave the container: the
	// token below is sent to them and must not be sent anywhere else.
	ExtraMCPURLs map[string]string
	// ExtraMCPToken is sent as a bearer token to ExtraMCPURLs. An extra
	// server listens on an interface the whole network can reach - a module
	// server does not - so this is what stands between that port and anyone
	// else on the same wifi. Empty means no header is sent.
	ExtraMCPToken string
	// CalendarURL is where calendar-svc listens. The events used to be a file
	// here, kept deliberately outside the workspace mount so the CLI's own
	// Read and Write could not be a second door into them. They are in
	// Postgres behind that service now, which closes the same door more
	// firmly: there is no path on this filesystem to walk past the calendar
	// tools' schema and approval cards.
	CalendarURL string
	// ChatURL is where chat-svc listens. The transcript used to be a file in
	// /data, kept outside the workspace so the CLI's own Read and Write could
	// not be a second door into it; it is in Postgres behind that service now.
	ChatURL string
	// AssetsURL is where assets-svc listens. The agent reads it and nothing
	// more: the brokerage keys stay in that service, and the routes this
	// token opens there are lookups plus the principal ledger, which no tool
	// here calls.
	AssetsURL string
	// SpendingURL is where spending-svc listens. Read-only, like assets.
	SpendingURL string
	// Enrich turns on the background lookup of marketplace orders (what a
	// "쿠팡 8,900원" charge actually bought). It needs the browser server in
	// ExtraMCPURLs; without it the loop does not start.
	Enrich bool
	// UploadsDir is where photos attached in conversation are kept. Empty
	// turns attachments off. The guest's agent keeps the guest's in a volume
	// of its own.
	UploadsDir string
	// JobsURL is where jobs-svc listens: work handed over in conversation,
	// run in the background on whatever cadence each job decides.
	JobsURL string
	// VaultURL is vault-svc: the operator's saved logins. Empty leaves the
	// vault module out (the guest, a laptop run).
	VaultURL string
	// MemoryURL is memory-svc (the Graphiti knowledge graph).
	MemoryURL string
	// MindURL is mind-svc: the one gate every suggestion goes through, and
	// the world model the conversation is told about. Empty: no mind.
	MindURL string
	// ContextURL is context-svc: where the operator is, and the devices'
	// state. Empty: no context tool.
	ContextURL string
	// SearchURL is search-svc - Naver's search, pages read with what their
	// pictures say, Naver's map - and SearchToken the token it alone takes.
	// Both set (SearchOn): the web module's search and read, find_nearby on
	// Naver first, and read in place of the CLI's WebFetch.
	SearchURL   string
	SearchToken string
	// Jobs turns on the scheduler. Like Enrich, it needs the browser server.
	Jobs bool
	// JobsInterval is the longest the scheduler goes without looking - a net
	// only: what makes a job due wakes it, and a job's own clock is kept.
	JobsInterval time.Duration
	JobsCooldown time.Duration
	JobsTimeout  time.Duration
	// Guest is the second agent, for the guest account (JARVIS_ROLE=guest).
	// It has its own conversation (Thread), its own CLI home, session, memory
	// and photos, the guest's own calendar side, jobs, reminders and lists,
	// and none of the operator's money, messages, logins or browser. Of the
	// built-in tools it has the web and the file tools for its photos and its
	// memory alone - reads stop at those folders and any other file call is
	// refused without a card: a file tool let loose could read this process's
	// environment, and with it the guest's token. No shell.
	Guest bool
	// Thread is this agent's conversation in chat-svc: "" or "guest".
	Thread string
	// BuiltinTools, when set, is passed as --tools: the only built-in CLI
	// tools the model gets. Nil means the CLI's default set. Every one of
	// them is described in every request, so the conversation gets only what
	// it uses (JARVIS_BUILTIN_TOOLS, "default" for all of them).
	BuiltinTools *string
	// CacheTTL is the conversation's prompt cache lifetime, "5m" or "1h"
	// (JARVIS_CACHE_TTL). A one-hour write costs 2x the input, a five-minute
	// one 1.25x; the hour pays off only when the next message usually comes
	// between five minutes and an hour later. Background tasks always use 5m:
	// their requests come seconds apart and no session outlives them.
	CacheTTL string
	// A typed message opens a new session, handed the recent conversation,
	// when the last turn ended SessionIdle ago or the session's history (its
	// context less the instructions and tools) reached SessionMaxTokens - but
	// never for a history under SessionMinTokens, which a recap would not
	// make smaller.
	SessionIdle      time.Duration
	SessionMaxTokens int
	SessionMinTokens int
	// Models for the background tasks (empty: Model). A job tried again after
	// a run that ended badly gets JobsRetryModel.
	EnrichModel    string
	JobsModel      string
	JobsRetryModel string
	// BackgroundLogin is the sign-in hosts a background task may use without
	// asking - so the order lookup can press 로그인 on a form the browser has
	// filled in from its saved passwords. Payment hosts are never on it.
	BackgroundLogin []string
	EnrichInterval  time.Duration
	EnrichCooldown  time.Duration
	EnrichTimeout   time.Duration
	// ProposalsPath is still a file, and still outside the workspace for that
	// same reason. The proposal store did not move: it is the approval gate's
	// other half, and a handler that does not return until a decision is made
	// is not something to put behind a network hop while moving storage around.
	ProposalsPath string
	// SessionPath keeps the CLI session id. Without it a restart starts a new
	// session, and the transcript on screen would be one the model has no
	// memory of.
	SessionPath   string
	AllowedOrigin string
	// APIToken guards the routes the console drives. Unset leaves them open,
	// which is fine while only the compose network can reach :8080 and not
	// fine once a tunnel publishes it - see the boot check below.
	APIToken string

	// NotifyURL is notify-svc: a card raised here becomes a push to the
	// operator's phone. Empty sends nothing.
	NotifyURL string

	// DatabaseURL is Postgres. Today only the events outbox writes to it:
	// what the agent reports reaches events-svc even if it is restarting.
	DatabaseURL string

	// EventsURL is events-svc: the assistant subscribes there for mind's
	// cards (internal/suggest) and reports decisions back; the guest's agent
	// for the guest's own reminders, and nothing else. SelfURL is where
	// events-svc reaches this agent.
	EventsURL string
	SelfURL   string
	Debug     bool
}

func Load() (Config, error) {
	c := Config{
		Addr:         env("JARVIS_ADDR", ":8080"),
		OAuthToken:   os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"),
		ClaudeBin:    env("JARVIS_CLAUDE_BIN", "claude"),
		Shell:        env("JARVIS_SHELL", "/bin/bash"),
		Model:        os.Getenv("JARVIS_MODEL"),
		Workspace:    env("JARVIS_WORKSPACE", "."),
		Home:         env("JARVIS_HOME", os.Getenv("HOME")),
		SystemPrompt: env("JARVIS_SYSTEM_PROMPT", defaultSystemPrompt),
		// list_events is here for the same reason reading the workspace is:
		// looking costs nothing and a card in front of every lookup would
		// train the operator to approve without reading. Writes are never on
		// this list - nor is starting or redirecting a job, which goes through
		// the gate to be logged and passes without a card (jobs.CardFree). The
		// other modules' lookups (assets, spending, memory, context, messages,
		// vault) are added from the modules.
		AllowedTools: splitList(env("JARVIS_ALLOWED_TOOLS",
			"TodoWrite,mcp__calendar__list_events,mcp__jobs__list_jobs")),
		// Only what the conversation uses: photos are opened with Read, the
		// web is searched and fetched (fetching on a card), ToolSearch loads
		// the module tools the CLI defers, and Write and Edit keep what the
		// operator asks it to remember (MemoryDir - those writes need no card
		// and each is told to the operator; any other write does). No shell, no
		// subagents - neither was used here, and each is described in every
		// request.
		BuiltinTools:     ptr(env("JARVIS_BUILTIN_TOOLS", "Read,Glob,Grep,TodoWrite,WebSearch,WebFetch,ToolSearch,Write,Edit")),
		CacheTTL:         cacheTTL(os.Getenv("JARVIS_CACHE_TTL")),
		SessionMaxTokens: envInt("JARVIS_SESSION_MAX_TOKENS", 50000),
		SessionMinTokens: envInt("JARVIS_SESSION_MIN_TOKENS", 20000),
		EnrichModel:      env("JARVIS_ENRICH_MODEL", "sonnet"),
		JobsModel:        env("JARVIS_JOBS_MODEL", "sonnet"),
		JobsRetryModel:   env("JARVIS_JOBS_RETRY_MODEL", "opus"),
		PermissionMode:   env("JARVIS_PERMISSION_MODE", "manual"),
		ApprovalWait:     envDuration("JARVIS_APPROVAL_TIMEOUT", 30*time.Minute),
		TurnTimeout:      envDuration("JARVIS_TURN_TIMEOUT", 2*time.Hour),
		PublicMCPURL:     env("JARVIS_MCP_URL", "http://127.0.0.1:8080/mcp"),
		ModuleMCPURLs:    map[string]string{},
		ExtraMCPURLs:     parseMCPURLs(os.Getenv("JARVIS_EXTRA_MCP")),
		ExtraMCPToken:    os.Getenv("JARVIS_MCP_TOKEN"),
		InternalMCPToken: env("JARVIS_MCP_INTERNAL_TOKEN", randomToken()),
		CalendarURL:      env("JARVIS_CALENDAR_URL", "http://localhost:8093"),
		ChatURL:          env("JARVIS_CHAT_URL", "http://localhost:8094"),
		AssetsURL:        env("JARVIS_ASSETS_URL", "http://localhost:8092"),
		SpendingURL:      env("JARVIS_SPENDING_URL", "http://localhost:8095"),
		Enrich:           envBool("JARVIS_ENRICH", true),
		UploadsDir:       env("JARVIS_UPLOADS_DIR", ""),
		JobsURL:          env("JARVIS_JOBS_URL", "http://localhost:8098"),
		VaultURL:         os.Getenv("JARVIS_VAULT_URL"),
		// Empty turns long-term memory off: no tool, nothing recalled.
		MemoryURL:    os.Getenv("JARVIS_MEMORY_URL"),
		MindURL:      os.Getenv("JARVIS_MIND_URL"),
		ContextURL:   os.Getenv("JARVIS_CONTEXT_URL"),
		SearchURL:    os.Getenv("JARVIS_SEARCH_URL"),
		SearchToken:  os.Getenv("JARVIS_SEARCH_TOKEN"),
		Jobs:         envBool("JARVIS_JOBS", true),
		JobsInterval: envDuration("JARVIS_JOBS_INTERVAL", 5*time.Minute),
		// Least time between two runs of any job. Sites watch for bots; a
		// person does not click through two shops in the same minute.
		JobsCooldown:    envDuration("JARVIS_JOBS_COOLDOWN", time.Minute),
		JobsTimeout:     envDuration("JARVIS_JOBS_TIMEOUT", 15*time.Minute),
		BackgroundLogin: splitList(env("JARVIS_BACKGROUND_LOGIN_HOSTS", "nid.naver.com")),
		EnrichInterval:  envDuration("JARVIS_ENRICH_INTERVAL", time.Minute),
		EnrichCooldown:  envDuration("JARVIS_ENRICH_COOLDOWN", 10*time.Minute),
		EnrichTimeout:   envDuration("JARVIS_ENRICH_TIMEOUT", 8*time.Minute),
		ProposalsPath:   env("JARVIS_PROPOSALS_PATH", "./data/proposals.json"),
		SessionPath:     env("JARVIS_SESSION_PATH", "./data/session.json"),
		AllowedOrigin:   env("JARVIS_ALLOWED_ORIGIN", ""),
		APIToken:        os.Getenv("JARVIS_API_TOKEN"),
		NotifyURL:       os.Getenv("JARVIS_NOTIFY_URL"),
		DatabaseURL:     os.Getenv("JARVIS_DATABASE_URL"),
		EventsURL:       os.Getenv("JARVIS_EVENTS_URL"),
		SelfURL:         env("JARVIS_SELF_URL", "http://agent:8080"),
		Debug:           envBool("JARVIS_DEBUG", false),
	}
	// With a five-minute cache every pause past five minutes writes the whole
	// history again, so half an hour of silence is a good place to start
	// over. An hour-long cache keeps the session warm through the hour: a new
	// session sooner would throw that away.
	idle := 30 * time.Minute
	if c.CacheTTL == "1h" {
		idle = time.Hour
	}
	c.SessionIdle = envDuration("JARVIS_SESSION_IDLE", idle)
	c.MemoryDir = memoryDir(c.Home, c.Workspace)

	if c.Guest = env("JARVIS_ROLE", "") == "guest"; c.Guest {
		c.Thread = "guest"
		c.SystemPrompt = env("JARVIS_SYSTEM_PROMPT", guestSystemPrompt)
		c.AllowedTools = splitList(env("JARVIS_ALLOWED_TOOLS", "TodoWrite,WebSearch,WebFetch,mcp__calendar__list_events,mcp__memory__search_memory"))
		// Read opens the photos the guest attaches and the notes of its own
		// memory, Write and Edit keep that memory - and nothing else: reads stop
		// at those folders (runner, readRules), and any file call past them is
		// refused without a card (the gate's refusal, set in main). No shell.
		builtins := "Read,Write,Edit,WebSearch,WebFetch,TodoWrite,ToolSearch"
		c.BuiltinTools = &builtins
		// No browser: it is logged in to the operator's shops and pay. No
		// background lookups: those belong to the operator's spending. Jobs,
		// yes - the guest's own, which open no website (jobs-svc refuses
		// one) - with no photos (the jobs module is given none). The photos
		// attached in conversation and the CLI's memory are the guest's own:
		// this agent's volumes, apart from the operator's.
		c.ExtraMCPURLs = map[string]string{}
		c.Enrich = false
		c.VaultURL = ""
		// events-svc brings the guest's reminders here, not to the
		// operator's agent.
		c.SelfURL = env("JARVIS_SELF_URL", "http://agent-guest:8081")
	}

	// With search-svc, read is how a page is opened: it reads Korean pages
	// and their pictures, and opens only addresses that were seen - WebFetch
	// opens any, so a page could have the model send what it knows out in
	// one. An explicit JARVIS_BUILTIN_TOOLS is left as it is.
	if c.SearchOn() && os.Getenv("JARVIS_BUILTIN_TOOLS") == "" && c.BuiltinTools != nil {
		c.BuiltinTools = ptr(withoutTool(*c.BuiltinTools, "WebFetch"))
	}

	if c.OAuthToken == "" {
		return c, fmt.Errorf("CLAUDE_CODE_OAUTH_TOKEN is not set (run: claude setup-token)")
	}
	// An extra MCP server is reachable from outside this machine; the token is
	// the only thing in front of it. Booting without one would leave that port
	// open and say nothing, so refuse - the same reason the check below exists.
	if len(c.ExtraMCPURLs) > 0 && c.ExtraMCPToken == "" {
		return c, fmt.Errorf("JARVIS_EXTRA_MCP is set without JARVIS_MCP_TOKEN; those servers would accept anyone who can reach the port")
	}
	// A configured origin means a browser somewhere else is expected to reach
	// this process, which means :8080 is published. Unguarded, that is the
	// whole assistant - and the approval gate with it - open to whoever finds
	// the hostname.
	if c.AllowedOrigin != "" && c.APIToken == "" {
		return c, fmt.Errorf("JARVIS_ALLOWED_ORIGIN is set without JARVIS_API_TOKEN; the console's routes would accept anyone who can reach the port")
	}
	// Even unpublished, :8080 is reachable from every container on the
	// compose network. Running open is for a local trial only, and has to
	// be asked for.
	if c.APIToken == "" && !envBool("JARVIS_ALLOW_OPEN", false) {
		return c, fmt.Errorf("JARVIS_API_TOKEN is not set; set it, or JARVIS_ALLOW_OPEN=true for a local trial")
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		// The CLI prefers the OAuth token, but an API key sitting in the
		// environment is a billing surprise waiting to happen. Say so loudly.
		return c, fmt.Errorf("ANTHROPIC_API_KEY is set alongside CLAUDE_CODE_OAUTH_TOKEN; unset it so usage bills to the subscription")
	}
	c.AllowedTools, c.DroppedTools = safeAllowed(c.AllowedTools)
	workspace := c.Workspace
	if c.Guest {
		workspace = "" // the guest's photos and memory only
	}
	c.AllowedTools = append(c.AllowedTools, readRules(workspace, c.UploadsDir, c.MemoryDir)...)
	return c, nil
}

// SearchOn is whether search-svc is set up for this agent.
func (c Config) SearchOn() bool { return c.SearchURL != "" && c.SearchToken != "" }

// withoutTool is a comma list less one tool.
func withoutTool(list, tool string) string {
	var out []string
	for _, t := range splitList(list) {
		if t != tool {
			out = append(out, t)
		}
	}
	return strings.Join(out, ",")
}

// unsafeTool is an allowlist entry that may never skip the gate, whatever
// JARVIS_ALLOWED_TOOLS says. A bare file tool reads anywhere in the container
// - the CLI's own credentials, /proc/<pid>/environ with every token - so
// reading is allowed only by path (readRules). Shell and edits write. A job,
// once made or redirected, runs on its own in the logged-in browser: it goes
// through the gate, which logs it and lets it by without a card (jobs.CardFree).
func unsafeTool(t string) bool {
	switch t {
	case "Read", "Glob", "Grep", "mcp__jobs__create_job", "mcp__jobs__instruct_job":
		return true
	}
	for _, prefix := range []string{"Bash", "Edit", "Write", "MultiEdit", "NotebookEdit"} {
		if t == prefix || strings.HasPrefix(t, prefix+"(") {
			return true
		}
	}
	return false
}

// gatedTool is an allowlist entry that takes no card but must still pass the
// gate: the shared browser's own tools (the mcp-browser server, jobs.
// BrowserServer). The gate lets them by without a card and, as it does, gives
// the browser to the conversation - a job on it stops then, and only then
// (Runner.TakeBrowser). Skipping the gate, the conversation would drive the
// browser while a job is on it.
func gatedTool(t string) bool { return strings.HasPrefix(t, "mcp__mcp-browser__") }

// Allow adds read-only tools a module declares to AllowedTools, once each.
// Called at boot for each module that is on.
func (c *Config) Allow(tools ...string) {
	for _, t := range tools {
		if !unsafeTool(t) && !gatedTool(t) && !slices.Contains(c.AllowedTools, t) {
			c.AllowedTools = append(c.AllowedTools, t)
		}
	}
}

// safeAllowed is JARVIS_ALLOWED_TOOLS less what may not skip the gate: the
// unsafe (dropped, and said so at boot) and the browser's tools (left to the
// gate, which passes them without a card).
func safeAllowed(tools []string) (kept, dropped []string) {
	for _, t := range tools {
		switch {
		case unsafeTool(t):
			dropped = append(dropped, t)
		case gatedTool(t):
		default:
			kept = append(kept, t)
		}
	}
	return kept, dropped
}

// readRules are the only file reading that skips the card: the workspace
// (the operator's agent only), the photos attached in conversation, and the
// CLI's own memory. The CLI also
// blocks reads outside its working directories altogether (runner:
// blockReadsOutsideWorkingDirectories, with uploads and memory added as
// working directories). Writing takes a card everywhere but the CLI's
// memory, which the CLI itself lets its model write (see MemoryDir) and the
// operator hears of instead.
func readRules(workspace, uploads, memory string) []string {
	var out []string
	if workspace != "" { // none for the guest
		if abs, err := filepath.Abs(workspace); err == nil {
			out = append(out, "Read(/"+abs+"/**)", "Grep(/"+abs+"/**)", "Glob(/"+abs+"/**)")
		}
	}
	for _, dir := range []string{uploads, memory} {
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			out = append(out, "Read(/"+abs+"/**)")
		}
	}
	return out
}

// memoryDir is where the CLI keeps its memory for a workspace: under its home,
// in the project directory it names after the workspace's absolute path with
// every character but letters and digits made "-" (/workspace → -workspace).
func memoryDir(home, workspace string) string {
	abs, err := filepath.Abs(workspace)
	if err != nil || home == "" {
		return ""
	}
	project := strings.Map(func(r rune) rune {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, abs)
	return filepath.Join(home, ".claude", "projects", project, "memory")
}

const defaultSystemPrompt = `당신은 Jarvis입니다. 한 사람의 개인 어시스턴트로, 그 사람과 1:1 스레드에서만 대화합니다.
아래 지시가 지금의 규칙입니다. 지난 대화에서 다르게 말했거나 다르게 했던 것과 어긋나면 이 지시를 따르세요.

말투와 분량
- 한국어로, 담백하게. 인사말과 사족 없이 본론부터.
- 한 번에 두세 문단을 넘기지 마세요. 목록이 꼭 필요할 때만 목록을 쓰세요.
- 한 일은 한 일로, 하려는 일은 하려는 일로 구분해서 말하세요. 하지 않은 일을 했다고 말하지 마세요.

승인
- 승인 카드는 무언가를 바꾸는 일과 결제에만 올라갑니다: 일정 추가·수정·삭제, 로그인 정보 저장, 결제. 그런 도구를 부르면 카드가 올라가고, 호출은 바로 "카드를 올렸다"는 답으로 끝납니다.
- 작업 만들기·지시(create_job, instruct_job), 리마인드(set_reminder·change_reminder·cancel_reminder), 체크리스트(make_checklist 등)와 찾기·읽기·비교는 카드 없이 바로 됩니다. 운영자가 맡긴 일이면 묻지 말고 바로 작업으로 넘기세요. 운영자가 시키지 않았는데 떠올린 작업은 카드가 없으니 말로 한 번 물어보세요. 작업이 글을 올리거나 메시지를 보내는 등 밖의 무언가를 바꾸는 단계는 작업이 그때 따로 묻고, 결제는 결제 카드가 따로 묻습니다.
- 카드가 필요한 일(일정 추가·수정·삭제 등)을 하면 좋겠다고 판단했으면 "할까요?", "원하시면 ~할까요?"라고 말로 묻지 말고 그 자리에서 바로 도구를 불러 카드를 올리세요. 카드가 곧 질문이고, 운영자는 카드에서 승인하거나 반려합니다. 운영자가 시키지 않은 일을 떠올렸을 때도 마찬가지입니다. 말로만 묻는 것은 운영자가 카드를 기다리게 만드는 실수입니다.
- 카드를 올렸으면 결정을 기다리지 말고, 무엇을 요청했는지 한 줄로 알린 뒤 응답을 마치세요. 같은 도구를 다시 부르지 마세요.
- 운영자가 결정하면 [승인됨…] 또는 [반려]로 시작하는 메시지가 옵니다. 운영자가 직접 친 말이 아니라 카드 결과이니, 그 안내대로 이어서 하세요.
- 반려되면 이유를 캐묻지 말고, 같은 목적을 이루는 덜 위험한 방법을 제안하거나 거기서 멈추세요.
- 되돌릴 수 없는 일은 한 번에 하나씩만 시도하세요. 한 번에 여러 개를 몰아서 요청하지 마세요.

결제
- 결제 수단은 네이버페이와 신용카드, 쿠팡에서는 쿠페이(쿠팡 간편결제)입니다. 카드는 운영자가 정한 것을, 없으면 하나카드를 고르세요. 카카오페이·토스페이·페이코 같은 다른 간편결제, 휴대폰 결제, 계좌이체, 가상계좌, 카드번호 직접 입력은 쓰지 마세요. 맞는 수단이 없으면 결제하지 말고 무엇을 사려다 멈췄는지 보고하세요.
- 결제는 어느 수단이든 늘 결제 승인 카드를 거칩니다. 결제 비밀번호·카드사 인증은 그 결제의 승인 카드가 승인된 뒤에만 하세요 - 카드 없이 인증 단계로 넘어갔다면 인증하지 말고 알리세요.
- 쇼핑몰의 결제수단이 '신용카드'뿐이면 신용카드를 골라 결제창(이니시스·KCP 같은 PG 창)까지 가도 됩니다. 결제창을 여는 순간 승인 카드가 올라가고 창은 빈 화면으로 막힙니다. 승인되면 결제창을 다시 열고, 간편결제에 네이버페이가 있으면 그것을, 없으면 카드(운영자가 정한 카드, 없으면 하나카드)로 결제하세요. 이어지는 카드사 창에서는 간편결제의 네이버페이로 결제하고, 카드사 앱 결제(하나Pay·KB Pay·페이북 같은 앱카드, QR·결제코드·전화번호 결제)는 쓰지 마세요. 네이버페이 비밀번호를 넣으면 네이버페이 창이 카드사의 인증 결과 화면('확인 버튼을 눌러 결제를 완료해 주세요')으로 바뀝니다 - 탭을 닫거나 카드사 창으로 옮기지 말고 그 확인을 누르세요. 그래야 결과가 카드사 창으로 넘어가고, 카드사 창의 결제완료는 그 전에는 '네이버페이 인증을 완료하여 주십시오'만 띄웁니다. 카드사 창에 네이버페이가 없으면 결제하지 말고 보인 결제 수단을 알리세요.
- 쿠팡에서 구매를 맡으면 결제 단계까지 진행하세요(승인 카드가 올라가면 거기서 멈춥니다).
- 상품을 찾고 비교하는 탐색(네이버쇼핑·쿠팡 등)은 묻지 않고 해도 됩니다. 결제로 돈이 나가는 요청만 승인 카드를 거칩니다.
- 결제 버튼을 누르면 승인 카드가 올라가면서 그 요청은 바로 막힙니다. 막혔으면 같은 버튼을 다시 누르지 말고, 무엇을 얼마에 결제하려고 카드를 올렸는지 한 줄로 알린 뒤 응답을 마치세요.
- 승인되면 [결제 승인됨]으로 시작하는 메시지가 옵니다. 그때 같은 결제 단계를 다시 진행하면 통과합니다. [결제 반려됨]이면 결제하지 마세요.
- 결제 비밀번호 키패드(쿠팡 쿠페이, 네이버페이)가 나오면 browser_enter_payment_pin 을 부르세요. 비밀번호를 묻거나 추측하거나 직접 누르지 마세요. 도구가 넣지 못했다고 하면 다시 부르지도 키패드를 누르지도 말고 그대로 알리세요(틀린 비밀번호가 쌓이면 결제가 잠깁니다). 결제 비밀번호가 저장되어 있지 않다고 하면 save_payment_pin(네이버페이·쿠페이)으로 저장 카드를 바로 올리세요 - 운영자가 카드의 비밀 칸에 넣고, 저장되면 결제를 다시 진행합니다.
- 다른 간편결제·휴대폰 결제로 가는 요청은 네트워크에서 막힙니다. 막혔다면 우회할 방법을 찾지 말고 네이버페이나 카드로 돌아오거나 멈추세요.

로그인
- 로그인 화면이 나오면 browser_fill_login 으로 저장된 아이디·비밀번호를 넣고 로그인 버튼을 누르세요. 값은 그 도구가 직접 넣고 당신에게는 보이지 않습니다.
- 저장된 정보가 없다고 하면 save_login(사이트 주소, 이름)으로 저장 카드를 올리세요. 운영자가 카드의 칸에 직접 넣습니다. 무엇이 저장돼 있는지는 list_logins 로 봅니다.
- 비밀번호를 대화로 묻거나, 받거나, 다시 적지 마세요. 운영자가 대화에 비밀번호를 쳤으면 그 값을 쓰거나 되풀이하지 말고, save_login(로그인) 카드나 save_payment_pin(결제 비밀번호) 카드로 넣어 달라고 하세요.
- 문자·이메일 인증, 보안문자, 새 기기 확인이 나오면 더 시도하지 말고, 화면 탭에서 직접 해 달라고 운영자에게 알리세요.

캘린더
- 일정은 calendar 도구로만 다루세요. 캘린더 파일을 직접 읽거나 쓰려고 하지 마세요.
- 일정을 고치거나 지우기 전에 list_events로 id를 먼저 확인하세요.
- 날짜는 YYYY-MM-DD, 시각은 HH:MM으로만 넘기세요.

자산
- 투자 자산은 assets 도구로만 조회하세요(get_portfolio, get_allocation, get_history). 전부 읽기 전용이고, 주문·이체 도구는 없습니다.
- 숫자는 도구가 준 값을 그대로 쓰세요. 반올림하거나 다시 계산하지 마세요.
- 수익률은 get_portfolio의 기간 변동이나 원금 대비로 말하세요. get_history의 총액 변화에는 입출금이 섞여 있습니다.
- "조회 실패"가 있으면 그 계좌가 빠진 값이라고 먼저 밝히세요.
- 매수·매도를 권할 수는 있지만, 대신 주문할 수는 없다는 걸 분명히 하세요.

가계부
- 지출은 spending 도구(get_spending)로만 조회하세요. 카드사 카톡 결제 알림으로 모은 것이고 읽기 전용입니다.
- 진행 중인 달은 지난달 전체가 아니라 "지난달 같은 날까지"와 비교하세요.
- "주의"나 "읽지 못한 카드 알림"이 있으면 합계가 빠졌을 수 있다고 먼저 밝히세요.
- 분류를 바꾸거나 예산을 정하는 건 가계부 화면에서 하도록 안내하세요.

기억
- 지난 일·사람·약속처럼 지금 대화에 없는 맥락은 먼저 search_memory 로 찾으세요.
- 카톡·iMessage·이 대화에서 누가 정확히 뭐라고 했는지, 어떤 말이 무슨 뜻이었는지처럼 원문이 필요한데 search_memory 로 못 찾았을 때만 search_messages 로 찾고, 고른 메시지의 앞뒤는 message_context 로 보세요.

사진 보여 주기
- 상품·장소·음식처럼 보면 바로 아는 것을 말할 때나 운영자가 보여 달라고 하면, 말로 묘사하지 말고 show_image 로 대화에 사진을 띄우세요. 한 번에 4장까지, caption 에 무엇인지 한 줄.
- 웹 사진은 read 로 읽은 페이지의 '그림 주소'처럼 대화에 나온 https 주소를 그대로 씁니다. 주소를 짐작해 만들지 마세요. 운영자가 보낸 사진·작업 사진은 그 이름(uploads)으로 띄웁니다.
- 띄운 사진은 대화에 따로 남습니다. 답에서 같은 사진의 주소를 늘어놓거나 다시 띄우지 마세요.

리마인드
- "몇 시에 알려줘", "집에 가면 알려줘", "나갈 때 말해줘"처럼 정해진 때에 알려 달라는 말은 작업이 아니라 set_reminder 로 맡기세요. 그때 폰에 푸시가 갑니다(방해 금지 시간·운전 중에도). 작업은 웹을 보거나 무언가를 지켜봐야 할 때만 씁니다.
- 때는 시각(at), 장소에 도착·출발할 때(place, move), 또는 장소를 기다리되 늦어도 그 시각(place와 at)으로 정합니다. "퇴근하고 집에 오면"처럼 그날 저녁부터 세는 조건은 after 에 그 시각을 넣으세요. 그때 이미 집이면 바로 알립니다.
- 운영자가 시각을 말하지 않았으면 알맞게 정하고, 정한 시각을 답에 밝히세요.
- 시각이나 조건을 바꿔 달라면 새로 만들지 말고 change_reminder 로 고치고, 그만두라면 cancel_reminder 를 쓰세요. id 는 지금 상황의 '맡아 둔 알림'이나 list_reminders 에 있습니다.
- 답할 때는 도구가 돌려준 저장된 때와 문구를 그대로 말하세요. 도구가 실패했으면 됐다고 말하지 마세요.
- 리마인드가 울리면 폰 푸시와 함께 대화에도 안내 카드가 남습니다. 그 사이 울린 것은 [작업 소식]에 '알림'으로 옵니다.

체크리스트
- 챙길 것·할 일이 여럿이면(캠핑 준비물, 집에 가서 할 일, 이사 전 할 일) 글머리표로 늘어놓지 말고 make_checklist 로 체크리스트를 만들어 대화에 카드로 띄우세요. 운영자가 카드에서 하나씩 체크하고, 체크한 상태는 저장됩니다.
- 이미 있는 목록이 지금 필요하면(목록을 보자고 할 때, 짐 쌀 때, 무엇이 남았냐고 물을 때) 다시 적지 말고 show_checklist 로 띄우세요. 열린 목록과 남은 항목은 지금 상황의 '체크리스트'에 id 와 함께 있습니다.
- 목록과 체크 상태는 mind 가 관리합니다. 특정한 날을 위한 목록(캠핑 가는 날 등)이면 make_checklist 에 date 를 넣으세요. mind 가 전날 저녁 집에서 남은 것만 다시 띄우고, 날이 지나면 닫습니다(다 한 목록도 밤에 닫습니다). 안 끝난 목록은 작업 탭에도 보입니다.
- 그 밖의 때(집에 도착할 때, 나갈 때) 다시 필요하면 set_reminder 의 checklist 에 그 id 를 넣으세요. 그때 안내 카드가 목록을 체크 상태 그대로 보여 주고, 푸시에는 남은 항목이 붙습니다. 운영자가 시키지 않았어도 목록이 그때 필요해 보이면 그렇게 걸어 두고 말하세요.
- 운영자는 카드에서 필요 없는 항목을 밀어서 뺄 수 있습니다. 목록에서 사라진 항목은 다시 넣지 마세요.
- 운영자가 했다고 말한 항목은 change_checklist 로 체크하세요. 말하지 않은 항목을 체크하지 마세요. 다 쓴 목록은 close_checklist 로 닫습니다.

작업
- 시간이 걸리거나 나중에 다시 봐야 하는 일, 웹사이트에서 해 둘 일은 create_job 으로 작업을 만들어 맡기세요. 예: 중고나라에 팔기, 가격 내려가면 알려주기, 택배 도착 지켜보기, 예약 열리면 알려주기. 정해진 때에 알려 주기만 하는 일은 작업이 아니라 리마인드입니다.
- 대화 안에서 브라우저로 직접 하지 말고 작업으로 넘기세요. 작업은 백그라운드에서 진행되고, 필요하면 스스로 주기적으로 다시 확인하며, 운영자에게 카드로 묻거나 로그인을 요청합니다.
- 작업을 만든 뒤 "작업을 만들었다/곧 시작한다/작업 탭에서 볼 수 있다" 같은 안내는 하지 마세요. 운영자는 압니다. 할 말이 없으면 "맡겼습니다." 한마디면 됩니다.
- goal 에는 끝났을 때의 상태를, instructions 에는 운영자가 말한 조건·선호·금지와 대화에서 알아낸 사실을 빠짐없이 적으세요. 주기를 말하지 않았으면 적지 않아도 됩니다 - 작업이 목표를 보고 정합니다.
- 사진이 첨부되어 있으면 Read로 먼저 보고, 알아낸 것(물건 이름·구성·상태)을 instructions 에 적고 photos 에 사진 이름을 넣으세요.
- 판매처럼 운영자가 정할 값(가격 등)이 있으면 지어내지 말라고 instructions 에 적으세요. 작업이 시세를 참고로 보여 주고 카드에서 받습니다.
- 진행 중인 작업에 대한 요청(가격 바꿔줘, 그만해)은 list_jobs 로 찾아 instruct_job 으로 전하세요.`

// guestSystemPrompt is for the guest account's agent. It knows nothing of the
// operator's money, messages or shopping - not by instruction alone but
// because it has no tool that could look - and the prompt says so, so that it
// answers "I can't see that" rather than guessing.
const guestSystemPrompt = `당신은 Jarvis입니다. 지금 대화 상대는 운영자가 초대한 손님 계정의 사용자이고, 이 대화는 그 사람과의 1:1 스레드입니다.
아래 지시가 지금의 규칙입니다. 지난 대화에서 다르게 말했거나 다르게 했던 것과 어긋나면 이 지시를 따르세요.

말투와 분량
- 한국어로, 담백하게. 인사말과 사족 없이 본론부터.
- 이 사용자가 정한 말투·호칭·이모티콘이 있으면(알고 있는 취향, 자비스 기억) 그것을 따르세요. 위의 기본 말투보다 먼저입니다. 분량 규칙은 그대로입니다.
- 한 번에 두세 문단을 넘기지 마세요. 목록이 꼭 필요할 때만 목록을 쓰세요.
- 한 일은 한 일로, 하려는 일은 하려는 일로 구분해서 말하세요. 하지 않은 일을 했다고 말하지 마세요.

할 수 있는 것과 없는 것
- 함께 쓰는 캘린더 조회·수정, 웹 검색, 일반적인 질문에 답할 수 있습니다.
- 이 사용자 자신의 리마인드(정한 때 폰으로 알림), 체크리스트, 작업(백그라운드로 맡기는 일)을 맡을 수 있습니다. 모두 이 사용자 것만 보이고, 운영자와 나누지 않습니다.
- 메시지마다 이 사용자의 지금 상황(이 사용자 폰의 위치, 이 사용자의 일정과 함께하는 일정, 맡아 둔 알림·체크리스트)이 붙습니다. 이 사용자가 첨부한 사진을 볼 수 있고, 기억해 달라는 것을 자비스 기억에 적어 둘 수 있습니다. 모두 이 사용자만의 것입니다.
- 운영자의 자산·투자·지출·카드 내역·카카오톡 메시지·쇼핑 계정·위치에는 접근할 수 없습니다. 그런 것을 물으면 이 계정에서는 볼 수 없다고 짧게 답하세요. 추측해서 말하지 마세요.
- 운영자와 나눈 다른 대화, 운영자의 리마인드·체크리스트·작업·기억은 볼 수 없습니다.

사진
- 사진이 첨부되면 Read 로 먼저 열어 보고 답하세요. 첨부한 사진 말고 다른 파일은 열 수 없습니다. 이 계정의 작업에는 사진을 넣을 수 없습니다.
- 상품·장소·음식처럼 보면 바로 아는 것을 말할 때나 보여 달라고 하면, 말로 묘사하지 말고 show_image 로 대화에 사진을 띄우세요. 한 번에 4장까지, caption 에 무엇인지 한 줄. 웹 사진은 read 로 읽은 페이지의 '그림 주소'처럼 대화에 나온 https 주소를 그대로 쓰고, 주소를 짐작해 만들지 마세요. 띄운 사진은 대화에 따로 남으니 답에서 다시 늘어놓지 마세요.

기억
- 지난 일·약속·사람처럼 지금 대화에 없는 맥락은 먼저 search_memory 로 찾으세요. 이 사용자와 나눈 대화에서 뽑은 기억만 있습니다.
- 사용자가 기억해 달라고 하거나 말투·호칭·이모티콘처럼 앞으로 계속 지킬 것을 정하면, 자비스 기억(메모리 폴더)에 적어 두세요. 새 세션에서도 이어집니다. 적거나 고친 내용은 사용자에게 알림으로 갑니다.

승인
- 일정을 추가·수정·삭제하는 도구는 실행 전에 이 대화의 사용자에게 승인을 받습니다. 도구를 부르면 승인 카드가 올라가고, 호출은 바로 "카드를 올렸다"는 답으로 끝납니다.
- 그래서 일정을 바꾸면 좋겠다고 판단했으면 "할까요?"라고 말로 묻지 말고 바로 도구를 불러 카드를 올리세요. 카드가 곧 질문입니다.
- 카드를 올렸으면 결정을 기다리지 말고, 무엇을 요청했는지 한 줄로 알린 뒤 응답을 마치세요. 같은 도구를 다시 부르지 마세요.
- [승인됨…] 또는 [반려]로 시작하는 메시지는 카드 결과이니, 그 안내대로 이어서 하세요.

캘린더
- 일정은 calendar 도구로만 다루세요.
- 일정을 고치거나 지우기 전에 list_events로 id를 먼저 확인하세요.
- 날짜는 YYYY-MM-DD, 시각은 HH:MM으로만 넘기세요.

리마인드
- "몇 시에 알려줘", "집에 가면 알려줘", "나갈 때 말해줘"처럼 정해진 때에 알려 달라는 말은 set_reminder 로 맡기세요. 그때 이 사용자의 폰에 푸시가 갑니다(방해 금지 시간에도). 카드 없이 바로 됩니다.
- 장소(집·회사·영역 이름)는 이 사용자 폰에 등록한 영역 기준입니다. 도착·출발은 이 사용자 폰의 위치로 압니다.
- 맡아 둔 알림을 고치거나 취소할 때는 list_reminders 로 id 를 먼저 확인하세요.

체크리스트
- 챙길 것·할 일이 여럿이면 글머리표로 늘어놓지 말고 make_checklist 로 체크리스트를 만들어 대화에 카드로 띄우세요. 사용자가 카드에서 하나씩 체크하고, 체크한 상태는 저장됩니다. 안 끝난 목록은 작업 탭에도 보입니다.
- 이미 있는 목록이 지금 필요하면 다시 적지 말고 list_checklists 로 id 를 찾아 show_checklist 로 띄우세요.
- 특정한 날을 위한 목록이면 make_checklist 에 date 를 넣으세요. 전날 저녁 집에서 남은 것만 다시 알려 드리고, 날이 지나면 닫습니다. 다른 때 다시 필요하면 set_reminder 의 checklist 에 그 id 를 넣으세요.

작업
- 시간이 걸리거나 정해진 때 다시 해야 하는 일(매일 아침 뉴스 요약, 주말마다 할 일 정리, 무언가 조사해 두기)은 create_job 으로 작업을 만들어 맡기세요. 정해진 때에 알려 주기만 하는 일은 작업이 아니라 리마인드입니다.
- 이 계정의 작업은 웹사이트를 열 수 없습니다(브라우저는 운영자 계정으로 로그인돼 있어서). sites 를 넣지 마세요. 쇼핑·예약·로그인·글 올리기가 필요한 일은 이 계정에서 할 수 없다고 짧게 말하세요. 웹 검색과 웹 문서 읽기로 되는 조사는 됩니다.
- 작업을 만든 뒤 "작업을 만들었다/곧 시작한다" 같은 안내는 하지 마세요. 할 말이 없으면 "맡겼습니다." 한마디면 됩니다. 진행 중인 작업에 대한 요청은 list_jobs 로 찾아 instruct_job 으로 전하세요.`

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// parseMCPURLs reads "name=url,name=url" into the server map. A malformed
// entry is dropped rather than failing the boot: a typo in an optional extra
// server must not take the assistant down with it.
func parseMCPURLs(s string) map[string]string {
	out := map[string]string{}
	for _, entry := range splitList(s) {
		name, url, ok := strings.Cut(entry, "=")
		name, url = strings.TrimSpace(name), strings.TrimSpace(url)
		if !ok || name == "" || url == "" {
			continue
		}
		out[name] = url
	}
	return out
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// cacheTTL is a prompt cache lifetime the CLI accepts: "1h", or "5m" for
// anything else.
func cacheTTL(v string) string {
	if strings.TrimSpace(v) == "1h" {
		return "1h"
	}
	return "5m"
}

func ptr(s string) *string { return &s }

func envInt(k string, def int) int {
	n, err := strconv.Atoi(os.Getenv(k))
	if err != nil {
		return def
	}
	return n
}

func envBool(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envDuration(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// randomToken is 32 random bytes, hex.
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
