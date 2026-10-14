// Package place is the agent's window on context-svc: where the operator is
// (the region they are in, since when; the last fix) and the devices' state
// (CarPlay, focus, the app in front of the Mac). Read only. It gives the
// conversation one tool, get_context, and the judging turns facts.
//
// The model is not asked to look where the operator is on every turn: mind
// already puts its situation (which uses the same visits) in front of it.
// This is for a question that needs the raw state - "아직 회사야?".
package place

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/module"
)

var _ module.ContextSource = (*Module)(nil)

const (
	Name       = "context"
	ServerName = "context"
)

// Tools is what the conversation may call without a card: it only reads.
var Tools = []string{"mcp__" + ServerName + "__get_context"}

// Now is context-svc's GET /now.
type Now struct {
	Location *struct {
		Lat  float64 `json:"lat"`
		Lon  float64 `json:"lon"`
		Acc  float64 `json:"acc"`
		Batt int     `json:"batt"`
	} `json:"location"`
	FixAt  *time.Time `json:"fixAt"`
	Visits []struct {
		Region    string    `json:"region"`
		Kind      string    `json:"kind"`
		EnteredAt time.Time `json:"enteredAt"`
	} `json:"visits"`
	Signals map[string]struct {
		State string    `json:"state"`
		At    time.Time `json:"at"`
	} `json:"signals"`
	Summary string `json:"summary"`
}

type Module struct {
	url, token string
	http       *http.Client
}

func New(url, token string) *Module {
	return &Module{url: strings.TrimRight(url, "/"), token: token, http: &http.Client{Timeout: 5 * time.Second}}
}

func (*Module) Name() string                { return Name }
func (*Module) Start(context.Context) error { return nil }
func (*Module) Stop(context.Context) error  { return nil }

func (m *Module) Now(ctx context.Context) (Now, error) {
	var out Now
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url+"/now", nil)
	if err != nil {
		return out, err
	}
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("위치 서비스에 연결하지 못했습니다: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("위치 서비스가 %d 로 응답했습니다", resp.StatusCode)
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// Facts is the current state as a fact for a judging turn.
func (m *Module) Facts(ctx context.Context, _ string) ([]module.Fact, error) {
	now, err := m.Now(ctx)
	if err != nil {
		return nil, err
	}
	return []module.Fact{{Source: Name, Text: now.Summary}}, nil
}

// Handler is the MCP server with get_context.
func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_context",
		Description: "운영자가 지금 어느 영역(집·회사·…)에 언제부터 있는지, 마지막 위치가 언제인지, 기기 상태(CarPlay·집중 모드 등)를 읽는다. 읽기만 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		now, err := m.Now(ctx)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		body, _ := json.Marshal(now)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: now.Summary + "\n" + string(body)}}}, nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
