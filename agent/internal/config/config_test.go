package config

import (
	"strings"
	"testing"
	"time"
)

// The guest agent's limits are what keep the operator's money and messages out
// of its reach, so they are pinned here rather than left to the compose file.
func TestGuestRole(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
	t.Setenv("JARVIS_API_TOKEN", "api")
	t.Setenv("JARVIS_ROLE", "guest")
	t.Setenv("JARVIS_EXTRA_MCP", "mcp-browser=http://browser:8931/mcp")
	t.Setenv("JARVIS_MCP_TOKEN", "tok")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.Guest || c.Thread != "guest" {
		t.Fatalf("guest=%v thread=%q", c.Guest, c.Thread)
	}
	// The web, and the file tools for its photos and memory - no shell, no
	// searching the disk, no subagents.
	if c.BuiltinTools == nil {
		t.Fatal("손님의 내장 도구를 정하지 않았습니다")
	}
	tools := "," + *c.BuiltinTools + ","
	for _, gone := range []string{"Bash", "Glob", "Grep", "Task", "Agent", "NotebookEdit"} {
		if strings.Contains(tools, ","+gone+",") {
			t.Fatalf("손님에게 %s: %s", gone, tools)
		}
	}
	for _, want := range []string{"Read", "Write", "Edit", "WebSearch", "ToolSearch"} {
		if !strings.Contains(tools, ","+want+",") {
			t.Fatalf("손님에게 %s 가 있어야 합니다: %s", want, tools)
		}
	}
	if len(c.ExtraMCPURLs) != 0 || c.Enrich {
		t.Fatalf("브라우저나 백그라운드 조회가 켜져 있습니다: %v %v", c.ExtraMCPURLs, c.Enrich)
	}
	for _, tool := range c.AllowedTools {
		if strings.Contains(tool, "assets") || strings.Contains(tool, "spending") || strings.Contains(tool, "browser") {
			t.Fatalf("손님에게 허용된 도구: %s", tool)
		}
	}
	if strings.Contains(c.SystemPrompt, "get_portfolio") || strings.Contains(c.SystemPrompt, "get_spending") {
		t.Fatal("손님 지시문에 자산·가계부 도구가 있습니다")
	}
	// events-svc reaches the guest's own agent with the guest's reminders.
	if c.SelfURL != "http://agent-guest:8081" {
		t.Fatalf("손님 agent 의 주소: %q", c.SelfURL)
	}
}

// The guest's photos and memory are read without a card - those folders and
// nothing else: not the workspace, not the CLI's home, not the disk. Writing
// skips no card either (the CLI's memory writes need none of their own).
func TestGuestFiles(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
	t.Setenv("JARVIS_API_TOKEN", "api")
	t.Setenv("JARVIS_ROLE", "guest")
	t.Setenv("JARVIS_HOME", "/home/jarvis")
	t.Setenv("JARVIS_WORKSPACE", "/workspace")
	t.Setenv("JARVIS_UPLOADS_DIR", "/uploads")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.UploadsDir != "/uploads" || c.MemoryDir != "/home/jarvis/.claude/projects/-workspace/memory" {
		t.Fatalf("손님의 사진·기억 폴더: %q %q", c.UploadsDir, c.MemoryDir)
	}
	var reads []string
	for _, tool := range c.AllowedTools {
		switch {
		case strings.HasPrefix(tool, "Read(") || strings.HasPrefix(tool, "Grep(") || strings.HasPrefix(tool, "Glob("):
			reads = append(reads, tool)
		case strings.HasPrefix(tool, "Write") || strings.HasPrefix(tool, "Edit") || tool == "Read" || tool == "Bash":
			t.Fatalf("손님 파일 도구가 카드를 건너뜁니다: %s", tool)
		}
	}
	want := []string{"Read(//uploads/**)", "Read(//home/jarvis/.claude/projects/-workspace/memory/**)"}
	if strings.Join(reads, " ") != strings.Join(want, " ") {
		t.Fatalf("손님이 카드 없이 읽는 곳: %v", reads)
	}
	t.Setenv("JARVIS_UPLOADS_DIR", "")
	if c, _ = Load(); c.UploadsDir != "" || strings.Contains(strings.Join(c.AllowedTools, ","), "uploads") {
		t.Fatalf("사진 폴더 없이: %q %v", c.UploadsDir, c.AllowedTools)
	}
}

func TestOwnerUnchanged(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
	t.Setenv("JARVIS_API_TOKEN", "api")
	t.Setenv("JARVIS_ROLE", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Guest || c.Thread != "" || c.SelfURL != "http://agent:8080" {
		t.Fatalf("운영자 설정이 바뀌었습니다: guest=%v thread=%q self=%q", c.Guest, c.Thread, c.SelfURL)
	}
}

// The conversation gets the built-in tools it uses - photos, the web, the
// deferred module tools - and none of the shell, edits or subagents, each of
// which is described in every request. "default" gives them all back.
func TestOwnerBuiltinTools(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
	t.Setenv("JARVIS_API_TOKEN", "api")
	t.Setenv("JARVIS_ROLE", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.BuiltinTools == nil {
		t.Fatal("대화의 내장 도구를 정하지 않았습니다")
	}
	tools := "," + *c.BuiltinTools + ","
	for _, want := range []string{"Read", "WebSearch", "WebFetch", "TodoWrite", "ToolSearch", "Write", "Edit"} {
		if !strings.Contains(tools, ","+want+",") {
			t.Errorf("%s 가 있어야 합니다: %s", want, tools)
		}
	}
	for _, gone := range []string{"Bash", "Task", "Agent", "NotebookEdit"} {
		if strings.Contains(tools, ","+gone+",") {
			t.Errorf("%s 는 대화에 필요 없습니다: %s", gone, tools)
		}
	}
	t.Setenv("JARVIS_BUILTIN_TOOLS", "default")
	if c, _ = Load(); c.BuiltinTools == nil || *c.BuiltinTools != "default" {
		t.Fatalf("default 는 CLI 의 전부: %v", c.BuiltinTools)
	}
}

// The conversation's cache lifetime is 5m unless set to 1h, and the pause
// that starts a new session follows it unless set.
func TestCacheAndSession(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
	t.Setenv("JARVIS_API_TOKEN", "api")
	t.Setenv("JARVIS_ROLE", "")
	for _, c := range []struct {
		ttl, idle string
		want      string
		wantIdle  time.Duration
	}{
		{"", "", "5m", 30 * time.Minute},
		{"1h", "", "1h", time.Hour},
		{"2h", "", "5m", 30 * time.Minute},
		{"1h", "45m", "1h", 45 * time.Minute},
	} {
		t.Setenv("JARVIS_CACHE_TTL", c.ttl)
		t.Setenv("JARVIS_SESSION_IDLE", c.idle)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.CacheTTL != c.want || cfg.SessionIdle != c.wantIdle {
			t.Errorf("%q/%q: ttl %q idle %v", c.ttl, c.idle, cfg.CacheTTL, cfg.SessionIdle)
		}
	}
	cfg, _ := Load()
	if cfg.SessionMaxTokens != 50000 || cfg.SessionMinTokens != 20000 {
		t.Fatalf("기본값: %d %d", cfg.SessionMaxTokens, cfg.SessionMinTokens)
	}
	if cfg.EnrichModel != "sonnet" || cfg.JobsModel != "sonnet" || cfg.JobsRetryModel != "opus" || cfg.Model != "" {
		t.Fatalf("모델: %q %q %q %q", cfg.EnrichModel, cfg.JobsModel, cfg.JobsRetryModel, cfg.Model)
	}
}

// A module's lookups are added once, and nothing unsafe gets in that way.
func TestAllow(t *testing.T) {
	c := Config{AllowedTools: []string{"TodoWrite"}}
	c.Allow("mcp__context__find_nearby", "TodoWrite", "Bash", "mcp__context__find_nearby")
	if strings.Join(c.AllowedTools, ",") != "TodoWrite,mcp__context__find_nearby" {
		t.Fatalf("%v", c.AllowedTools)
	}
}

// No token is no agent, unless a local trial asks for it; and the internal
// MCP token is made fresh when not pinned.
func TestTokens(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
	t.Setenv("JARVIS_API_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("빈 JARVIS_API_TOKEN 으로 떴습니다")
	}
	t.Setenv("JARVIS_ALLOW_OPEN", "true")
	a, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Load()
	if len(a.InternalMCPToken) != 64 || a.InternalMCPToken == b.InternalMCPToken {
		t.Fatalf("내부 MCP 토큰: %q %q", a.InternalMCPToken, b.InternalMCPToken)
	}
}

// Files are read without a card only inside the workspace and the uploads,
// and nothing in the environment can put a bare file tool, a shell, an edit
// or starting a job back on the list.
func TestReadsAreScopedAndUnsafeToolsDropped(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
	t.Setenv("JARVIS_API_TOKEN", "api")
	t.Setenv("JARVIS_ROLE", "")
	t.Setenv("JARVIS_WORKSPACE", "/workspace")
	t.Setenv("JARVIS_UPLOADS_DIR", "/uploads")
	t.Setenv("JARVIS_ALLOWED_TOOLS", "Read,Glob,Grep,TodoWrite,mcp__jobs__create_job,mcp__jobs__instruct_job,mcp__jobs__list_jobs,Bash(ls),Edit")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	has := func(tool string) bool {
		for _, x := range c.AllowedTools {
			if x == tool {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"TodoWrite", "mcp__jobs__list_jobs", "Read(//workspace/**)", "Grep(//workspace/**)", "Glob(//workspace/**)", "Read(//uploads/**)"} {
		if !has(want) {
			t.Errorf("%s 가 있어야 합니다: %v", want, c.AllowedTools)
		}
	}
	for _, gone := range []string{"Read", "Glob", "Grep", "mcp__jobs__create_job", "mcp__jobs__instruct_job", "Bash(ls)", "Edit"} {
		if has(gone) {
			t.Errorf("%s 는 카드를 건너뛰면 안 됩니다: %v", gone, c.AllowedTools)
		}
	}
	if len(c.DroppedTools) != 7 {
		t.Errorf("뺀 도구: %v", c.DroppedTools)
	}

	t.Setenv("JARVIS_ALLOWED_TOOLS", "")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range c.AllowedTools {
		if unsafeTool(tool) {
			t.Errorf("기본값에 %s", tool)
		}
	}
}

// The CLI's own memory - what the operator asked it to remember - is read
// without a card like the workspace. No rule of ours lets a write skip the
// card (the CLI lets memory writes through on its own). The guest has none.
func TestMemoryDir(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
	t.Setenv("JARVIS_API_TOKEN", "api")
	t.Setenv("JARVIS_ROLE", "")
	t.Setenv("JARVIS_HOME", "/home/jarvis")
	t.Setenv("JARVIS_WORKSPACE", "/workspace")
	t.Setenv("JARVIS_ALLOWED_TOOLS", "TodoWrite,Write(//home/jarvis/.claude/projects/-workspace/memory/**)")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MemoryDir != "/home/jarvis/.claude/projects/-workspace/memory" {
		t.Fatalf("기억 폴더: %q", c.MemoryDir)
	}
	read := false
	for _, tool := range c.AllowedTools {
		if tool == "Read(//home/jarvis/.claude/projects/-workspace/memory/**)" {
			read = true
		}
		if strings.HasPrefix(tool, "Write") || strings.HasPrefix(tool, "Edit") {
			t.Fatalf("쓰기가 카드를 건너뜁니다: %s", tool)
		}
	}
	if !read {
		t.Fatalf("기억은 카드 없이 읽어야 합니다: %v", c.AllowedTools)
	}
	if got := memoryDir("/h", "/Users/me/my.app"); got != "/h/.claude/projects/-Users-me-my-app/memory" {
		t.Fatalf("이름 규칙: %q", got)
	}

	// The guest's agent has its own: the same path, in its own home volume.
	t.Setenv("JARVIS_ROLE", "guest")
	g, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if g.MemoryDir != c.MemoryDir {
		t.Fatalf("손님의 기억 폴더: %q", g.MemoryDir)
	}
}

// With search-svc, read opens pages and WebFetch is not loaded - for the
// operator and the guest alike. An explicit tool list is left alone, and
// without both the address and the token nothing changes.
func TestSearchReplacesWebFetch(t *testing.T) {
	base := func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "x")
		t.Setenv("JARVIS_API_TOKEN", "api")
	}
	t.Run("운영자", func(t *testing.T) {
		base(t)
		t.Setenv("JARVIS_SEARCH_URL", "http://search:8104")
		t.Setenv("JARVIS_SEARCH_TOKEN", "s")
		c, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if !c.SearchOn() || strings.Contains(*c.BuiltinTools, "WebFetch") || !strings.Contains(*c.BuiltinTools, "WebSearch") {
			t.Fatalf("WebFetch 는 빠지고 WebSearch 는 남아야 합니다: %s", *c.BuiltinTools)
		}
	})
	t.Run("손님", func(t *testing.T) {
		base(t)
		t.Setenv("JARVIS_ROLE", "guest")
		t.Setenv("JARVIS_SEARCH_URL", "http://search:8104")
		t.Setenv("JARVIS_SEARCH_TOKEN", "s")
		c, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(*c.BuiltinTools, "WebFetch") || !strings.Contains(*c.BuiltinTools, "WebSearch") {
			t.Fatalf("손님도 WebFetch 는 빠지고 WebSearch 는 남아야 합니다: %s", *c.BuiltinTools)
		}
	})
	t.Run("직접 정한 목록", func(t *testing.T) {
		base(t)
		t.Setenv("JARVIS_SEARCH_URL", "http://search:8104")
		t.Setenv("JARVIS_SEARCH_TOKEN", "s")
		t.Setenv("JARVIS_BUILTIN_TOOLS", "WebSearch,WebFetch")
		c, _ := Load()
		if *c.BuiltinTools != "WebSearch,WebFetch" {
			t.Fatalf("정해 준 목록을 바꿨습니다: %s", *c.BuiltinTools)
		}
	})
	t.Run("토큰 없음", func(t *testing.T) {
		base(t)
		t.Setenv("JARVIS_SEARCH_URL", "http://search:8104")
		c, _ := Load()
		if c.SearchOn() || !strings.Contains(*c.BuiltinTools, "WebFetch") {
			t.Fatalf("토큰이 없으면 그대로여야 합니다: %v %s", c.SearchOn(), *c.BuiltinTools)
		}
	})
}
