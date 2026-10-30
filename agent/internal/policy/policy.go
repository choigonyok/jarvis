// Package policy is what may not happen, whoever asks and however sure they
// are. It decides nothing about what is worth doing: it holds the few rules
// that have no exceptions, in code the model cannot reach, and is asked before
// every tool call of every session - including the tools on the allow list,
// which never reach the approval gate, and the background runs, which have no
// gate at all. The CLI's PreToolUse hook is what asks (RunHook).
//
// Judging lives elsewhere: how sure a thing is, whether to ask, is mind's. A
// rule here has no probability in it. Nothing here allows anything either -
// a call this package lets by still goes through the gate as it did.
package policy

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
)

// The rules, by the name a refusal is recorded under.
const (
	RuleUnregistered = "unregistered-tool"
	RuleCLIConfig    = "cli-config"
	RuleFolders      = "guest-folders"
	RulePaymentPIN   = "payment-pin"
)

// PaymentPINTool is the one tool that spends: it types the payment password
// on a keypad. Who may call it is the rule the operator gave in words.
const PaymentPINTool = "mcp__mcp-browser__browser_enter_payment_pin"

// Policy is one agent's rules: what is registered for it and where it may
// write. It is fixed at boot and holds no state.
type Policy struct {
	// Builtins are the CLI tools this agent's sessions may have. Nil: the
	// agent did not limit them (JARVIS_BUILTIN_TOOLS=default).
	Builtins []string
	// Servers are the MCP servers registered with this agent. A tool of any
	// other server is not this agent's, however it came to be offered.
	Servers []string
	// Workspace is where a relative path starts.
	Workspace string
	// MemoryDir is the CLI's memory, the one place under .claude to write.
	MemoryDir string
	// Folders, when set, are the only places the file tools may reach (the
	// guest's agent).
	Folders Folders
}

// Servers is the MCP servers an agent has registered: the ones it mounted
// (mounts, by path: /mcp/calendar), the ones outside it (extra), and any it
// hands only to a run of its own (runs). The bare /mcp mount is the approval
// gate, which runs names too.
func Servers(mounts []string, extra []string, runs ...string) []string {
	out := append(slices.Clone(runs), extra...)
	for _, path := range mounts {
		if name, ok := strings.CutPrefix(path, "/mcp/"); ok {
			out = append(out, name)
		}
	}
	return out
}

// Call is one tool call about to run.
type Call struct {
	Tool  string
	Input json.RawMessage
	// PaymentApproved: whoever makes this call has an approved payment that
	// can still happen.
	PaymentApproved bool
}

// Verdict is what the policy said. A refusal carries the rule and the reason
// for the model. Not denied says nothing more than that.
type Verdict struct {
	Deny   bool   `json:"deny"`
	Rule   string `json:"rule,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func refuse(rule, reason string) Verdict { return Verdict{Deny: true, Rule: rule, Reason: reason} }

// Check judges one call.
func (p Policy) Check(c Call) Verdict {
	if !p.registered(c.Tool) {
		return refuse(RuleUnregistered, "이 도구는 등록되어 있지 않아 실행할 수 없습니다. 다시 시도하지 마세요.")
	}
	if path, ok := writePath(c.Tool, c.Input); ok && p.cliConfig(path) {
		return refuse(RuleCLIConfig, "이 파일은 다음 실행의 지시와 도구를 바꾸는 CLI 설정이라 쓸 수 없습니다. 다시 시도하지 마세요.")
	}
	if p.Folders != nil {
		if file, inside := p.Folders.Judge(c.Tool, c.Input); file && !inside {
			return refuse(RuleFolders, "이 계정에서는 첨부한 사진과 자비스 기억 밖의 파일을 열거나 쓸 수 없습니다. 다시 시도하지 마세요.")
		}
	}
	if c.Tool == PaymentPINTool && !c.PaymentApproved {
		return refuse(RulePaymentPIN, "승인된 결제가 없어 결제 비밀번호를 넣지 않았습니다. 결제 승인 카드가 승인된 뒤에만 넣습니다. "+
			"카드 없이 비밀번호 단계에 왔다면 진행하지 말고 그대로 알리세요.")
	}
	return Verdict{}
}

// registered is whether a tool is one this agent knows: a CLI tool it asked
// for, or a tool of a server it mounted. What is not listed does not run - a
// new tool in a CLI update is not this agent's until someone says so.
func (p Policy) registered(tool string) bool {
	if rest, ok := strings.CutPrefix(tool, "mcp__"); ok {
		server, _, found := strings.Cut(rest, "__")
		return found && slices.Contains(p.Servers, server)
	}
	return p.Builtins == nil || slices.Contains(p.Builtins, tool)
}

// writeTools are the CLI tools that write, and where each names its path.
var writeTools = map[string]string{
	"Write": "file_path", "Edit": "file_path", "MultiEdit": "file_path", "NotebookEdit": "notebook_path",
}

func writePath(tool string, input json.RawMessage) (string, bool) {
	key, ok := writeTools[tool]
	if !ok {
		return "", false
	}
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	path, _ := in[key].(string)
	return path, path != ""
}

// cliConfig is whether a path is one the CLI reads as its own instructions
// or configuration when it next starts: settings and hooks run commands, an
// MCP file adds servers, CLAUDE.md is believed. Written once, it outlives the
// session that wrote it - and a card would only let a page talk someone into
// approving that. The memory folder is the one exception: that is what the
// assistant keeps by design.
func (p Policy) cliConfig(path string) bool {
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.Workspace, path)
	}
	path = filepath.Clean(path)
	switch filepath.Base(path) {
	case ".mcp.json", ".claude.json", "CLAUDE.md", "CLAUDE.local.md":
		return !p.inMemory(path)
	}
	if slices.Contains(strings.Split(path, string(filepath.Separator)), ".claude") {
		return !p.inMemory(path)
	}
	return false
}

func (p Policy) inMemory(path string) bool {
	if p.MemoryDir == "" {
		return false
	}
	return strings.HasPrefix(path, filepath.Clean(p.MemoryDir)+string(filepath.Separator))
}
