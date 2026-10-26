// Package place is the agent's window on context-svc: where the operator is
// (the region they are in, since when; the last fix) and the devices' state
// (CarPlay, focus, the app in front of the Mac). Read only. It gives the
// conversation one tool, get_context, and the judging turns facts.
//
// The model is not asked to look where the operator is on every turn: mind
// already puts its situation (which uses the same visits) in front of it.
// This is for a question that needs the raw state - "아직 회사야?".
//
// find_nearby asks Naver's map first (Naver Place's list through search-svc,
// module web): what is open now and until when, stars, reviews. OpenStreetMap
// is behind it - for when Naver cannot be read, or search-svc is not set up -
// and the answer says which one it was.
package place

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/module/web"
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
	naver      NaverPlaces
}

// NaverPlaces is the map find_nearby asks first: Naver Place's list, through
// search-svc (web.Client).
type NaverPlaces interface {
	Places(ctx context.Context, q web.PlacesQuery) (web.Places, error)
}

// UseNaver puts Naver's map in front of OpenStreetMap. Before Handler: the
// tool's description says which map it reads.
func (m *Module) UseNaver(n NaverPlaces) { m.naver = n }

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
		What   string `json:"what,omitempty" jsonschema:"무엇을: 업종이나 물건 낱말 하나(예: 술집, 카페, 마트, 수산물, 약국, 이비인후과). 비우면 음식점. '늦게까지 하는' 같은 꾸밈말은 빼고 open 으로."`
		Open   bool   `json:"open,omitempty" jsonschema:"지금 영업 중인 곳만"`
		Radius int    `json:"radiusM,omitempty" jsonschema:"반경 미터(기본 1000, 최대 5000)"`
		Limit  int    `json:"limit,omitempty" jsonschema:"최대 개수(기본 8)"`
	}
	description := "어느 곳 근처의 식당·카페·편의점 등을 OpenStreetMap 에서 찾는다(읽기 전용, 키 없음). 가까운 순으로 이름·종류·거리·주소·영업시간. " +
		"지도 데이터가 빠진 곳이 있으니 후보로만 쓰고, 답에는 출처(OpenStreetMap)와 어디 기준인지 밝힌다. 주문·예약은 하지 않는다."
	if m.naver != nil {
		description = "어느 곳 근처의 가게·장소(식당·술집·카페·마트·약국·병원…)를 네이버 지도(플레이스)에서 찾는다(읽기 전용). " +
			"네이버 지도 목록 순서로 이름·종류·거리·지금 영업 여부와 마감 시각·별점·리뷰 수·주소·링크. " +
			"네이버를 읽지 못하면 OpenStreetMap 으로 찾고 그렇다고 알려 준다. 답에는 어디 기준인지 밝힌다. 주문·예약은 하지 않는다."
	}
	mcp.AddTool(server, &mcp.Tool{Name: "find_nearby", Description: description},
		func(ctx context.Context, _ *mcp.CallToolRequest, in nearbyIn) (*mcp.CallToolResult, any, error) {
			return m.findNearby(ctx, in.Near, in.What, in.Open, in.Radius, in.Limit), nil, nil
		})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

// findNearby is find_nearby: Naver's map, else OpenStreetMap.
func (m *Module) findNearby(ctx context.Context, near, what string, open bool, radius, limit int) *mcp.CallToolResult {
	fail := func(err error) *mcp.CallToolResult {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
	}
	p, err := m.Where(ctx, near)
	if err != nil {
		return fail(err)
	}
	what = strings.TrimSpace(what)
	if what == "" {
		what = "음식점"
	}
	if radius <= 0 {
		radius = 1000
	}
	radius = min(radius, 5000)
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	var naverErr error
	if m.naver != nil {
		// What leaves is the point rounded to about a hundred metres, as for
		// OpenStreetMap - and, for the stand-in search, the name asked about.
		area := ""
		if strings.HasPrefix(p.From, "지도 검색") {
			area = strings.TrimSpace(near)
		}
		res, err := m.naver.Places(ctx, web.PlacesQuery{Query: what, Lat: round3(p.Lat), Lng: round3(p.Lon),
			Radius: radius, Open: open, Limit: limit, Area: area})
		if err == nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: renderNaver(p, what, open, radius, res)}}}
		}
		naverErr = err
	}
	shops, err := m.Nearby(ctx, p, what, min(radius, 3000), limit)
	if err != nil {
		if naverErr != nil {
			return fail(fmt.Errorf("네이버 지도(%v)도 OpenStreetMap 도 읽지 못했습니다: %w", naverErr, err))
		}
		return fail(err)
	}
	head := fmt.Sprintf("%s 기준, OpenStreetMap 에서 %d곳", p.From, len(shops))
	if naverErr != nil {
		head = fmt.Sprintf("네이버 지도를 읽지 못해(%v) OpenStreetMap 으로 찾았습니다. ", naverErr) + head
	}
	if len(shops) == 0 {
		head += " - 지도에 없거나 반경이 좁습니다(radiusM 을 넓혀 보세요)"
	}
	if open {
		head += " (OpenStreetMap 은 지금 영업 여부를 모릅니다 - hours 가 적힌 곳만 영업시간이 있습니다)"
	}
	body, _ := json.Marshal(shops)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: head + "\n" + string(body)}}}
}

func renderNaver(p Point, what string, open bool, radius int, res web.Places) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s 기준, 네이버 지도에서 '%s' %d곳 · 반경 %s", p.From, what, len(res.Places), distance(radius))
	if open {
		b.WriteString(" · 지금 영업 중만")
	}
	var counts []string
	if res.Total > 0 {
		counts = append(counts, fmt.Sprintf("목록 %d곳 중", res.Total))
	}
	if res.Beyond > 0 {
		counts = append(counts, fmt.Sprintf("반경 밖 %d", res.Beyond))
	}
	if res.Closed > 0 {
		counts = append(counts, fmt.Sprintf("영업 중 아님 %d", res.Closed))
	}
	if len(counts) > 0 {
		b.WriteString(" (" + strings.Join(counts, " · ") + ")")
	}
	b.WriteString("\n")
	if res.Note != "" {
		b.WriteString("(" + res.Note + ")\n")
	}
	if len(res.Places) == 0 {
		b.WriteString("찾지 못했습니다 - 반경(radiusM)을 넓히거나 다른 낱말로 찾아 보세요.\n")
	}
	for _, pl := range res.Places {
		parts := []string{pl.Name}
		if pl.Category != "" {
			parts = append(parts, pl.Category)
		}
		if pl.Meters >= 0 {
			parts = append(parts, distance(pl.Meters))
		}
		if pl.Status != "" {
			s := pl.Status
			if pl.Hours != "" {
				s += ", " + pl.Hours
			}
			parts = append(parts, s)
		}
		switch {
		case pl.Rating > 0:
			parts = append(parts, fmt.Sprintf("★%.2f(방문자 리뷰 %d)", pl.Rating, pl.Reviews))
		case pl.Reviews > 0:
			parts = append(parts, fmt.Sprintf("방문자 리뷰 %d", pl.Reviews))
		}
		if pl.Address != "" {
			parts = append(parts, pl.Address)
		}
		if pl.Phone != "" {
			parts = append(parts, "☎ "+pl.Phone)
		}
		if pl.Options != "" {
			parts = append(parts, pl.Options)
		}
		if pl.URL != "" {
			parts = append(parts, pl.URL)
		}
		b.WriteString("- " + strings.Join(parts, " · ") + "\n")
	}
	return b.String()
}

func distance(m int) string {
	if m >= 1000 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(m)/1000), ".0") + "km"
	}
	return fmt.Sprintf("%dm", m)
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
