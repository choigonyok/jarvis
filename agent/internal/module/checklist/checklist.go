// Package checklist is the conversation's checklists - "캠핑 준비물 정리해
// 줘", "집에 가서 할 일" - kept by mind-svc with which items are done, and
// shown here as cards in the conversation whose items the operator ticks.
//
// A list said as a paragraph of bullets is read once and gone; a card keeps
// what was ticked, and so does mind, so the next time the list is shown -
// asked for, or at a reminder's moment (set_reminder with the list) - and
// whatever asks what is left sees the same. None of it takes an approval
// card: it changes the operator's own list, and they asked for it.
package checklist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

const (
	Name       = "checklist"
	ServerName = "checklist"
)

// Tools is every tool this server has; none needs a card.
var Tools = []string{
	"mcp__" + ServerName + "__make_checklist", "mcp__" + ServerName + "__show_checklist",
	"mcp__" + ServerName + "__change_checklist", "mcp__" + ServerName + "__list_checklists",
	"mcp__" + ServerName + "__close_checklist",
}

// Item is one line of a list, as mind keeps it.
type Item struct {
	ID     string     `json:"id"`
	Text   string     `json:"text"`
	Done   bool       `json:"done"`
	DoneAt *time.Time `json:"doneAt,omitempty"`
	By     string     `json:"by,omitempty"`
}

// List is one checklist as mind's /api/checklists answers it: what the
// console draws, too.
type List struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Note      string    `json:"note,omitempty"`
	Topic     string    `json:"topic,omitempty"`
	Date      string    `json:"date,omitempty"`
	Items     []Item    `json:"items"`
	Status    string    `json:"status"`
	Line      string    `json:"line,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Cards is the conversation, where a list is laid down as a card.
type Cards interface {
	Card(o thread.Origin, paragraphs []string) *thread.Turn
}

// Sender sends what would change mind through the stream (mindcmd).
type Sender interface {
	Do(ctx context.Context, method, path string, in, out any) error
}

type Module struct {
	url, token string
	http       *http.Client
	send       Sender
	cards      Cards
	bus        *bus.Bus
	log        *slog.Logger
}

// New reads mind-svc at url with the services' token and changes it through
// send; cards is the conversation, and b is where every list's change goes
// out to the screens.
func New(url, token string, send Sender, cards Cards, b *bus.Bus, log *slog.Logger) *Module {
	if log == nil {
		log = slog.Default()
	}
	return &Module{url: strings.TrimRight(url, "/"), token: token, http: &http.Client{Timeout: 15 * time.Second}, send: send, cards: cards, bus: b, log: log}
}

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) do(ctx context.Context, method, path string, in, out any) error {
	if method != http.MethodGet && m.send != nil {
		return m.send.Do(ctx, method, path, in, out)
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.url+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	res, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("mind 에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("mind 가 %d 로 응답했습니다", res.StatusCode)
	}
	return json.Unmarshal(raw, out)
}

// one is a call that answers with a list, which goes out to the screens.
func (m *Module) one(ctx context.Context, method, path string, in any) (List, error) {
	var out struct {
		Checklist List `json:"checklist"`
	}
	if err := m.do(ctx, method, path, in, &out); err != nil {
		return List{}, err
	}
	m.publish(out.Checklist)
	return out.Checklist, nil
}

// publish sends a list as it stands to every screen: a card showing it
// redraws, on this device and the others.
func (m *Module) publish(l List) {
	if m.bus != nil && l.ID != "" {
		m.bus.Publish(bus.Event{Type: "checklist", Checklist: l})
	}
}

// Get is one list.
func (m *Module) Get(ctx context.Context, id string) (List, error) {
	var out struct {
		Checklist List `json:"checklist"`
	}
	err := m.do(ctx, http.MethodGet, "/api/checklists/"+url.PathEscape(id), nil, &out)
	return out.Checklist, err
}

// Find is the lists of these ids, open or closed, by id: what the cards on
// a page of the conversation show. One that is gone is left out.
func (m *Module) Find(ctx context.Context, ids []string) (map[string]any, error) {
	out := map[string]any{}
	if len(ids) == 0 {
		return out, nil
	}
	var res struct {
		Checklists []List `json:"checklists"`
	}
	if err := m.do(ctx, http.MethodGet, "/api/checklists?ids="+url.QueryEscape(strings.Join(ids, ",")), nil, &res); err != nil {
		return nil, err
	}
	for _, l := range res.Checklists {
		out[l.ID] = l
	}
	return out, nil
}

// Open is the lists still open, the latest changed first: what the 작업
// tab shows of them (the unfinished among them).
func (m *Module) Open(ctx context.Context) (any, error) {
	var res struct {
		Checklists []List `json:"checklists"`
	}
	if err := m.do(ctx, http.MethodGet, "/api/checklists", nil, &res); err != nil {
		return nil, err
	}
	if res.Checklists == nil {
		res.Checklists = []List{}
	}
	return res.Checklists, nil
}

// Drop takes an item off a list - one swiped away on a card as not needed.
func (m *Module) Drop(ctx context.Context, id, item string) (any, error) {
	return m.one(ctx, http.MethodPatch, "/api/checklists/"+url.PathEscape(id), map[string]any{"remove": []string{item}, "by": "card"})
}

// Close closes a list from the console.
func (m *Module) Close(ctx context.Context, id string) (any, error) {
	return m.one(ctx, http.MethodDelete, "/api/checklists/"+url.PathEscape(id), nil)
}

// Check is a tap on a card's item.
func (m *Module) Check(ctx context.Context, id, item string, done bool) (any, error) {
	return m.one(ctx, http.MethodPost, "/api/checklists/"+url.PathEscape(id)+"/items/"+url.PathEscape(item),
		map[string]any{"done": done, "by": "card"})
}

// Show lays the list in the conversation as a card, under lead (a line on
// why now; empty is none).
func (m *Module) Show(ctx context.Context, id, lead string) (List, error) {
	return m.ShowAs(ctx, id, lead, "")
}

// ShowAs is Show for what came from elsewhere as ref (mind's event): the
// card is laid once, however often ref is delivered.
func (m *Module) ShowAs(ctx context.Context, id, lead, ref string) (List, error) {
	l, err := m.Get(ctx, id)
	if err != nil {
		return l, err
	}
	m.lay(l, lead, ref)
	return l, nil
}

func (m *Module) lay(l List, lead, ref string) {
	if m.cards == nil {
		return
	}
	if ref != "" {
		if t, ok := m.cards.(interface{ Told(kind, ref string) bool }); ok && t.Told(thread.OriginChecklist, ref) {
			return
		}
	}
	var paragraphs []string
	if lead = strings.TrimSpace(lead); lead != "" {
		paragraphs = []string{lead}
	}
	m.cards.Card(thread.Origin{Kind: thread.OriginChecklist, Checklist: l.ID, Title: l.Title, Ref: ref}, paragraphs)
	m.publish(l)
}

// Publish sends the list of this id as it stands now to the screens - for a
// card laid elsewhere (a notice) that shows it.
func (m *Module) Publish(ctx context.Context, id string) {
	if l, err := m.Get(ctx, id); err == nil {
		m.publish(l)
	} else {
		m.log.Warn("체크리스트를 읽지 못했습니다", "checklist", id, "err", err)
	}
}

type MakeInput struct {
	Title string   `json:"title" jsonschema:"목록 이름. 예: '주말 캠핑 준비물', '집에 가서 할 일'"`
	Items []string `json:"items" jsonschema:"항목들, 하나에 한 가지씩 짧게. 예: ['장갑', '베개', '이소가스']"`
	Note  string   `json:"note,omitempty" jsonschema:"목록 아래 한 줄(선택). 예: '토요일 아침 출발'"`
	Topic string   `json:"topic,omitempty" jsonschema:"같은 목록을 다시 만들 때 묶는 이름(선택). 같은 topic 의 열린 목록이 있으면 새로 만들지 않고 그 목록에 더한다. 예: 'camping-weekend'"`
	Date  string   `json:"date,omitempty" jsonschema:"이 목록이 필요한 날 YYYY-MM-DD(선택, 캠핑 가는 날 등). 주면 mind 가 전날 저녁 집에서 남은 것만 다시 띄우고, 날이 지나면 닫는다"`
	Show  *bool    `json:"show,omitempty" jsonschema:"대화에 카드로 띄울지. 기본 true"`
	Lead  string   `json:"lead,omitempty" jsonschema:"카드 위에 붙일 한 줄(선택)"`
}

type ShowInput struct {
	ID   string `json:"id" jsonschema:"체크리스트 id(chk_…). 지금 상황의 '체크리스트'나 list_checklists 에 있다"`
	Lead string `json:"lead,omitempty" jsonschema:"카드 위에 붙일 한 줄(선택). 예: '내일 출발이라 남은 것만 확인해 보세요'"`
}

type ChangeInput struct {
	ID      string   `json:"id" jsonschema:"체크리스트 id(chk_…)"`
	Title   string   `json:"title,omitempty" jsonschema:"새 이름. 비우면 그대로"`
	Note    string   `json:"note,omitempty" jsonschema:"새 한 줄 설명. 비우면 그대로"`
	Date    string   `json:"date,omitempty" jsonschema:"목록이 필요한 날을 바꿀 때 YYYY-MM-DD. 비우면 그대로"`
	Add     []string `json:"add,omitempty" jsonschema:"더할 항목"`
	Remove  []string `json:"remove,omitempty" jsonschema:"뺄 항목(항목 id 나 이름)"`
	Check   []string `json:"check,omitempty" jsonschema:"했다고 체크할 항목(항목 id 나 이름). 운영자가 했다고 말한 것만"`
	Uncheck []string `json:"uncheck,omitempty" jsonschema:"체크를 풀 항목(항목 id 나 이름)"`
}

type ListInput struct {
	All bool `json:"all,omitempty" jsonschema:"true 면 최근 2주 안에 닫은 목록까지"`
}

type CloseInput struct {
	ID string `json:"id" jsonschema:"닫을 체크리스트 id(chk_…)"`
}

func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "make_checklist",
		Description: "체크리스트를 만든다 - 준비물, 할 일처럼 하나씩 챙기고 지워 갈 것. 목록과 체크 상태는 mind 가 갖고, 대화에 체크할 수 있는 카드로 띄운다(show 기본 true). " +
			"목록을 글로 늘어놓는 대신 이것을 쓴다. 같은 topic 의 열린 목록이 있으면 그 목록에 더한다. " +
			"특정한 날을 위한 목록이면 date 를 넣는다 - mind 가 전날 저녁 집에서 남은 것만 다시 띄우고 날이 지나면 닫는다. " +
			"그 밖의 때(집에 도착할 때, 나갈 때) 다시 띄우려면 set_reminder 의 checklist 에 이 id 를 넣는다. 안 끝난 목록은 작업 탭에도 보인다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MakeInput) (*mcp.CallToolResult, any, error) {
		var out struct {
			Checklist List `json:"checklist"`
		}
		if err := m.do(ctx, http.MethodPost, "/api/checklists", map[string]any{"title": in.Title, "items": in.Items, "note": in.Note, "topic": in.Topic, "date": in.Date, "source": "chat"}, &out); err != nil {
			return failed("만들지 못했습니다", err), nil, nil
		}
		l := out.Checklist
		said := "만들었습니다: " + l.Line + "\n항목: " + items(l)
		if in.Show == nil || *in.Show {
			m.lay(l, in.Lead, "") // and the screens hear of it
			said += "\n대화에 체크할 수 있는 카드로 띄웠습니다. 항목을 글로 다시 늘어놓지 마세요."
		} else {
			m.publish(l)
		}
		return toolText(said, false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "show_checklist",
		Description: "이미 있는 체크리스트를 지금 대화에 카드로 띄운다(체크한 상태 그대로). 운영자가 그 목록을 보자고 하거나 지금 그 목록이 필요할 때(짐 쌀 때, 집에 왔을 때, 무엇이 남았냐고 물을 때) " +
			"말로 늘어놓지 말고 이것을 쓴다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ShowInput) (*mcp.CallToolResult, any, error) {
		l, err := m.Show(ctx, in.ID, in.Lead)
		if err != nil {
			return failed("띄우지 못했습니다", err), nil, nil
		}
		return toolText("대화에 카드로 띄웠습니다: "+l.Line+"\n항목을 글로 다시 늘어놓지 마세요.", false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "change_checklist",
		Description: "체크리스트를 고친다: 항목 더하기·빼기, 운영자가 했다고 말한 항목 체크, 체크 풀기, 이름 바꾸기. 대화에 떠 있는 카드도 바로 바뀐다. " +
			"운영자가 말하지 않은 항목을 체크하지 않는다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ChangeInput) (*mcp.CallToolResult, any, error) {
		body := map[string]any{"by": "chat"}
		if strings.TrimSpace(in.Title) != "" {
			body["title"] = in.Title
		}
		if strings.TrimSpace(in.Note) != "" {
			body["note"] = in.Note
		}
		if strings.TrimSpace(in.Date) != "" {
			body["date"] = in.Date
		}
		for k, v := range map[string][]string{"add": in.Add, "remove": in.Remove, "check": in.Check, "uncheck": in.Uncheck} {
			if len(v) > 0 {
				body[k] = v
			}
		}
		l, err := m.one(ctx, http.MethodPatch, "/api/checklists/"+url.PathEscape(in.ID), body)
		if err != nil {
			return failed("고치지 못했습니다", err), nil, nil
		}
		return toolText("고쳤습니다: "+l.Line, false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_checklists",
		Description: "체크리스트들과 항목·체크 상태. 읽기만 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, any, error) {
		path := "/api/checklists"
		if in.All {
			path += "?all=1"
		}
		var out struct {
			Checklists []List `json:"checklists"`
		}
		if err := m.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		if len(out.Checklists) == 0 {
			return toolText("체크리스트가 없습니다.", false), nil, nil
		}
		var b strings.Builder
		for _, l := range out.Checklists {
			b.WriteString(l.Line + "\n  " + items(l) + "\n")
		}
		return toolText(strings.TrimRight(b.String(), "\n"), false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "close_checklist",
		Description: "다 쓴 체크리스트를 닫는다. 지금 상황에 더 나오지 않고, 2주 동안은 list_checklists(all) 로 볼 수 있다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CloseInput) (*mcp.CallToolResult, any, error) {
		l, err := m.one(ctx, http.MethodDelete, "/api/checklists/"+url.PathEscape(in.ID), nil)
		if err != nil {
			return failed("닫지 못했습니다", err), nil, nil
		}
		return toolText("닫았습니다: "+l.Line, false), nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

// items is a list's items with their ids and checks: "i1 장갑 ✓, i2 베개".
func items(l List) string {
	parts := make([]string, 0, len(l.Items))
	for _, it := range l.Items {
		p := it.ID + " " + it.Text
		if it.Done {
			p += " ✓"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// failed is a change that did not happen, said so the conversation does not
// tell the operator it did.
func failed(what string, err error) *mcp.CallToolResult {
	return toolText(what+": "+err.Error()+"\n바뀐 것이 없습니다. 운영자에게 됐다고 말하지 마세요.", true)
}

func toolText(text string, isErr bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isErr, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
