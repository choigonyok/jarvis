package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
)

// ServerName is the MCP server the CLI is told to dial for calendar work.
// Tool names reaching the permission gate are therefore mcp__calendar__<tool>.
const ServerName = "calendar"

// QualifiedToolName is how Claude Code refers to one of these tools - the
// same spelling that goes in the pre-allowed list for read-only tools.
func QualifiedToolName(tool string) string {
	return fmt.Sprintf("mcp__%s__%s", ServerName, tool)
}

// ToolList is the read-only tool the operator can safely pre-allow. Writes
// are deliberately absent: they belong on a card.
var ToolList = QualifiedToolName("list_events")

// Handler exposes the calendar to Claude Code over MCP. The write tools here
// are not gated by this package - by the time one runs, the permission gate
// has already put the same action on a card and a person has approved it.
// This handler's job is only to be the single door to the store.
func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_events",
		Description: "캘린더 일정을 조회한다. 수정·삭제하려면 먼저 이걸로 id를 확인한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, ListOutput, error) {
		events := m.store.Range(in.From, in.To)
		return text(renderList(events)), ListOutput{Events: events}, nil
	})

	addWrite[CreateInput](server, m, "create_event", KindCreate, "캘린더에 일정을 추가한다.")
	addWrite[UpdateInput](server, m, "update_event", KindUpdate, "기존 일정을 수정한다. 바꿀 필드만 보낸다.")
	addWrite[DeleteInput](server, m, "delete_event", KindDelete, "일정을 삭제한다.")

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

// addWrite registers one write tool. Every one of them does the same thing -
// re-marshal the typed input into an action and hand it to Execute - so the
// module has exactly one code path that writes, whatever called it.
func addWrite[In any](s *mcp.Server, m *Module, tool, kind, desc string) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        tool,
		Description: desc,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		raw, err := json.Marshal(in)
		if err != nil {
			return nil, nil, err
		}
		res, err := m.Execute(ctx, action.Action{Kind: kind, Input: raw})
		if err != nil {
			// Reported as a tool error, not a transport error: the model
			// should read the reason and correct itself, not retry blindly.
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, nil, nil
		}
		return text(res.Note), nil, nil
	})
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func renderList(events []Event) string {
	if len(events) == 0 {
		return "해당 기간에 일정이 없습니다."
	}
	out := ""
	for _, e := range events {
		out += fmt.Sprintf("%s  [%s]\n", line(e), e.ID)
	}
	return out
}
