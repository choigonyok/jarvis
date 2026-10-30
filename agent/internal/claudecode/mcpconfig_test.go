package claudecode

import (
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/config"
)

// The agent's own MCP servers refuse callers without the internal token, so
// every entry pointing at them - chat or background - must carry it, and the
// outside servers must not.
func TestInternalMCPToken(t *testing.T) {
	r := &Runner{cfg: config.Config{
		PublicMCPURL:     "http://127.0.0.1:8080/mcp",
		ModuleMCPURLs:    map[string]string{"calendar": "http://127.0.0.1:8080/mcp/calendar"},
		ExtraMCPURLs:     map[string]string{"mcp-browser": "http://b"},
		ExtraMCPToken:    "outer",
		InternalMCPToken: "inner",
	}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	var chat struct {
		MCPServers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(r.mcpConfig()), &chat); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"jarvis", "calendar"} {
		if got := chat.MCPServers[name].Headers["Authorization"]; got != "Bearer inner" {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := chat.MCPServers["mcp-browser"].Headers["Authorization"]; got != "Bearer outer" {
		t.Errorf("browser: %q", got)
	}

	args, err := r.backgroundArgs(Task{Name: "t", Servers: map[string]string{"suggest": "http://127.0.0.1:8080/mcp/suggest"}})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "--mcp-config")
	var bg struct {
		MCPServers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if i < 0 || json.Unmarshal([]byte(args[i+1]), &bg) != nil || bg.MCPServers["suggest"].Headers["Authorization"] != "Bearer inner" {
		t.Fatalf("background MCP config: %v", args)
	}
}

// The hook is on every kind of run: the allow list and the background runs
// never reach the gate, so this is the only rule that sees them.
func TestEveryRunCarriesThePolicyHook(t *testing.T) {
	r := &Runner{cfg: config.Config{PolicyHook: "/usr/local/bin/agent policy-hook", PolicyURL: "http://127.0.0.1:8080/policy/check", InternalMCPToken: "tok"}}
	value := func(args []string, flag string) string {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	chat := value(r.args("system"), "--settings")
	if !strings.Contains(chat, `"PreToolUse"`) || !strings.Contains(chat, "agent policy-hook") || !strings.Contains(chat, "blockReadsOutsideWorkingDirectories") {
		t.Fatalf("대화: %s", chat)
	}
	args, err := r.backgroundArgs(Task{Name: "작업", Prompt: "p", System: "s", Allowed: []string{"mcp__job__ask"}})
	if err != nil {
		t.Fatal(err)
	}
	if bg := value(args, "--settings"); !strings.Contains(bg, "agent policy-hook") {
		t.Fatalf("백그라운드: %s", bg)
	}
	env := strings.Join(r.env(""), "\n")
	if !strings.Contains(env, "JARVIS_POLICY_URL=http://127.0.0.1:8080/policy/check") || !strings.Contains(env, "JARVIS_POLICY_TOKEN=tok") {
		t.Fatalf("환경: %s", env)
	}

	// Without it (a laptop run), nothing is added and the settings stay valid.
	off := &Runner{}
	if got := off.settings(map[string]any{}); got != "{}" {
		t.Fatalf("%s", got)
	}
	if strings.Contains(strings.Join(off.env(""), "\n"), "JARVIS_POLICY") {
		t.Fatal("훅이 없으면 환경도 없습니다")
	}
}
