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
	// that is not enabled is a module the model cannot see.
	ModuleMCPURLs map[string]string
	// CalendarPath is the calendar module's file. Deliberately outside the
	// workspace mount: if it sat inside, the CLI's own Read and Write tools
	// would be a second door into the data, and the calendar tools' schema
	// and approval cards could be walked straight past.
	CalendarPath  string
	AllowedOrigin string
	Debug         bool
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
			"Read,Glob,Grep,TodoWrite,mcp__calendar__list_events")),
		PermissionMode: env("JARVIS_PERMISSION_MODE", "manual"),
		ApprovalWait:   envDuration("JARVIS_APPROVAL_TIMEOUT", 30*time.Minute),
		TurnTimeout:    envDuration("JARVIS_TURN_TIMEOUT", 2*time.Hour),
		PublicMCPURL:   env("JARVIS_MCP_URL", "http://127.0.0.1:8080/mcp"),
		ModuleMCPURLs:  map[string]string{},
		CalendarPath:   env("JARVIS_CALENDAR_PATH", "./data/calendar.json"),
		AllowedOrigin:  env("JARVIS_ALLOWED_ORIGIN", ""),
		Debug:          envBool("JARVIS_DEBUG", false),
	}

	if c.OAuthToken == "" {
		return c, fmt.Errorf("CLAUDE_CODE_OAUTH_TOKEN is not set (run: claude setup-token)")
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
- 상태를 바꾸는 도구는 실행 전에 운영자의 승인을 받습니다. 승인 카드가 뜨고, 결정이 날 때까지 멈춥니다.
- 반려되면 이유를 캐묻지 말고, 같은 목적을 이루는 덜 위험한 방법을 제안하거나 거기서 멈추세요.
- 되돌릴 수 없는 일은 한 번에 하나씩만 시도하세요. 한 번에 여러 개를 몰아서 요청하지 마세요.

캘린더
- 일정은 calendar 도구로만 다루세요. 캘린더 파일을 직접 읽거나 쓰려고 하지 마세요.
- 일정을 고치거나 지우기 전에 list_events로 id를 먼저 확인하세요.
- 날짜는 YYYY-MM-DD, 시각은 HH:MM으로만 넘기세요.`

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
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
