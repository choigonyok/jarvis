// Package messages lets the conversation read the messages themselves -
// KakaoTalk, iMessage and this conversation - as mind-svc keeps them.
//
// Long-term memory holds what mind's interpreter took from them: facts of a
// few kinds (plans, people, tastes). Anything else - what a word someone used
// means, who said exactly what - is only in the text. Two tools, so a
// question costs little context: search_messages answers with a short
// stretch of each match, message_context with the few messages around one.
//
// Read only, and the operator's only: the guest agent is built without it.
package messages

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	Name       = "messages"
	ServerName = "messages"
)

// Tools is every tool this server has; both read.
var Tools = []string{"mcp__" + ServerName + "__search_messages", "mcp__" + ServerName + "__message_context"}

// Message is one message as mind-svc's /messages routes return it.
type Message struct {
	Ref      string    `json:"ref"`
	Source   string    `json:"source"`
	RoomName string    `json:"roomName"`
	Sender   string    `json:"sender"`
	IsMine   bool      `json:"isMine"`
	SentAt   time.Time `json:"sentAt"`
	Text     string    `json:"text"`
}

type Module struct {
	url, token string
	http       *http.Client
}

// New reads mind-svc at url with the services' token.
func New(url, token string) *Module {
	return &Module{url: strings.TrimRight(url, "/"), token: token, http: &http.Client{Timeout: 20 * time.Second}}
}

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) get(ctx context.Context, path string, q url.Values) ([]Message, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.url+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	res, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mind 에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	var out struct {
		Messages []Message `json:"messages"`
		Error    string    `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil && res.StatusCode < 300 {
		return nil, err
	}
	if res.StatusCode >= 300 {
		if out.Error != "" {
			return nil, fmt.Errorf("%s", out.Error)
		}
		return nil, fmt.Errorf("mind 가 %d 로 응답했습니다", res.StatusCode)
	}
	return out.Messages, nil
}

type SearchInput struct {
	Query string `json:"query" jsonschema:"찾을 낱말. 띄어 쓴 낱말은 모두 들어 있는 메시지만 찾는다. 조사가 붙어 있어도 찾는다(예: '갓생' 은 '갓생이라' 도). 문장 말고 핵심 낱말 한두 개로."`
	Room  string `json:"room,omitempty" jsonschema:"대화방 이름의 일부나 방 id 로 좁히기. 비우면 전부."`
	Since string `json:"since,omitempty" jsonschema:"이 날짜(YYYY-MM-DD) 이후만. 비우면 보존 기간 전부."`
}

type ContextInput struct {
	ID     string `json:"id" jsonschema:"search_messages 가 준 메시지 id"`
	Before int    `json:"before,omitempty" jsonschema:"앞 메시지 수(0-20). 비우면 5."`
	After  int    `json:"after,omitempty" jsonschema:"뒤 메시지 수(0-20). 비우면 5."`
}

func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "search_messages",
		Description: "카카오톡·iMessage·이 대화의 메시지 원문을 낱말로 찾는다(최신 먼저, 최대 10개, 메시지마다 찾은 낱말 둘레만). " +
			"search_memory 로 못 찾았을 때만 쓴다: 어떤 말의 뜻, 누가 정확히 뭐라고 했는지처럼 기억에 사실로 남지 않은 것. " +
			"고른 메시지의 앞뒤는 message_context 로 본다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
		q := url.Values{"q": {in.Query}, "limit": {"10"}}
		if in.Room != "" {
			q.Set("room", in.Room)
		}
		if in.Since != "" {
			q.Set("since", in.Since)
		}
		ms, err := m.get(ctx, "/messages/search", q)
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText(renderSearch(ms), false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "message_context",
		Description: "search_messages 로 찾은 메시지 하나와 같은 대화방의 앞뒤 메시지를 시간순으로 본다. 필요한 만큼만(기본 앞뒤 5개).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ContextInput) (*mcp.CallToolResult, any, error) {
		q := url.Values{"ref": {in.ID}, "before": {count(in.Before)}, "after": {count(in.After)}}
		ms, err := m.get(ctx, "/messages/context", q)
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText(renderContext(ms, in.ID), false), nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

// count is a before/after the model gave, 5 when it gave none.
func count(n int) string {
	if n <= 0 {
		return "5"
	}
	return fmt.Sprint(n)
}

func renderSearch(ms []Message) string {
	if len(ms) == 0 {
		return "찾은 메시지가 없습니다. 다른 낱말로 찾거나, 없다고 답하세요."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "찾은 메시지 %d개 (최신 먼저, 찾은 낱말 둘레만):\n", len(ms))
	for _, msg := range ms {
		fmt.Fprintf(&b, "- [%s] %s · %s · %s: %s\n", msg.Ref, stamp(msg.SentAt), room(msg), who(msg), msg.Text)
	}
	return b.String()
}

func renderContext(ms []Message, ref string) string {
	if len(ms) == 0 {
		return "메시지가 없습니다."
	}
	var b strings.Builder
	b.WriteString(room(ms[0]) + "\n")
	for _, msg := range ms {
		mark := "-"
		if msg.Ref == ref {
			mark = "▶"
		}
		fmt.Fprintf(&b, "%s %s %s: %s\n", mark, stamp(msg.SentAt), who(msg), msg.Text)
	}
	return b.String()
}

func stamp(t time.Time) string { return t.In(time.Local).Format("2006-01-02 15:04") }

func room(m Message) string {
	src := map[string]string{"kakao": "카카오톡", "imessage": "iMessage", "chat": "Jarvis 대화"}[m.Source]
	if src == "" {
		src = m.Source
	}
	if m.RoomName == "" {
		return src
	}
	return src + " " + m.RoomName
}

func who(m Message) string {
	switch {
	case m.IsMine:
		return "나"
	case m.Sender != "":
		return m.Sender
	}
	return "상대"
}

func toolText(s string, isErr bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isErr, Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
