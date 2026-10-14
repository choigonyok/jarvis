package suggest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/mind"
)

// fakeMind is mind-svc's API: it records what it was asked.
type fakeMind struct {
	mu    sync.Mutex
	calls []string
	body  []map[string]any
	reply map[string]any
}

func (f *fakeMind) server(t *testing.T) *mind.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &m)
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		f.body = append(f.body, m)
		f.mu.Unlock()
		out := f.reply
		if out == nil {
			out = map[string]any{"id": "int_1", "status": "pending", "decision": "ask", "card": true}
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return &mind.Client{URL: srv.URL, Token: "tok"}
}

func deliver(e *Engine, typ string, data any) {
	raw, _ := json.Marshal(map[string]any{"type": typ, "data": data})
	req := httptest.NewRequest(http.MethodPost, "/consume", strings.NewReader(string(raw)))
	e.Consume(httptest.NewRecorder(), req)
}

// A mind card is an approval card at once - no judging turn - opened once
// however often it is delivered, closed when mind says it was answered, and
// a rejection goes back to mind.
func TestMindCards(t *testing.T) {
	e, bg, props, _ := setup(t)
	fm := &fakeMind{}
	e.cfg.Mind = fm.server(t)
	card := map[string]any{"intentId": "int_a", "mode": "ask", "skill": "schedule", "kind": "calendar.create",
		"title": "캘린더에 추가: 10/17(토) 강릉", "preview": "내 일정에 추가: 2026-10-17 강릉", "reason": "대화에서 확정됐는데 캘린더에 없어요",
		"evidence": []map[string]string{{"ref": "kakao:1", "quote": "그러자"}}, "confidence": 0.9}
	deliver(e, mind.CardRaised, card)
	deliver(e, mind.CardRaised, card)
	list := props.List()
	if len(list) != 1 || list[0].Action.Kind != mind.Approve || len(bg.tasks) != 0 {
		t.Fatalf("카드 한 장, 판단 턴 없이: %+v / %d", list, len(bg.tasks))
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

// With mind, what the judging turn proposes goes to mind's gate, not
// straight to a card.
func TestProposeGoesToMind(t *testing.T) {
	e, bg, props, _ := setup(t)
	fm := &fakeMind{reply: map[string]any{"id": "int_1", "decision": "drop", "why": "최근에 거절하신 주제"}}
	e.cfg.Mind = fm.server(t)
	bg.input = &ProposeInput{Title: "리밸런싱", Body: "금 3%p 부족", Why: "비중 이탈", Action: "none", Confidence: 0.8}
	if err := e.judge(context.Background(), &job{ev: Event{Type: "assets.band"}, topic: "rebalance"}); err != nil || bg.err != nil {
		t.Fatalf("%v %v", err, bg.err)
	}
	if len(props.List()) != 0 || !strings.Contains(bg.reply, "올리지 않았습니다") {
		t.Fatalf("mind 가 버린 제안은 카드가 아닙니다: %v %q", props.List(), bg.reply)
	}
	if fm.body[0]["topic"] != "rebalance" || fm.body[0]["kind"] != "note" || fm.body[0]["skill"] != "suggest" {
		t.Fatalf("mind 에 넘긴 의도: %v", fm.body[0])
	}
}
