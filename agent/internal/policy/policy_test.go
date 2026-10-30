package policy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var owner = Policy{
	Builtins:  []string{"Read", "Glob", "Grep", "TodoWrite", "WebSearch", "ToolSearch", "Write", "Edit"},
	Servers:   []string{"jarvis", "calendar", "assets", "job", "mcp-browser"},
	Workspace: "/workspace",
	MemoryDir: "/home/jarvis/.claude/projects/-workspace/memory",
}

var guest = Policy{
	Builtins:  []string{"Read", "Write", "Edit", "WebSearch", "WebFetch", "TodoWrite", "ToolSearch"},
	Servers:   []string{"jarvis", "calendar", "memory"},
	Workspace: "/workspace",
	MemoryDir: "/home/jarvis/.claude/projects/-workspace/memory",
	Folders:   Folders{"/uploads", "/home/jarvis/.claude/projects/-workspace/memory"},
}

func input(key, value string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{key: value})
	return raw
}

func TestWhatIsNotRegisteredDoesNotRun(t *testing.T) {
	for _, tool := range []string{"Bash", "Task", "KillShell", "mcp__mystery__do", "mcp__", "mcp__calendar", "mcp____x"} {
		if v := owner.Check(Call{Tool: tool}); !v.Deny || v.Rule != RuleUnregistered {
			t.Errorf("%s: %+v", tool, v)
		}
	}
	for _, tool := range []string{"Read", "TodoWrite", "mcp__calendar__create_event", "mcp__job__ask", "mcp__mcp-browser__browser_click"} {
		if v := owner.Check(Call{Tool: tool}); v.Deny {
			t.Errorf("%s: %+v", tool, v)
		}
	}
}

// Every tool the conversations and the guest have really used (their session
// records, 2026-10-31) still goes: a rule that stops ordinary work is worse
// than no rule. What is gone is what no longer exists.
func TestWhatIsInUseIsNotStopped(t *testing.T) {
	p := Policy{
		Builtins: []string{"Read", "Glob", "Grep", "TodoWrite", "WebSearch", "ToolSearch", "Write", "Edit"},
		Servers: Servers([]string{"/mcp", "/mcp/calendar", "/mcp/assets", "/mcp/spending", "/mcp/spending-enrich", "/mcp/vault", "/mcp/jobs", "/mcp/job-run",
			"/mcp/memory", "/mcp/web", "/mcp/show", "/mcp/context", "/mcp/messages", "/mcp/remind", "/mcp/checklist"}, []string{"mcp-browser"}, "jarvis", "job"),
	}
	for _, tool := range []string{
		"ToolSearch", "WebSearch", "Grep", "Write", "Edit", "Read", "Glob",
		"mcp__jobs__create_job", "mcp__jobs__instruct_job", "mcp__jobs__list_jobs", "mcp__memory__search_memory",
		"mcp__calendar__list_events", "mcp__calendar__create_event", "mcp__calendar__update_event", "mcp__calendar__delete_event",
		"mcp__web__read", "mcp__web__search", "mcp__remind__change_reminder", "mcp__remind__set_reminder", "mcp__remind__list_reminders",
		"mcp__messages__search_messages", "mcp__messages__message_context", "mcp__checklist__make_checklist", "mcp__checklist__change_checklist",
		"mcp__checklist__list_checklists", "mcp__checklist__show_checklist", "mcp__spending__get_spending", "mcp__context__get_context", "mcp__context__find_nearby",
		"mcp__vault__save_login", "mcp__vault__list_logins", "mcp__vault__save_shipping_address", "mcp__vault__save_payment_pin", "mcp__vault__get_shipping_address",
		"mcp__mcp-browser__browser_list_sessions", "mcp__mcp-browser__browser_screenshot", "mcp__mcp-browser__browser_extract_content",
		"mcp__assets__get_portfolio", "mcp__assets__get_history", "mcp__show__show_image",
		// A job's run and a background order lookup.
		"mcp__job__ask", "mcp__job__finish", "mcp__job__wait_payment", "mcp__spending-enrich__save_order_items",
	} {
		if v := p.Check(Call{Tool: tool}); v.Deny {
			t.Errorf("%s: %+v", tool, v)
		}
	}
	for _, tool := range []string{"mcp__market__post_listing", "CronCreate", "Bash"} {
		if v := p.Check(Call{Tool: tool}); !v.Deny {
			t.Errorf("없는 도구입니다: %s", tool)
		}
	}
}

// An agent that did not limit the CLI's tools leaves them to the CLI, but
// never its servers.
func TestUnlimitedBuiltins(t *testing.T) {
	p := Policy{Servers: []string{"calendar"}}
	if v := p.Check(Call{Tool: "Bash"}); v.Deny {
		t.Fatalf("%+v", v)
	}
	if v := p.Check(Call{Tool: "mcp__other__x"}); !v.Deny {
		t.Fatalf("%+v", v)
	}
}

// The tools of the operator's money and browser are not the guest's, whatever
// a page or a prompt says.
func TestTheGuestHasNoneOfTheOperatorsTools(t *testing.T) {
	for _, tool := range []string{"mcp__mcp-browser__browser_navigate", "mcp__assets__get_portfolio", "mcp__messages__send_message", "mcp__vault__save_login", "Bash", "Glob"} {
		if v := guest.Check(Call{Tool: tool}); !v.Deny || v.Rule != RuleUnregistered {
			t.Errorf("%s: %+v", tool, v)
		}
	}
	if v := guest.Check(Call{Tool: "mcp__calendar__list_events"}); v.Deny {
		t.Fatalf("%+v", v)
	}
}

func TestTheGuestStaysInTheirFolders(t *testing.T) {
	if v := guest.Check(Call{Tool: "Read", Input: input("file_path", "/proc/1/environ")}); !v.Deny || v.Rule != RuleFolders {
		t.Fatalf("%+v", v)
	}
	if v := guest.Check(Call{Tool: "Read", Input: input("file_path", "/uploads/photo.jpg")}); v.Deny {
		t.Fatalf("%+v", v)
	}
	if v := guest.Check(Call{Tool: "Write", Input: input("file_path", "/home/jarvis/.claude/projects/-workspace/memory/MEMORY.md")}); v.Deny {
		t.Fatalf("%+v", v)
	}
}

// What the CLI reads as its own settings next time cannot be written, with a
// card or without: a hook there runs any command.
func TestTheCLIsOwnConfigIsNotWritten(t *testing.T) {
	for _, path := range []string{
		"/workspace/.claude/settings.json", ".claude/settings.local.json", "/workspace/sub/.claude/hooks/x.sh",
		"/home/jarvis/.claude/settings.json", "/home/jarvis/.claude.json", "/workspace/.mcp.json", ".mcp.json",
		"/workspace/CLAUDE.md", "CLAUDE.local.md", "/workspace/a/../.claude/agents/x.md",
		"/home/jarvis/.claude/projects/-workspace/memory/../settings.json",
	} {
		for _, tool := range []string{"Write", "Edit"} {
			if v := owner.Check(Call{Tool: tool, Input: input("file_path", path)}); !v.Deny || v.Rule != RuleCLIConfig {
				t.Errorf("%s %s: %+v", tool, path, v)
			}
		}
	}
	// With the CLI's tools unlimited, its other writers are held to the same.
	open := owner
	open.Builtins = nil
	if v := open.Check(Call{Tool: "MultiEdit", Input: input("file_path", ".claude/settings.json")}); v.Rule != RuleCLIConfig {
		t.Errorf("%+v", v)
	}
	if v := open.Check(Call{Tool: "NotebookEdit", Input: input("notebook_path", "/workspace/.claude/x.ipynb")}); v.Rule != RuleCLIConfig {
		t.Errorf("%+v", v)
	}
	for _, path := range []string{"/workspace/notes.md", "report.txt", "/workspace/claude.md.bak",
		"/home/jarvis/.claude/projects/-workspace/memory/MEMORY.md", "/home/jarvis/.claude/projects/-workspace/memory/CLAUDE.md"} {
		if v := owner.Check(Call{Tool: "Write", Input: input("file_path", path)}); v.Deny {
			t.Errorf("%s: %+v", path, v)
		}
	}
	// Reading them is the working directories' rule, not this one.
	if v := owner.Check(Call{Tool: "Read", Input: input("file_path", "/workspace/CLAUDE.md")}); v.Deny {
		t.Errorf("%+v", v)
	}
}

func TestThePaymentPasswordNeedsAnApproval(t *testing.T) {
	if v := owner.Check(Call{Tool: PaymentPINTool}); !v.Deny || v.Rule != RulePaymentPIN {
		t.Fatalf("%+v", v)
	}
	if v := owner.Check(Call{Tool: PaymentPINTool, PaymentApproved: true}); v.Deny {
		t.Fatalf("%+v", v)
	}
	// An approval opens that one tool, nothing else.
	if v := owner.Check(Call{Tool: "Bash", PaymentApproved: true}); !v.Deny {
		t.Fatalf("%+v", v)
	}
}

const hookInput = `{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`

func runHook(t *testing.T, url string) (out string) {
	t.Helper()
	var b bytes.Buffer
	if code := RunHook(strings.NewReader(hookInput), &b, url, "tok", http.DefaultClient); code != 0 {
		t.Fatalf("끝 코드 %d", code)
	}
	return b.String()
}

func TestTheHookSaysARefusalAndNothingElse(t *testing.T) {
	var got string
	verdict := Verdict{Deny: true, Rule: RuleUnregistered, Reason: "안 됩니다"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(r.Body)
		got = b.String()
		_ = json.NewEncoder(w).Encode(verdict)
	}))
	defer srv.Close()

	out := runHook(t, srv.URL)
	if got != hookInput {
		t.Fatalf("호출을 그대로 물어야 합니다: %q", got)
	}
	var parsed hookOutput
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	h := parsed.HookSpecificOutput
	if h.HookEventName != "PreToolUse" || h.PermissionDecision != "deny" || h.PermissionDecisionReason != "안 됩니다" {
		t.Fatalf("%+v", h)
	}

	// Not refused: silence, so the call goes on to the gate as it would have.
	verdict = Verdict{}
	if out := runHook(t, srv.URL); out != "" {
		t.Fatalf("허용을 말하면 게이트를 건너뜁니다: %q", out)
	}
}

func TestTheHookFailsClosed(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	url := gone.URL
	gone.Close()
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer refused.Close()
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer garbled.Close()
	for name, u := range map[string]string{"꺼짐": url, "거절": refused.URL, "못 읽음": garbled.URL, "주소 없음": ""} {
		if out := runHook(t, u); !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Errorf("%s: %q", name, out)
		}
	}
	var b bytes.Buffer
	RunHook(strings.NewReader(""), &b, "http://127.0.0.1:1", "", http.DefaultClient)
	if !strings.Contains(b.String(), "deny") {
		t.Errorf("빈 입력: %q", b.String())
	}
}
