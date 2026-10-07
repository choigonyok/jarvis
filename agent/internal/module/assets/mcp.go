package assets

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerName is the MCP server the CLI dials. Tools reach the permission gate
// as mcp__assets__<tool>.
const ServerName = "assets"

func QualifiedToolName(tool string) string {
	return fmt.Sprintf("mcp__%s__%s", ServerName, tool)
}

// Tools is every tool this server has. All of them read, so all of them can
// be pre-allowed - a card in front of "how is my portfolio" would only train
// the operator to approve without looking.
var Tools = []string{
	QualifiedToolName("get_portfolio"),
	QualifiedToolName("get_allocation"),
	QualifiedToolName("get_history"),
}

type PortfolioInput struct{}

type HistoryInput struct {
	Days int `json:"days,omitempty" jsonschema:"최근 며칠치를 볼지. 1~3650, 비우면 90."`
}

// The output type is any on purpose, as with the calendar's write tools. A
// typed output gets a schema inferred from the Go struct and every response is
// validated against it - and a brokerage answer with one unexpected null would
// then fail the whole lookup. The structured value still goes out alongside
// the text; it is just not policed.
func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_portfolio",
		Description: "투자 자산 현황을 조회한다: 총자산, 현금, 보유 종목별 평가·손익, 원금 대비 수익, " +
			"1일/1개월/1년 변동, 계좌별(업비트·한국투자증권·금현물) 원금 대비. 읽기 전용, 최대 30초 전 값.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ PortfolioInput) (*mcp.CallToolResult, any, error) {
		p, err := m.store.Portfolio(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		return text(renderPortfolio(p)), p, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_allocation",
		Description: "목표 비중(유망주45/메이저코인30/현금(달러)12/배당주8/금5) 대비 현재 비중, 허용 범위 이탈 여부, " +
			"목표로 돌아가려면 어느 자산군에서 어느 자산군으로 얼마를 옮겨야 하는지. 읽기 전용.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ PortfolioInput) (*mcp.CallToolResult, any, error) {
		p, err := m.store.Portfolio(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		return text(renderAllocation(p)), p.Allocation, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_history",
		Description: "기록된 일별 총자산 스냅샷(그날 실제로 들고 있던 것 기준). 입출금이 섞여 있어 수익률이 아니다. " +
			"수익률은 get_portfolio 의 기간 변동이나 원금 대비를 쓴다. 읽기 전용.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in HistoryInput) (*mcp.CallToolResult, any, error) {
		days := in.Days
		if days <= 0 {
			days = 90
		}
		if days > 3650 {
			days = 3650
		}
		rows, err := m.store.History(ctx, days)
		if err != nil {
			return toolError(err), nil, nil
		}
		return text(renderHistory(rows)), HistoryOutput{Snapshots: rows}, nil
	})

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

type HistoryOutput struct {
	Snapshots []Snapshot `json:"snapshots"`
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// A failed lookup is a tool error, not a transport error: the model should
// say the brokerage did not answer, not retry in a loop.
func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}
