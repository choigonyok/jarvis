package suggest

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

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/mind"
	"github.com/choigonyok/jarvis/gocommon/queue"
)

// fakeMind is mind-svc's API: it records what it was asked.
type fakeMind struct {
	mu    sync.Mutex
	calls []string
	body  []map[string]any
	reply map[string]any
	// down makes every call fail, as an unreachable mind would.
	down bool
}

func (f *fakeMind) server(t *testing.T) *mind.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &m)
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		f.body = append(f.body, m)
		down, out := f.down, f.reply
		f.mu.Unlock()
		if down {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if out == nil {
			out = map[string]any{"id": "int_1", "status": "pending", "decision": "ask", "card": true}
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return &mind.Client{URL: srv.URL, Token: "tok"}
}

func setup(t *testing.T) (*Engine, *proposal.Store) {
	t.Helper()
	props := proposal.NewStore(bus.New())
	return New(props, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))), props
}

func deliver(e *Engine, typ string, data any) {
	_ = e.Take(context.Background(), message(map[string]any{"type": typ, "data": data}))
}

// message is an event as the stream hands it over: the data in the payload,
// the wording beside it.
func message(ev map[string]any) queue.Message {
	raw, _ := json.Marshal(ev["data"])
	payload := map[string]any{}
	_ = json.Unmarshal(raw, &payload)
	if n, ok := ev["notify"]; ok {
		payload["notify"] = n
	}
	body, _ := json.Marshal(payload)
	kind, _ := ev["type"].(string)
	key, _ := ev["key"].(string)
	return queue.Message{Kind: kind, SourceKey: key, Payload: body}
}

// A mind card is an approval card at once - no judging turn - opened once
// however often it is delivered, closed when mind says it was answered, and
// a rejection goes back to mind.
func TestMindCards(t *testing.T) {
	e, props := setup(t)
	fm := &fakeMind{}
	e.cfg.Mind = fm.server(t)
	card := map[string]any{"intentId": "int_a", "mode": "ask", "skill": "schedule", "kind": "calendar.create",
		"title": "캘린더에 추가: 10/17(토) 강릉", "preview": "내 일정에 추가: 2026-10-17 강릉", "reason": "대화에서 확정됐는데 캘린더에 없어요",
		"evidence": []map[string]string{{"ref": "kakao:1", "quote": "그러자"}}, "confidence": 0.9}
	deliver(e, mind.CardRaised, card)
	deliver(e, mind.CardRaised, card)
	list := props.List()
	if len(list) != 1 || list[0].Action.Kind != mind.Approve {
		t.Fatalf("카드 한 장: %+v", list)
	}
	if !strings.Contains(list[0].Card.Body, "“그러자”") || !strings.Contains(list[0].Card.Body, "내 일정에 추가") {
		t.Fatalf("근거와 미리보기가 카드에: %q", list[0].Card.Body)
	}

	e.OnDecide(list[0], proposal.Reject)
	if len(fm.calls) != 1 || fm.calls[0] != "POST /api/intents/int_a/reject Bearer tok" {
		t.Fatalf("거절이 mind 로: %v", fm.calls)
	}

	card["intentId"] = "int_b"
	deliver(e, mind.CardRaised, card)
	deliver(e, mind.CardClosed, map[string]string{"intentId": "int_b", "status": "superseded"})
	if p, _ := e.findCard("int_b"); p.State != proposal.Rejected {
		t.Fatalf("mind 에서 닫힌 카드는 닫혀야 합니다: %+v", p)
	}
}

// What mind told at its moment is laid in the conversation when the notice
// asked for a card - with the reminder's own line rather than the push's,
// and its list - and not otherwise; a list mind shows is shown.
func TestNoticesBecomeCards(t *testing.T) {
	e, _ := setup(t)
	var notices []Notice
	var shown []string
	e.cfg.Notice = func(n Notice) { notices = append(notices, n) }
	e.cfg.Show = func(id, lead, key string) { shown = append(shown, id+"|"+lead+"|"+key) }
	send := func(ev map[string]any) {
		if err := e.Take(context.Background(), message(ev)); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]any{"type": "remind.asked", "key": "remind:rem_1:1",
		"data":   map[string]any{"card": true, "why": "집에 도착하셔서 알려 드려요", "body": "", "checklist": "chk_1"},
		"notify": map[string]any{"tier": "now", "title": "집에 가서 할 일", "body": "남은 것: 빨래\n집에 도착하셔서 알려 드려요"}})
	send(map[string]any{"type": "remind.rain", "key": "mind:int_9",
		"data":   map[string]any{"card": true, "why": "비 오는 날 집을 나섬"},
		"notify": map[string]any{"tier": "now", "title": "15시부터 비 · 우산 챙기세요", "body": "오늘 집 날씨: 비"}})
	// An older mind's notice (no card asked for), and a digest line.
	send(map[string]any{"type": "remind.asked", "key": "remind:rem_2:1", "data": map[string]any{},
		"notify": map[string]any{"tier": "now", "title": "옛 알림"}})
	send(map[string]any{"type": "remind.asked", "key": "remind:rem_3:1", "data": map[string]any{"card": true},
		"notify": map[string]any{"tier": "digest", "title": "저녁 요약"}})
	send(map[string]any{"type": ChecklistShown, "key": "checklist.shown:chk_1:a", "data": map[string]any{"checklist": "chk_1", "lead": "내일 캠핑이에요"}})

	if len(notices) != 2 {
		t.Fatalf("카드를 청한 지금의 알림만: %+v", notices)
	}
	if n := notices[0]; n.Key != "remind:rem_1:1" || n.Title != "집에 가서 할 일" || n.Body != "" || n.Why != "집에 도착하셔서 알려 드려요" || n.Checklist != "chk_1" {
		t.Fatalf("리마인드: %+v", n)
	}
	if n := notices[1]; n.Body != "오늘 집 날씨: 비" || n.Why != "비 오는 날 집을 나섬" {
		t.Fatalf("비 알림: %+v", n)
	}
	if len(shown) != 1 || shown[0] != "chk_1|내일 캠핑이에요|checklist.shown:chk_1:a" {
		t.Fatalf("mind 가 띄운 목록: %v", shown)
	}
	for _, typ := range Types {
		if typ == "remind.*" {
			return
		}
	}
	t.Fatalf("구독에 remind.* 가 없습니다: %v", Types)
}
