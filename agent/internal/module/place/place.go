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

// Tools is what the conversation may call without a card: they only read.
var Tools = []string{"mcp__" + ServerName + "__get_context", "mcp__" + ServerName + "__find_nearby"}

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
	type nearbyIn struct {
		Near   string `json:"near,omitempty" jsonschema:"어디 근처인지: 비우면 지금 위치, 등록된 영역 이름(예: 집, 지수네 집), 또는 지도에서 찾을 장소·동네 이름(예: 망원동, 강남역)"`
		What   string `json:"what,omitempty" jsonschema:"무엇을: 식당(기본)·배달·카페·술집·편의점·마트·약국"`
		Radius int    `json:"radiusM,omitempty" jsonschema:"반경 미터(기본 800, 최대 3000)"`
		Limit  int    `json:"limit,omitempty" jsonschema:"최대 개수(기본 8)"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "find_nearby",
		Description: "어느 곳 근처의 식당·카페·편의점 등을 OpenStreetMap 에서 찾는다(읽기 전용, 키 없음). 가까운 순으로 이름·종류·거리·주소·영업시간. " +
			"지도 데이터가 빠진 곳이 있으니 후보로만 쓰고, 답에는 출처(OpenStreetMap)와 어디 기준인지 밝힌다. 주문·예약은 하지 않는다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in nearbyIn) (*mcp.CallToolResult, any, error) {
		fail := func(err error) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		p, err := m.Where(ctx, in.Near)
		if err != nil {
			return fail(err)
		}
		shops, err := m.Nearby(ctx, p, in.What, in.Radius, in.Limit)
		if err != nil {
			return fail(err)
		}
		head := fmt.Sprintf("%s 기준, OpenStreetMap 에서 %d곳", p.From, len(shops))
		if len(shops) == 0 {
			head += " - 지도에 없거나 반경이 좁습니다(radiusM 을 넓혀 보세요)"
		}
		body, _ := json.Marshal(shops)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: head + "\n" + string(body)}}}, nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
