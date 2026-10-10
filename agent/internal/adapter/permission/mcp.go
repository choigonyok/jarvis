// Package permission adapts Claude Code's tool-permission callback to a
// proposal. Claude Code is launched with --permission-prompt-tool pointing
// here, so every tool call it wants to make that is not pre-allowed lands in
// this handler.
//
// The handler does not wait for the person. It used to - and the CLI's HTTP
// client gives up on an MCP call after five minutes no matter what
// MCP_TOOL_TIMEOUT says, so a card left for five minutes came back as a
// failed call, the model retried, and the same card appeared twice. Now the
// call returns at once with "the card is up", the turn ends, and the decision
// starts a new turn when it comes (Resolve).
package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// ToolName is what the CLI must be told to call: mcp__<server>__<tool>.
const (
	ServerName = "jarvis"
	ToolName   = "request_approval"
)

func QualifiedToolName() string {
	return fmt.Sprintf("mcp__%s__%s", ServerName, ToolName)
}

// Request is an untyped bag on purpose. The CLI's permission payload is not
// documented, and a Go struct would compile to a schema with required fields
// and additionalProperties:false - so an unexpected shape would be rejected
// before the gate ever ran. A map accepts anything and we read it ourselves.
type Request = map[string]any

// Response mirrors the Agent SDK's canUseTool result. Also undocumented for
// the MCP path, hence the raw-request logging in the handler.
type Response struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

// Transcript is the conversation a card is raised inside of. An interface so
// the gate does not care that a chat thread is on the other end - a proposal
// raised by a future detector has no transcript at all.
type Transcript interface {
	AttachProposal(proposalID string)
}

// FollowUp starts a turn the operator did not type: the assistant hearing
// how a card it raised was decided.
type FollowUp interface {
	Enqueue(prompt string)
}

// grantTTL bounds how long an approval waits for the model to come back and
// make the call. The follow-up turn starts right after the decision, so this
// only has to outlast a queue behind a long turn.
const grantTTL = 2 * time.Hour

type Gate struct {
	proposals *proposal.Store
	modules   *module.Registry
	thread    Transcript
	followUp  FollowUp
	log       *slog.Logger
	debug     bool

	mu sync.Mutex
	// grants are approved calls the CLI runs itself (Bash, WebSearch, an
	// outside MCP server). Keyed by tool and exact arguments, spent once: the
	// operator approved that call, not the tool.
	grants map[string]time.Time
}

func NewGate(
	proposals *proposal.Store,
	modules *module.Registry,
	transcript Transcript,
	debug bool,
	log *slog.Logger,
) *Gate {
	return &Gate{
		proposals: proposals,
		modules:   modules,
		thread:    transcript,
		log:       log,
		debug:     debug,
		grants:    map[string]time.Time{},
	}
}

// SetFollowUp wires the runner in. It is created after the gate, because the
// runner's MCP config points at the gate's handler.
func (g *Gate) SetFollowUp(f FollowUp) { g.followUp = f }

// Handler returns an http.Handler to mount at the path the CLI is pointed at.
func (g *Gate) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolName,
		Description: "운영자에게 도구 실행 승인을 요청합니다. 카드를 올리고 바로 반환합니다.",
	}, g.decide)

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

func (g *Gate) decide(ctx context.Context, _ *mcp.CallToolRequest, in Request) (*mcp.CallToolResult, any, error) {
	// The permission payload is undocumented. Log it verbatim once so the
	// real shape can be read off the first live run rather than guessed at.
	if raw, err := json.Marshal(in); err == nil {
		g.log.Info("permission request", "payload", string(raw))
	}

	tool := pickString(in, "tool_name", "toolName", "tool", "name")
	input := pickAny(in, "input", "tool_input", "toolInput", "arguments", "args")
	fields, _ := input.(map[string]any)

	act, card, err := g.describe(tool, input, fields)
	if err != nil {
		// A card we cannot render honestly is a card we must not show. Deny
		// with the reason so the model can correct itself.
		return reply(Response{Behavior: "deny", Message: err.Error()})
	}

	// The second half of an approval: the operator said yes, and the model
	// is back making exactly the call that was approved.
	if g.spend(tool, act.Input) {
		return reply(Response{Behavior: "allow"})
	}

	if waiting, ok := g.proposals.FindPending(act); ok {
		return reply(Response{
			Behavior: "deny",
			Message: fmt.Sprintf("같은 요청의 승인 카드(%s)가 이미 올라가 있습니다. 다시 요청하지 마세요. "+
				"운영자가 결정하면 그 결과가 새 메시지로 옵니다. 지금은 응답을 마치세요.", waiting.ID),
		})
	}

	p, _ := g.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginChat,
		Action: act,
		Card:   card,
	})
	g.thread.AttachProposal(p.ID)

	// Denied in the CLI's terms, pending in ours. The wording carries the
	// difference: nothing was refused, the turn just must not wait here.
	return reply(Response{
		Behavior: "deny",
		Message: fmt.Sprintf("승인 카드(%s)를 올렸습니다. 아직 실행되지 않았고, 반려된 것도 아닙니다. "+
			"결정을 기다리지 말고, 무엇을 요청했는지 한 줄로 알린 뒤 이번 응답을 마치세요. "+
			"운영자가 승인하거나 반려하면 그 결과가 새 메시지로 옵니다. 이 도구를 다시 호출하지 마세요.", p.ID),
	})
}

// Resolve acts on a decision about a card this gate raised, and wakes the
// assistant with the outcome.
//
// A module's action is run here, by the module, with the exact input the
// operator saw - the model is told what happened rather than asked to redo
// it. A tool the CLI runs itself cannot be run from here, so the approval
// becomes a one-time grant for that exact call and the model is asked to make
// it again.
func (g *Gate) Resolve(p proposal.Proposal, d proposal.Decision) {
	if p.Origin == proposal.OriginSuggest {
		g.resolveSuggestion(p, d)
		return
	}
	if p.Origin != proposal.OriginChat {
		return
	}
	title := p.Card.Title

	if d == proposal.Reject {
		g.enqueue(fmt.Sprintf("[반려] 운영자가 승인 카드 %s(%s)를 반려했습니다.\n%s\n\n"+
			"실행하지 마세요. 이유를 캐묻지 말고, 같은 목적을 이루는 덜 위험한 방법이 있으면 제안하고 없으면 짧게 확인만 하세요.",
			p.ID, title, p.Card.Body))
		return
	}

	if actuator, _, ok := g.modules.Lookup(p.Action.Kind); ok {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		res, err := actuator.Execute(ctx, p.Action)
		if err != nil {
			g.proposals.Settle(p.ID, proposal.Failed, err.Error())
			g.enqueue(fmt.Sprintf("[승인 후 실패] 운영자가 승인 카드 %s(%s)를 승인했지만 실행에 실패했습니다: %s\n\n"+
				"무엇이 실패했는지 알리고, 고칠 수 있으면 고친 요청을 다시 올리세요.", p.ID, title, err))
			return
		}
		g.proposals.Settle(p.ID, proposal.Executed, res.Note)
		g.enqueue(fmt.Sprintf("[승인됨·실행 완료] 운영자가 승인 카드 %s(%s)를 승인했고, 이미 실행했습니다: %s\n\n"+
			"같은 도구를 다시 호출하지 마세요. 원래 하던 일이 남았으면 이어서 하고, 없으면 결과를 짧게 알리세요.",
			p.ID, title, res.Note))
		return
	}

	tool := toolOf(p.Action.Kind)
	g.grant(tool, p.Action.Input)
	g.enqueue(fmt.Sprintf("[승인됨] 운영자가 승인 카드 %s(%s)를 승인했습니다. 아직 실행되지 않았습니다.\n"+
		"지금 %s 도구를 정확히 이 인자로 다시 호출하면 카드 없이 실행됩니다. 인자를 하나라도 바꾸면 새 카드가 올라갑니다.\n%s\n\n"+
		"실행한 뒤 원래 하던 일을 이어서 하세요.", p.ID, title, tool, string(proposal.Canonical(p.Action.Input))))
}

// resolveSuggestion settles a card the assistant raised on its own. Approved,
// it runs the action it carries (a calendar entry) or, carrying none, is
// recorded as seen. Nothing goes into the conversation: the operator did not
// start this, and the card's own state says how it ended.
func (g *Gate) resolveSuggestion(p proposal.Proposal, d proposal.Decision) {
	if d != proposal.Approve {
		return
	}
	actuator, _, ok := g.modules.Lookup(p.Action.Kind)
	if !ok {
		g.proposals.Settle(p.ID, proposal.Executed, "확인했습니다.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := actuator.Execute(ctx, p.Action)
	if err != nil {
		g.proposals.Settle(p.ID, proposal.Failed, err.Error())
		return
	}
	g.proposals.Settle(p.ID, proposal.Executed, res.Note)
}

func (g *Gate) enqueue(prompt string) {
	if g.followUp == nil {
		g.log.Error("결정을 전달할 곳이 없습니다", "prompt", prompt)
		return
	}
	g.followUp.Enqueue(prompt)
}

func grantKey(tool string, input json.RawMessage) string {
	return tool + "\x00" + string(proposal.Canonical(input))
}

func (g *Gate) grant(tool string, input json.RawMessage) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants[grantKey(tool, input)] = time.Now().Add(grantTTL)
}

// spend uses up a grant for this exact call, if there is a live one.
func (g *Gate) spend(tool string, input json.RawMessage) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for k, until := range g.grants {
		if now.After(until) {
			delete(g.grants, k)
		}
	}
	k := grantKey(tool, input)
	if _, ok := g.grants[k]; !ok {
		return false
	}
	delete(g.grants, k)
	return true
}

// toolOf is the CLI's name for an action describe() made from one of its own
// tools: "claude.Bash" → "Bash".
func toolOf(kind string) string {
	return strings.TrimPrefix(kind, "claude.")
}

// describe turns a tool call into an action and the card that stands for it.
//
// A module's tool renders its own card: only the calendar knows that moving
// an event should show both the old time and the new one. Claude Code's own
// tools have no module behind them, so they fall back to the generic renderer
// below - the CLI runs those itself, and all we can show is the literal call.
func (g *Gate) describe(tool string, input any, fields map[string]any) (action.Action, action.Card, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		raw = []byte("{}")
	}

	if kind, ok := moduleKind(tool); ok {
		if actuator, _, found := g.modules.Lookup(kind); found {
			act := action.Action{Kind: kind, Input: raw}
			card, err := actuator.Preview(context.Background(), act)
			if err != nil {
				return action.Action{}, action.Card{}, err
			}
			return act, card, nil
		}
	}

	return action.Action{Kind: "claude." + fallback(tool, "unknown"), Input: raw},
		action.Card{
			// Claude writes its own one-line description for some tools; its
			// wording is more specific than anything generic we could supply.
			Title:       fallback(pickString(fields, "description"), title(tool)),
			Body:        body(tool, input),
			Consequence: consequence(tool),
		}, nil
}

// moduleKind maps mcp__calendar__create_event to calendar.create_event.
func moduleKind(tool string) (string, bool) {
	rest, ok := strings.CutPrefix(tool, "mcp__")
	if !ok {
		return "", false
	}
	server, name, ok := strings.Cut(rest, "__")
	if !ok || server == "" || name == "" {
		return "", false
	}
	return server + "." + name, true
}

// reply packs the decision as a JSON text block, which is how the CLI's
// permission tool is expected to answer.
func reply(r Response) (*mcp.CallToolResult, any, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(payload)}},
	}, nil, nil
}
