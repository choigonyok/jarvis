package checklist

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// fakeMind is mind's /api/checklists, keeping one list.
type fakeMind struct {
	mu    sync.Mutex
	list  List
	calls []string
}

func (f *fakeMind) serve(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/checklists":
			f.list = List{ID: "chk_1", Title: body["title"].(string), Status: "active"}
			for i, it := range body["items"].([]any) {
				f.list.Items = append(f.list.Items, Item{ID: "i" + string(rune('1'+i)), Text: it.(string)})
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/items/i2"):
			f.list.Items[1].Done = body["done"].(bool)
			f.list.Items[1].By = body["by"].(string)
		case r.Method == http.MethodGet && r.URL.Path == "/api/checklists":
			_ = json.NewEncoder(w).Encode(map[string]any{"checklists": []List{f.list}})
			return
		case r.URL.Path != "/api/checklists/chk_1":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "그런 체크리스트가 없습니다"})
			return
		}
		f.list.Line = "[" + f.list.ID + "] " + f.list.Title
		_ = json.NewEncoder(w).Encode(map[string]any{"checklist": f.list})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// conversation stands in for the transcript: the cards laid in it.
type conversation struct {
	mu    sync.Mutex
	cards []thread.Origin
	leads [][]string
}

func (c *conversation) Card(o thread.Origin, paragraphs []string) *thread.Turn {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cards = append(c.cards, o)
	c.leads = append(c.leads, paragraphs)
	return &thread.Turn{ID: "a-1", Origin: &o}
}

func (c *conversation) Told(kind, ref string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, o := range c.cards {
		if o.Kind == kind && o.Ref == ref && ref != "" {
			return true
		}
	}
	return false
}

func session(t *testing.T, m *Module) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func call(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func published(t *testing.T, events <-chan bus.Event) List {
	t.Helper()
	select {
	case ev := <-events:
		if ev.Type != "checklist" {
			t.Fatalf("이벤트 %s", ev.Type)
		}
		return ev.Checklist.(List)
	case <-time.After(2 * time.Second):
		t.Fatal("목록이 화면으로 가지 않았습니다")
	}
	return List{}
}

// A list made in conversation is laid in it as a card at once, and every
// screen hears of it and of each tick; one shown for mind is laid once
// however often mind's event comes.
func TestChecklistIsACardInTheConversation(t *testing.T) {
	mind := &fakeMind{}
	chat := &conversation{}
	b := bus.New()
	events, stop := b.Subscribe()
	defer stop()
	m := New(mind.serve(t), "tok", chat, b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := session(t, m)

	text, isErr := call(t, s, "make_checklist", map[string]any{"title": "캠핑 준비물", "items": []string{"장갑", "베개"}, "lead": "금요일 저녁에 다시 띄워 드릴게요"})
	if isErr || !strings.Contains(text, "카드로 띄웠습니다") || !strings.Contains(text, "i1 장갑") {
		t.Fatalf("만들기: %q", text)
	}
	if len(chat.cards) != 1 || chat.cards[0].Kind != thread.OriginChecklist || chat.cards[0].Checklist != "chk_1" || chat.leads[0][0] != "금요일 저녁에 다시 띄워 드릴게요" {
		t.Fatalf("대화에 카드: %+v %v", chat.cards, chat.leads)
	}
	if l := published(t, events); l.ID != "chk_1" {
		t.Fatalf("화면에 간 목록: %+v", l)
	}

	got, err := m.Check(context.Background(), "chk_1", "i2", true)
	if err != nil || !got.(List).Items[1].Done || got.(List).Items[1].By != "card" {
		t.Fatalf("카드에서 체크: %+v %v", got, err)
	}
	if l := published(t, events); !l.Items[1].Done {
		t.Fatal("체크한 상태가 다른 화면에 가지 않았습니다")
	}

	for range 2 {
		if _, err := m.ShowAs(context.Background(), "chk_1", "내일 출발이에요", "checklist.shown:chk_1:x"); err != nil {
			t.Fatal(err)
		}
	}
	if len(chat.cards) != 2 || chat.cards[1].Ref != "checklist.shown:chk_1:x" {
		t.Fatalf("mind 가 띄운 목록은 한 번: %+v", chat.cards)
	}

	if text, isErr := call(t, s, "show_checklist", map[string]any{"id": "chk_9"}); !isErr || !strings.Contains(text, "바뀐 것이 없습니다") {
		t.Fatalf("없는 목록: %q", text)
	}
	found, err := m.Find(context.Background(), []string{"chk_1"})
	if err != nil || found["chk_1"].(List).Title != "캠핑 준비물" {
		t.Fatalf("찾기: %+v %v", found, err)
	}
}
