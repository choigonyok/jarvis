// Package permission adapts Claude Code's tool-permission callback to a
// proposal. Claude Code is launched with --permission-prompt-tool pointing
// here, so every tool call it wants to make that is not pre-allowed lands in
// this handler and blocks until a person decides.
//
// This used to be the approval system. It is now one producer of proposals -
// the one that happens to have a caller to keep waiting. A detector that
// notices something on its own opens a proposal the same way and simply does
// not read the decision channel.
package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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
	SetThinking(bool)
}

type Gate struct {
	proposals *proposal.Store
	modules   *module.Registry
	thread    Transcript
	wait      time.Duration
	log       *slog.Logger
	debug     bool
}

func NewGate(
	proposals *proposal.Store,
	modules *module.Registry,
	transcript Transcript,
	wait time.Duration,
	debug bool,
	log *slog.Logger,
) *Gate {
	return &Gate{
		proposals: proposals,
		modules:   modules,
		thread:    transcript,
		wait:      wait,
		log:       log,
		debug:     debug,
	}
}

// Handler returns an http.Handler to mount at the path the CLI is pointed at.
func (g *Gate) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        ToolName,
		Description: "운영자에게 도구 실행 승인을 요청합니다. 결정이 날 때까지 반환하지 않습니다.",
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

	p, decisions := g.proposals.Open(proposal.Proposal{
		Origin: proposal.OriginChat,
		Action: act,
		Card:   card,
	})
	g.thread.AttachProposal(p.ID)

	// While a card is up the agent is waiting on a person, not working.
	g.thread.SetThinking(false)
	defer g.thread.SetThinking(true)

	wait, cancel := context.WithTimeout(ctx, g.wait)
	defer cancel()

	select {
	case d := <-decisions:
		if d == proposal.Reject {
			return reply(Response{
				Behavior: "deny",
				Message:  "운영자가 반려했습니다. 실행하지 마세요. 같은 목적을 이룰 더 안전한 방법이 있으면 제안하고, 없으면 여기서 멈추세요.",
			})
		}
		return reply(Response{Behavior: "allow"})
	case <-wait.Done():
		g.proposals.Abandon(p.ID, "제한 시간 안에 결정이 나지 않았습니다.")
		return reply(Response{
			Behavior: "deny",
			Message:  "제한 시간 안에 결정이 나지 않아 요청이 만료되었습니다.",
		})
	}
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
