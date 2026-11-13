package assemble

import (
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/adapter/permission"
	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/jobs"
	"github.com/choigonyok/jarvis/agent/internal/module/show"
	"github.com/choigonyok/jarvis/agent/internal/module/web"
	"github.com/choigonyok/jarvis/agent/internal/tool"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// dead is an address nothing answers at: building the agent asks no service
// but input-svc whether it is up.
const dead = "http://127.0.0.1:1"

// agentConfig is the configuration an agent of this role reads with every
// service set up, as the compose file sets it, and set is any of it changed.
func agentConfig(t *testing.T, role string, set map[string]string) *config.Config {
	t.Helper()
	input := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(input.Close)
	dir := t.TempDir()
	env := map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN": "test", "ANTHROPIC_API_KEY": "", "JARVIS_API_TOKEN": "test",
		"JARVIS_ROLE": role, "JARVIS_CHAT_URL": input.URL, "JARVIS_MCP_URL": "http://127.0.0.1:8080/mcp",
		"JARVIS_WORKSPACE": filepath.Join(dir, "workspace"), "JARVIS_HOME": filepath.Join(dir, "home"),
		"JARVIS_UPLOADS_DIR":    filepath.Join(dir, "uploads"),
		"JARVIS_PROPOSALS_PATH": filepath.Join(dir, "data", "proposals.json"),
		"JARVIS_SESSION_PATH":   filepath.Join(dir, "data", "session.json"),
		"JARVIS_MIND_URL":       dead, "JARVIS_MEMORY_URL": dead, "JARVIS_CONTEXT_URL": dead, "JARVIS_PHOTOS_URL": dead,
		"JARVIS_CALENDAR_URL": dead, "JARVIS_ASSETS_URL": dead, "JARVIS_VAULT_URL": dead,
		"JARVIS_SEARCH_URL": dead, "JARVIS_SEARCH_TOKEN": "test",
		"JARVIS_EXTRA_MCP": jobs.BrowserServer + "=" + dead, "JARVIS_MCP_TOKEN": "test",
		"JARVIS_NOTIFY_URL": "", "JARVIS_DATABASE_URL": "", "JARVIS_QUEUE_DATABASE_URL": "",
		"JARVIS_ALLOWED_TOOLS": "", "JARVIS_BUILTIN_TOOLS": "", "JARVIS_ALLOWED_ORIGIN": "",
		"JARVIS_JOBS": "true", "JARVIS_ENRICH": "true", "JARVIS_POLICY_HOOK": "off",
	}
	maps.Copy(env, set)
	for k, v := range env {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return &cfg
}

func assembled(t *testing.T, cfg *config.Config) *Assembly {
	t.Helper()
	a, err := Assemble(cfg, Deps{Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// The operator's agent: every module in the order its rows are said, the MCP
// servers at their paths - the conversation given those in its config, a
// background task the rest - and what passes without a card.
func TestTheOperatorsAgent(t *testing.T) {
	cfg := agentConfig(t, "", nil)
	a := assembled(t, cfg)
	if got, want := a.Modules.Names(), []string{"calendar", "assets", "spending", "vault", "job", "memory", "web", "show", "photos", "context", "mind", "messages", "remind", "checklist", "goal", "couple"}; !slices.Equal(got, want) {
		t.Fatalf("모듈:\n%v\n%v", got, want)
	}
	if got, want := slices.Sorted(maps.Keys(a.Mounts)), []string{"/mcp", "/mcp/calendar", "/mcp/change", "/mcp/job-run", "/mcp/jobs", "/mcp/look", "/mcp/show", "/mcp/spending-enrich", "/mcp/vault", "/mcp/web"}; !slices.Equal(got, want) {
		t.Fatalf("MCP 서버:\n%v\n%v", got, want)
	}
	if got, want := slices.Sorted(maps.Keys(cfg.ModuleMCPURLs)), []string{"calendar", "change", "jobs", "look", "show", "vault", "web"}; !slices.Equal(got, want) {
		t.Fatalf("대화에 주는 서버:\n%v\n%v", got, want)
	}
	for name, url := range cfg.ModuleMCPURLs {
		if url != "http://127.0.0.1:8080/mcp/"+name {
			t.Errorf("%s: %s", name, url)
		}
	}
	if got := slices.Sorted(maps.Keys(a.Boards)); !slices.Equal(got, []string{"/mcp/change", "/mcp/look"}) {
		t.Fatalf("판: %v", got)
	}
	for _, want := range append(append([]string{tool.Qualified(tool.Look), tool.Qualified(tool.Change), "Skill"}, web.Tools...), show.Tools...) {
		if !slices.Contains(cfg.AllowedTools, want) {
			t.Errorf("%s 는 카드 없이 지나가야 합니다: %v", want, cfg.AllowedTools)
		}
	}
	for _, want := range []string{permission.ServerName, jobs.RunServerName, jobs.BrowserServer, "look", "jobs", "spending-enrich"} {
		if !slices.Contains(a.Policy.Servers, want) {
			t.Errorf("정책이 아는 서버에 %s 가 없습니다: %v", want, a.Policy.Servers)
		}
	}
	if a.checker == nil || a.suggester != nil || a.watcher != nil {
		t.Fatalf("로그인 확인은 브라우저가 있으면, 제안은 스트림이, 기억 알림은 알릴 곳이 있어야: %v %v %v", a.checker, a.suggester, a.watcher)
	}
}

// The guest's agent is built without the operator's money, vault, photos,
// place and mind: no module, no server, no tool for its model to call.
func TestTheGuestsAgent(t *testing.T) {
	cfg := agentConfig(t, "guest", nil)
	a := assembled(t, cfg)
	if got, want := a.Modules.Names(), []string{"calendar", "job", "memory", "web", "show", "remind", "checklist", "goal"}; !slices.Equal(got, want) {
		t.Fatalf("모듈:\n%v\n%v", got, want)
	}
	if got, want := slices.Sorted(maps.Keys(a.Mounts)), []string{"/mcp", "/mcp/calendar", "/mcp/change", "/mcp/job-run", "/mcp/jobs", "/mcp/look", "/mcp/show", "/mcp/web"}; !slices.Equal(got, want) {
		t.Fatalf("MCP 서버:\n%v\n%v", got, want)
	}
	if slices.Contains(cfg.AllowedTools, "Skill") {
		t.Fatalf("손님에게는 스킬이 없습니다: %v", cfg.AllowedTools)
	}
	if a.checker != nil || a.assets != nil || a.spending != nil {
		t.Fatal("손님의 에이전트는 운영자의 브라우저와 돈을 모릅니다")
	}
}

// Every card waiting reaches the phone, and its notification is put away
// once it is decided: the hooks the agent ties to the cards.
func TestACardRingsAndItsDecisionPutsItAway(t *testing.T) {
	var mu sync.Mutex
	var got []string
	inbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.URL.Path+" "+string(body))
		mu.Unlock()
	}))
	defer inbox.Close()
	a := assembled(t, agentConfig(t, "", map[string]string{"JARVIS_NOTIFY_URL": inbox.URL}))
	if a.watcher == nil {
		t.Fatal("알릴 곳이 있으면 기억이 바뀐 것도 알립니다")
	}
	p, _ := a.Proposals.Open(proposal.Proposal{Card: action.Card{Title: "캠핑 일정을 추가합니다", Body: "11/14 캠핑"}})
	if err := a.Proposals.Decide(p.ID, proposal.Reject); err != nil {
		t.Fatal(err)
	}
	wait := func(want string) {
		t.Helper()
		for i := 0; i < 200; i++ {
			mu.Lock()
			for _, s := range got {
				if strings.HasPrefix(s, "/records ") && strings.Contains(s, want) && strings.Contains(s, p.ID) {
					mu.Unlock()
					return
				}
			}
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("%s 가 오지 않았습니다: %v", want, got)
	}
	wait("approval.pending")
	wait("proposal.decided")
}

// Every module that offers the conversation tools is put up here: one that
// is not would be a feature nobody, test or operator, can reach.
func TestEveryModuleWithToolsIsPutUp(t *testing.T) {
	var src strings.Builder
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			src.Write(b)
		}
	}
	offers := regexp.MustCompile(`func \(m \*Module\) (Look|Change|Handler)\(`)
	dirs, err := os.ReadDir("../module")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join("../module", d.Name(), "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			body, _ := os.ReadFile(f)
			if offers.Match(body) && !strings.Contains(src.String(), `"github.com/choigonyok/jarvis/agent/internal/module/`+d.Name()+`"`) {
				t.Errorf("internal/module/%s 는 대화에 도구를 내놓는데 assemble 이 세우지 않습니다", d.Name())
			}
		}
	}
}
