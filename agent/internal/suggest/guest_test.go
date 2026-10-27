package suggest

import (
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
)

// Each side's reminders lay their card in that side's conversation only: the
// guest's agent lays the guest's and nothing else - not the operator's
// reminder, not mind's cards, not a list - and the operator's agent leaves
// the guest's alone.
func TestEachSideLaysItsOwnReminders(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	guestProps := proposal.NewStore(bus.New())
	guest := New(guestProps, Config{Guest: true}, log)
	owner, _ := setup(t)
	var guestSaw, ownerSaw []Notice
	var guestShown []string
	guest.cfg.Notice = func(n Notice) { guestSaw = append(guestSaw, n) }
	guest.cfg.Show = func(id, lead, key string) { guestShown = append(guestShown, id) }
	owner.cfg.Notice = func(n Notice) { ownerSaw = append(ownerSaw, n) }
	send := func(e *Engine, ev map[string]any) {
		raw, _ := json.Marshal(ev)
		rec := httptest.NewRecorder()
		e.Consume(rec, httptest.NewRequest(http.MethodPost, "/consume", strings.NewReader(string(raw))))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%v: %d", ev["type"], rec.Code)
		}
	}
	events := []map[string]any{
		{"type": "remind.asked", "key": "remind:rem_o:1",
			"data":   map[string]any{"card": true, "why": "10:00 에 알려 달라고 하셨어요", "body": ""},
			"notify": map[string]any{"tier": "now", "title": "운영자 약 먹기"}},
		{"type": "remind.asked", "key": "remind:rem_g:1",
			"data":   map[string]any{"card": true, "why": "집에 도착하셔서 알려 드려요", "body": "현관 앞에 두기", "checklist": "chk_g", "who": "guest"},
			"notify": map[string]any{"tier": "now", "title": "택배 챙기기", "body": "현관 앞에 두기\n남은 것: 상자\n집에 도착하셔서 알려 드려요", "audience": "guest"}},
		{"type": "remind.list", "key": "remind:rem_gl:1",
			"data":   map[string]any{"card": true, "why": "내일 캠핑이라 남은 것만 알려 드려요", "checklist": "chk_g2", "who": "guest"},
			"notify": map[string]any{"tier": "now", "title": "캠핑 준비물", "body": "남은 것: 장갑", "audience": "guest"}},
		// For the guest's phone without naming anyone in its data - as the
		// guest's token's own posts are: the guest's all the same.
		{"type": "remind.asked", "key": "remind:rem_gx:1",
			"data":   map[string]any{"card": true, "why": "손님 폰으로"},
			"notify": map[string]any{"tier": "now", "title": "손님 폰에만", "audience": "guest"}},
		{"type": mind.CardRaised, "data": map[string]any{"intentId": "int_g", "mode": "ask", "kind": "calendar.create", "title": "캘린더에 추가"}},
		{"type": ChecklistShown, "key": "checklist.shown:chk_g:a", "data": map[string]any{"checklist": "chk_g", "lead": "내일이에요", "who": "guest"}},
	}
	for _, ev := range events {
		send(guest, ev)
		send(owner, ev)
	}

	if len(guestSaw) != 3 || guestSaw[2].Key != "remind:rem_gx:1" {
		t.Fatalf("손님 대화에는 손님 리마인드 셋만: %+v", guestSaw)
	}
	if n := guestSaw[0]; n.Key != "remind:rem_g:1" || n.Title != "택배 챙기기" || n.Body != "현관 앞에 두기" || n.Why != "집에 도착하셔서 알려 드려요" || n.Checklist != "chk_g" {
		t.Fatalf("손님 리마인드: %+v", n)
	}
	if n := guestSaw[1]; n.Key != "remind:rem_gl:1" || n.Title != "캠핑 준비물" || n.Body != "남은 것: 장갑" || n.Checklist != "chk_g2" {
		t.Fatalf("손님 목록 리마인드: %+v", n)
	}
	if len(guestProps.List()) != 0 || len(guestShown) != 0 {
		t.Fatalf("손님 agent 는 mind 카드도 목록도 열지 않습니다: %+v %v", guestProps.List(), guestShown)
	}
	if len(ownerSaw) != 1 || ownerSaw[0].Key != "remind:rem_o:1" {
		t.Fatalf("운영자 대화에는 운영자 리마인드만: %+v", ownerSaw)
	}
}

// The guest's agent registers as agent-guest, for the guest's reminders,
// with its own token; the operator's as agent, for everything it takes.
func TestEachSideSubscribesAsItself(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URL   string   `json:"url"`
			Types []string `json:"types"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got[r.URL.Path] = r.Method + " " + r.Header.Get("Authorization") + " " + body.URL + " " + strings.Join(body.Types, ",")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := t.Context()
	New(proposal.NewStore(bus.New()), Config{EventsURL: srv.URL, Token: "guest-token", Guest: true}, log).
		Subscribe(ctx, "http://agent-guest:8081/consume")
	New(proposal.NewStore(bus.New()), Config{EventsURL: srv.URL, Token: "owner-token"}, log).
		Subscribe(ctx, "http://agent:8080/consume")
	if g := got["/subscriptions/agent-guest"]; g != "PUT Bearer guest-token http://agent-guest:8081/consume remind.*" {
		t.Fatalf("손님 agent 의 구독: %q", g)
	}
	if o := got["/subscriptions/agent"]; o != "PUT Bearer owner-token http://agent:8080/consume "+strings.Join(Types, ",") {
		t.Fatalf("운영자 agent 의 구독: %q", o)
	}
}
