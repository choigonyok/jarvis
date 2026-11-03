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

	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/tool"
)

var _ module.ContextSource = (*Module)(nil)

// Name is the module.
const Name = "memory"

// Fact is one edge of the graph as memory-svc returns it.
type Fact struct {
	Fact      string  `json:"fact"`
	Relation  string  `json:"relation"`
	ValidAt   *string `json:"validAt"`
	InvalidAt *string `json:"invalidAt"`
	// Score is how well the fact answers the query, by the reranker (0~1);
	// nil when memory ranked without it (Voyage out of reach).
	Score *float64 `json:"score"`
}

// JobRelation is the relation memory keeps a job by (mind's sensor
// JobRecord): a job asked for, and what it did once it was over. Not a fact
// about the operator's life - where a job stands is the job's (look 의 job).
const JobRelation = "맡긴 작업"

// What is put in front of a message on its own (Recall). Measured on the live
// graph (2026-11-03, ten typical messages): a fact that answers the message
// scored 0.6~0.86, one that only shared a word 0.35~0.55 - all of "응 그거
// 해줘"'s were 0.47~0.53 - and a job's report came first for "예약 확정됐어?"
// and filled all eight places for "쿠팡 주문한 거 어떻게 됐어?".
const (
	recallMin   = 0.55
	recallMax   = 6
	recallAsked = 12
)

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

// Recall renders what memory holds that answers the message as a block to put
// in front of it, or "" when there is nothing that does (or memory is down - a
// turn never waits on it). Not what only shares a word with it (recallMin), and
// not a job's report: the job says where it stands, and a report kept from the
// middle of one was handed over as a fact after the job had found otherwise.
func (m *Module) Recall(ctx context.Context, query string) string {
	if strings.TrimSpace(query) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	facts, err := m.store.Search(ctx, clip(query, 500), recallAsked)
	if err != nil {
		return ""
	}
	kept := pick(facts)
	if len(kept) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[기억에서 찾은 관련 사실 - 참고용. 지금 메시지와 관계없으면 무시하고, 더 필요하면 look 의 memory 로 찾으세요. 작업이 어디까지 했는지는 look 의 job 으로 보세요]\n")
	for _, f := range kept {
		b.WriteString("- " + line(f) + "\n")
	}
	return b.String()
}

// pick is what of a search goes in front of a message: no job's report,
// nothing scored below recallMin, at most recallMax, best first as they came.
func pick(facts []Fact) []Fact {
	var out []Fact
	for _, f := range facts {
		if f.Relation == JobRelation || (f.Score != nil && *f.Score < recallMin) {
			continue
		}
		if out = append(out, f); len(out) == recallMax {
			break
		}
	}
	return out
}

type SearchInput struct {
	Query string `json:"query" jsonschema:"찾을 내용. 사람·장소·물건 이름이나 '지난달 외식', '여자친구 생일 선물'처럼 자연어로."`
	Limit int    `json:"limit,omitempty" jsonschema:"최대 개수(1-30). 비우면 10."`
}

// Look puts long-term memory on the board.
func (m *Module) Look(b *tool.Board) {
	tool.Add(b, "memory", "jarvis 의 장기 기억(지식 그래프)을 검색한다. 지난 대화, 카카오톡·iMessage 대화, 일정, 지출, 자산 보유·입출금, "+
		"운동, 작업, 사진(날짜·장소)에서 뽑은 사실을 언제부터/언제까지 유효했는지와 함께 돌려준다. "+
		"'전에 말했던', '지난번', 누군가와의 약속·관계·취향처럼 지금 화면에 없는 과거 맥락이 필요할 때 쓴다. "+
		"현재 잔액·이번 달 지출처럼 지금 값은 portfolio·spending 이 더 정확하다.",
		func(ctx context.Context, in SearchInput) (string, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 10
			}
			facts, err := m.store.Search(ctx, in.Query, min(limit, 30))
			if err != nil {
				return "", err
			}
			return render(facts), nil
		})
}

func render(facts []Fact) string {
	if len(facts) == 0 {
		return "기억에서 찾은 것이 없습니다."
	}
	var b strings.Builder
	for _, f := range facts {
		s := line(f)
		if f.Relation == JobRelation {
			s += " [작업 기록 - 지금 어디까지 했는지는 look 의 job]"
		}
		b.WriteString("- " + s + "\n")
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

// APIUsage is what the extraction model (pay-as-you-go API, not the
// subscription) has cost, as memory-svc counts it.
type APIUsage struct {
	Model    string  `json:"model"`
	Enabled  bool    `json:"enabled"`
	DailyUSD float64 `json:"dailyUsd"`
	TodayUSD float64 `json:"todayUsd"`
	Month    struct {
		USD   float64 `json:"usd"`
		Calls int     `json:"calls"`
	} `json:"month"`
	Days []struct {
		Day          string  `json:"day"`
		USD          float64 `json:"usd"`
		Calls        int     `json:"calls"`
		InputTokens  int     `json:"inputTokens"`
		OutputTokens int     `json:"outputTokens"`
	} `json:"days"`
	// Voyage is the search-side key (embeddings, reranking) as memory-svc
	// counts it, passed through as is; absent while there is no key.
	Voyage json.RawMessage `json:"voyage,omitempty"`
}

func (s *Store) APIUsage(ctx context.Context) (*APIUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/status", nil)
	if err != nil {
		return nil, err
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("기억 서비스가 %d 로 응답했습니다", res.StatusCode)
	}
	var st struct {
		Model      string  `json:"model"`
		LLM        bool    `json:"llm"`
		DailyUSD   float64 `json:"dailyUsd"`
		SpentToday float64 `json:"spentToday"`
		Month      struct {
			USD   float64 `json:"usd"`
			Calls int     `json:"calls"`
		} `json:"month"`
		Usage  json.RawMessage `json:"usage"`
		Voyage json.RawMessage `json:"voyage"`
	}
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil {
		return nil, err
	}
	u := &APIUsage{Model: st.Model, Enabled: st.LLM, DailyUSD: st.DailyUSD, TodayUSD: st.SpentToday}
	if len(st.Voyage) > 0 && string(st.Voyage) != "null" {
		u.Voyage = st.Voyage
	}
	u.Month.USD, u.Month.Calls = st.Month.USD, st.Month.Calls
	if len(st.Usage) > 0 {
		_ = json.Unmarshal(st.Usage, &u.Days)
	}
	return u, nil
}

// APIUsage is the module's view of the same, for the console's usage panel.
func (m *Module) APIUsage(ctx context.Context) (any, error) { return m.store.APIUsage(ctx) }
