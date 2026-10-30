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
	// PolicyHook is the command the CLI runs before every tool call, with
	// PolicyURL where it asks. Empty leaves the CLI without the hook - a
	// laptop run with no agent binary to call (JARVIS_POLICY_HOOK=off).
	PolicyHook string
	PolicyURL  string

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
	// ChatURL is where the turns are kept (input-svc). The transcript used to be a file in
	// /data, kept outside the workspace so the CLI's own Read and Write could
	// not be a second door into it; it is in Postgres behind that service now.
	ChatURL string
	// AssetsURL is where assets-svc listens. The agent reads it and nothing
	// more: the brokerage keys stay in that service, and the routes this
	// token opens there are lookups plus the principal ledger, which no tool
	// here calls.
	AssetsURL string
	// Enrich turns on the background lookup of marketplace orders (what a
	// "쿠팡 8,900원" charge actually bought). It needs the browser server in
	// ExtraMCPURLs; without it the loop does not start.
	Enrich bool
	// UploadsDir is where photos attached in conversation are kept. Empty
	// turns attachments off. The guest's agent keeps the guest's in a volume
	// of its own.
	UploadsDir string
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
	// Both set (SearchOn): the web module's search and read, "nearby" on
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
	// Thread is this agent's conversation in input-svc: "" or "guest".
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

	// NotifyURL is output-svc: a card raised here becomes a push to the
	// operator's phone. Empty sends nothing.
	NotifyURL string

	// DatabaseURL is Postgres. What the agent reports goes to input.outbox
	// there, so it reaches input-svc even if that is restarting.
	DatabaseURL string

	// QueueURL is where the agent reads what mind sends out: cards
	// (internal/suggest), and for the guest's agent the guest's own reminders
	// and nothing else - under a role that sees only the guest's tables. It
	// is DatabaseURL unless JARVIS_QUEUE_DATABASE_URL says otherwise.
	QueueURL string
	Debug    bool
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
		// Reading skips the card: looking costs nothing and a card in front of
		// every lookup would train the operator to approve without reading.
		// Writes are never on this list - nor is starting or redirecting a job,
		// which goes through the gate to be logged and passes without a card
		// (jobs.CardFree). The look and change boards are added at boot.
		AllowedTools: splitList(env("JARVIS_ALLOWED_TOOLS",
			"TodoWrite,Skill")),
		// Only what the conversation uses: photos are opened with Read, the
		// web is searched and fetched (fetching on a card), ToolSearch loads
		// the module tools the CLI defers, and Write and Edit keep what the
		// operator asks it to remember (MemoryDir - those writes need no card
		// and each is told to the operator; any other write does). No shell, no
		// subagents - neither was used here, and each is described in every
		// request.
		BuiltinTools:     ptr(env("JARVIS_BUILTIN_TOOLS", "Read,Glob,Grep,TodoWrite,WebSearch,WebFetch,ToolSearch,Write,Edit,Skill")),
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
		PolicyHook:       env("JARVIS_POLICY_HOOK", "/usr/local/bin/agent policy-hook"),
		ModuleMCPURLs:    map[string]string{},
		ExtraMCPURLs:     parseMCPURLs(os.Getenv("JARVIS_EXTRA_MCP")),
		ExtraMCPToken:    os.Getenv("JARVIS_MCP_TOKEN"),
		InternalMCPToken: env("JARVIS_MCP_INTERNAL_TOKEN", randomToken()),
		CalendarURL:      env("JARVIS_CALENDAR_URL", "http://localhost:8093"),
		ChatURL:          env("JARVIS_CHAT_URL", "http://localhost:8101"),
		AssetsURL:        env("JARVIS_ASSETS_URL", "http://localhost:8092"),
		Enrich:           envBool("JARVIS_ENRICH", true),
		UploadsDir:       env("JARVIS_UPLOADS_DIR", ""),
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
		QueueURL:        env("JARVIS_QUEUE_DATABASE_URL", os.Getenv("JARVIS_DATABASE_URL")),
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
		c.AllowedTools = splitList(env("JARVIS_ALLOWED_TOOLS", "TodoWrite,WebSearch,WebFetch"))
		// Read opens the photos the guest attaches and the notes of its own
		// memory, Write and Edit keep that memory - and nothing else: reads stop
		// at those folders (runner, readRules), and any file call past them is
		// refused without a card (policy.Folders). No shell.
		builtins := "Read,Write,Edit,WebSearch,WebFetch,TodoWrite,ToolSearch"
		c.BuiltinTools = &builtins
		// No browser: it is logged in to the operator's shops and pay. No
		// background lookups: those belong to the operator's spending. Jobs,
		// yes - the guest's own, which open no website (mind refuses
		// one) - with no photos (the jobs module is given none). The photos
		// attached in conversation and the CLI's memory are the guest's own:
		// this agent's volumes, apart from the operator's.
		c.ExtraMCPURLs = map[string]string{}
		c.Enrich = false
		c.VaultURL = ""
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
	if c.PolicyHook == "off" {
		c.PolicyHook = ""
	}
	c.PolicyURL = strings.TrimSuffix(c.PublicMCPURL, "/mcp") + "/policy/check"
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
- 작업 만들기·지시(create_job, instruct_job), 리마인드(change 의 reminder.set·reminder.change·reminder.cancel), 체크리스트(change 의 checklist.make 등)와 찾기·읽기·비교는 카드 없이 바로 됩니다. 운영자가 맡긴 일이면 묻지 말고 바로 작업으로 넘기세요. 운영자가 시키지 않았는데 떠올린 작업은 카드가 없으니 말로 한 번 물어보세요. 작업이 글을 올리거나 메시지를 보내는 등 밖의 무언가를 바꾸는 단계는 작업이 그때 따로 묻고, 결제는 결제 카드가 따로 묻습니다.
- 카드가 필요한 일(일정 추가·수정·삭제 등)을 하면 좋겠다고 판단했으면 "할까요?", "원하시면 ~할까요?"라고 말로 묻지 말고 그 자리에서 바로 도구를 불러 카드를 올리세요. 카드가 곧 질문이고, 운영자는 카드에서 승인하거나 반려합니다. 운영자가 시키지 않은 일을 떠올렸을 때도 마찬가지입니다. 말로만 묻는 것은 운영자가 카드를 기다리게 만드는 실수입니다.
- 카드를 올렸으면 결정을 기다리지 말고, 무엇을 요청했는지 한 줄로 알린 뒤 응답을 마치세요. 같은 도구를 다시 부르지 마세요.
- 운영자가 결정하면 [승인됨…] 또는 [반려]로 시작하는 메시지가 옵니다. 운영자가 직접 친 말이 아니라 카드 결과이니, 그 안내대로 이어서 하세요.
- 반려되면 이유를 캐묻지 말고, 같은 목적을 이루는 덜 위험한 방법을 제안하거나 거기서 멈추세요.
- 되돌릴 수 없는 일은 한 번에 하나씩만 시도하세요. 한 번에 여러 개를 몰아서 요청하지 마세요.

도구와 스킬
- 지금 값을 읽는 것은 look, 리마인드·체크리스트를 남기고 고치는 것은 change 입니다. 무엇을 넣을지는 도구 설명의 목록에 있습니다.
- 결제·로그인·캘린더·자산·가계부·기억·사진·리마인드·체크리스트·작업의 규칙은 스킬에 있습니다. 그 일을 하기 전에, 이번 세션에서 아직 읽지 않았으면 Skill 로 그 스킬을 먼저 읽으세요.`

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
- 지난 일·약속·사람처럼 지금 대화에 없는 맥락은 먼저 look 의 memory 로 찾으세요. 이 사용자와 나눈 대화에서 뽑은 기억만 있습니다.
- 사용자가 기억해 달라고 하거나 말투·호칭·이모티콘처럼 앞으로 계속 지킬 것을 정하면, 자비스 기억(메모리 폴더)에 적어 두세요. 새 세션에서도 이어집니다. 적거나 고친 내용은 사용자에게 알림으로 갑니다.

승인
- 일정을 추가·수정·삭제하는 도구는 실행 전에 이 대화의 사용자에게 승인을 받습니다. 도구를 부르면 승인 카드가 올라가고, 호출은 바로 "카드를 올렸다"는 답으로 끝납니다.
- 그래서 일정을 바꾸면 좋겠다고 판단했으면 "할까요?"라고 말로 묻지 말고 바로 도구를 불러 카드를 올리세요. 카드가 곧 질문입니다.
- 카드를 올렸으면 결정을 기다리지 말고, 무엇을 요청했는지 한 줄로 알린 뒤 응답을 마치세요. 같은 도구를 다시 부르지 마세요.
- [승인됨…] 또는 [반려]로 시작하는 메시지는 카드 결과이니, 그 안내대로 이어서 하세요.

캘린더
- 일정은 look 의 calendar 와 calendar 도구로만 다루세요.
- 일정을 고치거나 지우기 전에 look 의 calendar 로 id 를 먼저 확인하세요.
- 날짜는 YYYY-MM-DD, 시각은 HH:MM으로만 넘기세요.

리마인드
- "몇 시에 알려줘", "집에 가면 알려줘", "나갈 때 말해줘"처럼 정해진 때에 알려 달라는 말은 change 의 reminder.set 로 맡기세요. 그때 이 사용자의 폰에 푸시가 갑니다(방해 금지 시간에도). 카드 없이 바로 됩니다.
- 장소(집·회사·영역 이름)는 이 사용자 폰에 등록한 영역 기준입니다. 도착·출발은 이 사용자 폰의 위치로 압니다.
- 맡아 둔 알림을 고치거나 취소할 때는 look 의 reminders 로 id 를 먼저 확인하세요.

체크리스트
- 챙길 것·할 일이 여럿이면 글머리표로 늘어놓지 말고 change 의 checklist.make 로 체크리스트를 만들어 대화에 카드로 띄우세요. 사용자가 카드에서 하나씩 체크하고, 체크한 상태는 저장됩니다. 안 끝난 목록은 작업 탭에도 보입니다.
- 이미 있는 목록이 지금 필요하면 다시 적지 말고 look 의 checklists 로 id 를 찾아 checklist.show 로 띄우세요.
- 특정한 날을 위한 목록이면 change 의 checklist.make 에 date 를 넣으세요. 전날 저녁 집에서 남은 것만 다시 알려 드리고, 날이 지나면 닫습니다. 다른 때 다시 필요하면 change 의 reminder.set 의 checklist 에 그 id 를 넣으세요.

작업
- 시간이 걸리거나 정해진 때 다시 해야 하는 일(매일 아침 뉴스 요약, 주말마다 할 일 정리, 무언가 조사해 두기)은 create_job 으로 작업을 만들어 맡기세요. 정해진 때에 알려 주기만 하는 일은 작업이 아니라 리마인드입니다.
- 이 계정의 작업은 웹사이트를 열 수 없습니다(브라우저는 운영자 계정으로 로그인돼 있어서). sites 를 넣지 마세요. 쇼핑·예약·로그인·글 올리기가 필요한 일은 이 계정에서 할 수 없다고 짧게 말하세요. 웹 검색과 웹 문서 읽기로 되는 조사는 됩니다.
- 작업을 만든 뒤 "작업을 만들었다/곧 시작한다" 같은 안내는 하지 마세요. 할 말이 없으면 "맡겼습니다." 한마디면 됩니다. 진행 중인 작업에 대한 요청은 look 의 jobs 로 찾아 instruct_job 으로 전하세요. 작업이 어디까지 했는지는 look 의 job 으로 최근 기록을 보고 답하세요.`

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
