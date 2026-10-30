package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/gocommon/record"
)

// heard is a runner that remembers what it was told.
type heard struct {
	bgRunner
	said []string
}

func (h *heard) Send(text string, _ []string) error {
	h.said = append(h.said, text)
	return nil
}

// What is said waits at the way in; the agent takes it from the queue, reads its
// words there, and starts the turn. What it cannot do is said on the screen, not
// tried again, and words it could not read yet are.
func TestWhatIsSaidComesFromTheQueue(t *testing.T) {
	var down bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch strings.TrimPrefix(r.URL.Path, "/chat/messages/") {
		case "msg-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "내일 일정 알려줘"})
		case "msg-photo":
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "이거 봐", "images": []string{"a.jpg"}})
		case "msg-quote":
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "이거", "ref": map[string]string{"id": "u-9-9"}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"turns": []thread.Turn{}, "hasMore": false})
		}
	}))
	defer srv.Close()
	events := bus.New()
	frames, stop := events.Subscribe()
	defer stop()
	run := &heard{}
	s := New(Deps{Thread: thread.NewStore(srv.URL, "", events), Proposals: proposal.NewStore(events), Runner: run, Bus: events, APIToken: "svc"})
	ctx := context.Background()

	if err := s.TakeMessage(ctx, queue.Message{SourceKey: "msg-1"}); err != nil {
		t.Fatal(err)
	}
	if len(run.said) != 1 || run.said[0] != "내일 일정 알려줘" {
		t.Fatalf("%v", run.said)
	}
	told := func(want string) {
		t.Helper()
		select {
		case f := <-frames:
			if f.Type != "error" || !strings.Contains(f.Message, want) {
				t.Fatalf("%+v", f)
			}
		case <-time.After(time.Second):
			t.Fatal("화면에 말하지 않았습니다")
		}
	}
	if err := s.TakeMessage(ctx, queue.Message{SourceKey: "msg-photo"}); err != nil {
		t.Fatal(err)
	}
	told("사진")
	if err := s.TakeMessage(ctx, queue.Message{SourceKey: "msg-quote"}); err != nil {
		t.Fatal(err)
	}
	told("참조한 메시지")
	if len(run.said) != 1 {
		t.Fatalf("시작하면 안 됩니다: %v", run.said)
	}
	down = true
	if err := s.TakeMessage(ctx, queue.Message{SourceKey: "msg-1"}); err == nil || errors.Is(err, queue.ErrRefused) {
		t.Fatalf("입구가 없으면 다시 해 봅니다: %v", err)
	}
}

// inputDoor is the way in as the agent sees it: an answer to read, and where to
// say how it went.
func inputDoor(t *testing.T, answers map[string]string) (*httptest.Server, *replies) {
	t.Helper()
	r := &replies{got: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		key := strings.TrimPrefix(q.URL.Path, "/chat/answers/")
		if name, ok := strings.CutSuffix(key, "/reply"); ok {
			body, _ := io.ReadAll(q.Body)
			r.mu.Lock()
			r.got[name] = string(body)
			r.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if body, ok := answers[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"turns": []thread.Turn{}, "hasMore": false})
	}))
	t.Cleanup(srv.Close)
	return srv, r
}

type replies struct {
	mu  sync.Mutex
	got map[string]string
}

func (r *replies) of(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got[key]
}

func answerOf(card, key string) queue.Message {
	raw, _ := json.Marshal(map[string]string{"card": card, "sealed": "x"})
	return queue.Message{Kind: record.KindAnswer, SourceKey: key, Payload: raw}
}

// The check of the to-be structure: a person's answer goes from the way in to
// what waits on the card, with no mind anywhere in it. A payment held for its
// card goes on as it was.
func TestAHeldPaymentGoesOnFromTheQueue(t *testing.T) {
	srv, said := inputDoor(t, map[string]string{"ans-1": `{"decision":"approved"}`, "ans-2": `{"decision":"approved"}`})
	events := bus.New()
	props := proposal.NewStore(events)
	s := New(Deps{Thread: thread.NewStore(srv.URL, "", events), Proposals: props, Runner: bgRunner{}, Bus: events, InterceptToken: "tok",
		FollowUp: &followUps{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s.holdDur = 5 * time.Second

	done := make(chan map[string]string, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/intercept", strings.NewReader(`{"url":"https://pay.coupang.com/x","method":"POST","host":"pay.coupang.com","event":"order"}`))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		s.postIntercept(rec, req)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		done <- out
	}()
	var card string
	for i := 0; i < 500 && (card == "" || s.payments.Holding() == 0); i++ {
		if list := props.List(); len(list) == 1 {
			card = list[0].ID
		}
		time.Sleep(2 * time.Millisecond)
	}
	if card == "" {
		t.Fatal("카드가 올라가지 않았습니다")
	}
	if err := s.Take(context.Background(), answerOf(card, "ans-1")); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		if out["decision"] != string(proposal.Approve) || out["why"] != "held" {
			t.Fatalf("%+v", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("멈춘 결제가 이어지지 않았습니다")
	}
	if got := said.of("ans-1"); !strings.Contains(got, "204") {
		t.Fatalf("누른 쪽에 알린 것: %q", got)
	}
	// Answered twice is said so, and changes nothing.
	if err := s.Take(context.Background(), answerOf(card, "ans-2")); err != nil {
		t.Fatal(err)
	}
	if got := said.of("ans-2"); !strings.Contains(got, "409") || !strings.Contains(got, "이미") {
		t.Fatalf("%q", got)
	}
}

// A card with a secret field is answered directly and not as a record; any
// other card is not answered at this door.
func TestOnlyASecretCardIsAnsweredHere(t *testing.T) {
	props := proposal.NewStore(bus.New())
	s := &Server{proposals: props, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p, _ := props.Open(proposal.Proposal{Action: action.Action{Kind: "shop.sell", Input: json.RawMessage(`{}`)}, Card: action.Card{Title: "sell"}})
	req := httptest.NewRequest(http.MethodPost, "/proposals/"+p.ID+"/decision", strings.NewReader(`{"decision":"approved"}`))
	req.SetPathValue("id", p.ID)
	rec := httptest.NewRecorder()
	s.postDecision(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("%d", rec.Code)
	}
	if got, _ := props.Get(p.ID); got.State != proposal.Pending {
		t.Fatalf("%s", got.State)
	}
}
