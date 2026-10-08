package config

import (
	"fmt"
	"os"
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
	Home         string
	SystemPrompt string
	// AllowedTools skip the approval card entirely. Read-only tools belong
	// here; anything that writes does not.
	AllowedTools []string
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
	// over loopback inside the container and carry no credential.
	ModuleMCPURLs map[string]string
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
	// turns attachments off (the guest agent).
	UploadsDir string
	// JobsURL is where jobs-svc listens: work handed over in conversation,
	// run in the background on whatever cadence each job decides.
	JobsURL string
	// MemoryURL is memory-svc (the Graphiti knowledge graph).
	MemoryURL string
	// Jobs turns on the scheduler. Like Enrich, it needs the browser server.
	Jobs         bool
	JobsInterval time.Duration
	JobsCooldown time.Duration
	JobsTimeout  time.Duration
	// Guest is the second agent, for the guest account (JARVIS_ROLE=guest).
	// It has its own conversation (Thread), its own CLI home and session, only
	// the calendar module, no browser, and no built-in tools beyond web search -
	// the shell and file tools could read this process's environment, and with
	// it the token every service accepts.
	Guest bool
	// Thread is this agent's conversation in chat-svc: "" or "guest".
	Thread string
	// BuiltinTools, when set, is passed as --tools: the only built-in CLI
	// tools the model gets. Nil means the CLI's default set.
	BuiltinTools *string
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
		// list_events is here for the same reason Read is: looking costs
		// nothing and a card in front of every lookup would train the
		// operator to approve without reading. Writes are never on this list.
		AllowedTools: splitList(env("JARVIS_ALLOWED_TOOLS",
			"Read,Glob,Grep,TodoWrite,mcp__calendar__list_events,"+
				"mcp__assets__get_portfolio,mcp__assets__get_allocation,mcp__assets__get_history,"+
				"mcp__spending__get_spending,mcp__memory__search_memory,mcp__jobs__create_job,mcp__jobs__list_jobs,mcp__jobs__instruct_job")),
		PermissionMode: env("JARVIS_PERMISSION_MODE", "manual"),
		ApprovalWait:   envDuration("JARVIS_APPROVAL_TIMEOUT", 30*time.Minute),
		TurnTimeout:    envDuration("JARVIS_TURN_TIMEOUT", 2*time.Hour),
		PublicMCPURL:   env("JARVIS_MCP_URL", "http://127.0.0.1:8080/mcp"),
		ModuleMCPURLs:  map[string]string{},
		ExtraMCPURLs:   parseMCPURLs(os.Getenv("JARVIS_EXTRA_MCP")),
		ExtraMCPToken:  os.Getenv("JARVIS_MCP_TOKEN"),
		CalendarURL:    env("JARVIS_CALENDAR_URL", "http://localhost:8093"),
		ChatURL:        env("JARVIS_CHAT_URL", "http://localhost:8094"),
		AssetsURL:      env("JARVIS_ASSETS_URL", "http://localhost:8092"),
		SpendingURL:    env("JARVIS_SPENDING_URL", "http://localhost:8095"),
		Enrich:         envBool("JARVIS_ENRICH", true),
		UploadsDir:     env("JARVIS_UPLOADS_DIR", ""),
		JobsURL:        env("JARVIS_JOBS_URL", "http://localhost:8098"),
		// Empty turns long-term memory off: no tool, nothing recalled.
		MemoryURL:    os.Getenv("JARVIS_MEMORY_URL"),
		Jobs:         envBool("JARVIS_JOBS", true),
		JobsInterval: envDuration("JARVIS_JOBS_INTERVAL", 30*time.Second),
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
		Debug:           envBool("JARVIS_DEBUG", false),
	}

	if c.Guest = env("JARVIS_ROLE", "") == "guest"; c.Guest {
		c.Thread = "guest"
		c.SystemPrompt = env("JARVIS_SYSTEM_PROMPT", guestSystemPrompt)
		c.AllowedTools = splitList(env("JARVIS_ALLOWED_TOOLS", "TodoWrite,WebSearch,WebFetch,mcp__calendar__list_events,mcp__memory__search_memory"))
		builtins := "WebSearch,WebFetch,TodoWrite"
		c.BuiltinTools = &builtins
		// No browser: it is logged in to the operator's shops and pay. No
		// background lookups: those belong to the operator's spending.
		c.ExtraMCPURLs = map[string]string{}
		c.Enrich = false
		c.Jobs = false
		c.UploadsDir = ""
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
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		// The CLI prefers the OAuth token, but an API key sitting in the
		// environment is a billing surprise waiting to happen. Say so loudly.
		return c, fmt.Errorf("ANTHROPIC_API_KEY is set alongside CLAUDE_CODE_OAUTH_TOKEN; unset it so usage bills to the subscription")
	}
	return c, nil
}

const defaultSystemPrompt = `당신은 Jarvis입니다. 한 사람의 개인 어시스턴트로, 그 사람과 1:1 스레드에서만 대화합니다.

말투와 분량
- 한국어로, 담백하게. 인사말과 사족 없이 본론부터.
- 한 번에 두세 문단을 넘기지 마세요. 목록이 꼭 필요할 때만 목록을 쓰세요.
- 한 일은 한 일로, 하려는 일은 하려는 일로 구분해서 말하세요. 하지 않은 일을 했다고 말하지 마세요.

승인
- 상태를 바꾸는 도구는 실행 전에 운영자의 승인을 받습니다. 도구를 부르면 승인 카드가 올라가고, 호출은 바로 "카드를 올렸다"는 답으로 끝납니다.
- 카드를 올렸으면 결정을 기다리지 말고, 무엇을 요청했는지 한 줄로 알린 뒤 응답을 마치세요. 같은 도구를 다시 부르지 마세요.
- 운영자가 결정하면 [승인됨…] 또는 [반려]로 시작하는 메시지가 옵니다. 운영자가 직접 친 말이 아니라 카드 결과이니, 그 안내대로 이어서 하세요.
- 반려되면 이유를 캐묻지 말고, 같은 목적을 이루는 덜 위험한 방법을 제안하거나 거기서 멈추세요.
- 되돌릴 수 없는 일은 한 번에 하나씩만 시도하세요. 한 번에 여러 개를 몰아서 요청하지 마세요.

결제
- 결제 수단은 네이버페이 하나입니다. 다른 수단은 쓰지 마세요.
- 결제 화면에서는 네이버페이를 고르세요. 네이버페이를 지원하지 않는 곳이면 결제하지 말고, 무엇을 사려다 멈췄는지 보고하세요.
- 쇼핑몰 자체 간편결제(쿠팡페이 등)로 가는 요청은 네트워크에서 막힙니다. 막혔다면 우회할 방법을 찾지 말고 네이버페이로 돌아오거나 멈추세요.

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

작업
- 시간이 걸리거나 나중에 다시 봐야 하는 일, 웹사이트에서 해 둘 일은 create_job 으로 작업을 만들어 맡기세요. 예: 중고나라에 팔기, 가격 내려가면 알려주기, 택배 도착 지켜보기, 예약 열리면 알려주기.
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

말투와 분량
- 한국어로, 담백하게. 인사말과 사족 없이 본론부터.
- 한 번에 두세 문단을 넘기지 마세요. 목록이 꼭 필요할 때만 목록을 쓰세요.
- 한 일은 한 일로, 하려는 일은 하려는 일로 구분해서 말하세요. 하지 않은 일을 했다고 말하지 마세요.

할 수 있는 것과 없는 것
- 함께 쓰는 캘린더 조회·수정, 웹 검색, 일반적인 질문에 답할 수 있습니다.
- 운영자의 자산·투자·지출·카드 내역·카카오톡 메시지·쇼핑 계정에는 접근할 수 없습니다. 그런 것을 물으면 이 계정에서는 볼 수 없다고 짧게 답하세요. 추측해서 말하지 마세요.
- 운영자와 나눈 다른 대화는 볼 수 없습니다.

승인
- 일정을 추가·수정·삭제하는 도구는 실행 전에 이 대화의 사용자에게 승인을 받습니다. 도구를 부르면 승인 카드가 올라가고, 호출은 바로 "카드를 올렸다"는 답으로 끝납니다.
- 카드를 올렸으면 결정을 기다리지 말고, 무엇을 요청했는지 한 줄로 알린 뒤 응답을 마치세요. 같은 도구를 다시 부르지 마세요.
- [승인됨…] 또는 [반려]로 시작하는 메시지는 카드 결과이니, 그 안내대로 이어서 하세요.

캘린더
- 일정은 calendar 도구로만 다루세요.
- 일정을 고치거나 지우기 전에 list_events로 id를 먼저 확인하세요.
- 날짜는 YYYY-MM-DD, 시각은 HH:MM으로만 넘기세요.`

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
