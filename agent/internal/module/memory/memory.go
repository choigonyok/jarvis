// Package memory lets the model ask jarvis' long-term memory (memory-svc, a
// Graphiti knowledge graph over chat, messengers, calendar, spending, assets,
// workouts, jobs and photos) what it knows.
//
// Read-only: the graph is filled by memory-svc's own collectors, never by the
// model. The group is fixed when the module is built - the operator's agent
// reads "owner", the guest's reads "guest" - so no tool input can reach the
// other side.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/module"
)

var _ module.ContextSource = (*Module)(nil)

const (
	Name       = "memory"
	ServerName = "memory"
)

func QualifiedToolName(tool string) string {
	return fmt.Sprintf("mcp__%s__%s", ServerName, tool)
}

// Tools is every tool this server has; all of them read.
var Tools = []string{QualifiedToolName("search_memory")}

// Fact is one edge of the graph as memory-svc returns it.
type Fact struct {
	Fact      string  `json:"fact"`
	Relation  string  `json:"relation"`
	ValidAt   *string `json:"validAt"`
	InvalidAt *string `json:"invalidAt"`
}

// --- client ---------------------------------------------------------------

type Store struct {
	base   string
	token  string
	group  string
	client *http.Client
}

func NewStore(baseURL, token, group string) *Store {
	return &Store{base: strings.TrimRight(baseURL, "/"), token: token, group: group,
		client: &http.Client{Timeout: 15 * time.Second}}
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]Fact, error) {
	q := url.Values{"q": {query}, "group": {s.group}, "limit": {fmt.Sprint(limit)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("기억 서비스에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("기억 서비스가 %d 로 응답했습니다", res.StatusCode)
	}
	var out struct {
		Facts []Fact `json:"facts"`
	}
	return out.Facts, json.NewDecoder(res.Body).Decode(&out)
}

// --- module ---------------------------------------------------------------

type Module struct{ store *Store }

func New(store *Store) *Module { return &Module{store: store} }

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

// Facts is what the graph holds about query - how a chat turn is handed what
// jarvis already knows before the model decides whether to look further.
func (m *Module) Facts(ctx context.Context, query string) ([]module.Fact, error) {
	facts, err := m.store.Search(ctx, query, 8)
	if err != nil {
		return nil, err
	}
	out := make([]module.Fact, 0, len(facts))
	for _, f := range facts {
		out = append(out, module.Fact{Source: Name, Text: line(f)})
	}
	return out, nil
}

// Recall renders Facts as a block to put in front of the operator's message,
// or "" when there is nothing (or memory is down - a turn never waits on it).
func (m *Module) Recall(ctx context.Context, text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	facts, err := m.Facts(ctx, clip(text, 500))
	if err != nil || len(facts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[기억에서 찾은 관련 사실 - 참고용. 지금 메시지와 관계없으면 무시하고, 더 필요하면 search_memory 로 찾으세요]\n")
	for _, f := range facts {
		b.WriteString("- " + f.Text + "\n")
	}
	return b.String()
}

type SearchInput struct {
	Query string `json:"query" jsonschema:"찾을 내용. 사람·장소·물건 이름이나 '지난달 외식', '여자친구 생일 선물'처럼 자연어로."`
	Limit int    `json:"limit,omitempty" jsonschema:"최대 개수(1-30). 비우면 10."`
}

func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "search_memory",
		Description: "jarvis 의 장기 기억(지식 그래프)을 검색한다. 지난 대화, 카카오톡·iMessage 대화, 일정, 지출, 자산 보유·입출금, " +
			"운동, 작업, 사진(날짜·장소)에서 뽑은 사실을 언제부터/언제까지 유효했는지와 함께 돌려준다. " +
			"'전에 말했던', '지난번', 누군가와의 약속·관계·취향처럼 지금 화면에 없는 과거 맥락이 필요할 때 쓴다. " +
			"현재 잔액·이번 달 지출처럼 지금 값은 각 서비스 도구가 더 정확하다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 10
		}
		facts, err := m.store.Search(ctx, in.Query, min(limit, 30))
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: render(facts)}}}, nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

func render(facts []Fact) string {
	if len(facts) == 0 {
		return "기억에서 찾은 것이 없습니다."
	}
	var b strings.Builder
	for _, f := range facts {
		b.WriteString("- " + line(f) + "\n")
	}
	return b.String()
}

// line is one fact with when it held. Structured facts already carry their
// date in the text; extracted ones get it from validAt.
func line(f Fact) string {
	s := f.Fact
	if f.InvalidAt != nil {
		s += fmt.Sprintf(" (%s 까지, 지금은 아님)", day(*f.InvalidAt))
	} else if f.ValidAt != nil {
		if d := day(*f.ValidAt); len(d) < 4 || !strings.Contains(s, d[:4]) {
			s += fmt.Sprintf(" (%s 부터)", d)
		}
	}
	return s
}

func day(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.In(time.Local).Format("2006-01-02")
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
