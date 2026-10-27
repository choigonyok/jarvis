package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// endedSvc is jobs-svc with one job over (18, done) and one still waiting
// (19): an answer to the first is turned away as 410, as jobs-svc does.
type endedSvc struct {
	mu      sync.Mutex
	answers []string
}

func (f *endedSvc) serve(t *testing.T, waitingCard string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/jobs":
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []map[string]any{
				{"id": 18, "title": "탁상시계 구매", "state": "done"},
				{"id": 19, "title": "LP 재발매 확인", "state": "waiting", "waitingCard": waitingCard},
			}})
		case strings.HasSuffix(r.URL.Path, "/answer"):
			f.mu.Lock()
			f.answers = append(f.answers, r.URL.Path)
			f.mu.Unlock()
			if r.URL.Path == "/jobs/18/answer" {
				w.WriteHeader(http.StatusGone)
				_, _ = w.Write([]byte(`{"error":"끝난 작업이라 답을 전하지 않았습니다. 이어서 하려면 작업 탭에서 지시하세요."}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func askFor(props *proposal.Store, job int64, question string) string {
	raw, _ := json.Marshal(AskInput{JobID: job, JobTitle: "작업", Question: question})
	p, _ := props.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: action.Action{Kind: KindAsk, Input: raw},
		Card: action.Card{Title: question}})
	return p.ID
}

// A card answered after its job was over says the answer went nowhere - not
// "작업에 전했습니다" - and is not tried again (job 18, 2026-10-18).
func TestAnswerToAJobThatIsOverSaysSo(t *testing.T) {
	svc := &endedSvc{}
	props := proposal.NewStore(bus.New())
	m := New(NewStore(svc.serve(t, "").URL, ""), nil, props, &transcript{}, quietLog())
	id := askFor(props, 18, "쿠페이 비밀번호 입력이 자동으로 안 됩니다. 어떻게 할까요?")

	if err := props.Decide(id, proposal.Reject); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "카드가 끝난 작업이라고 말해야 합니다", func() bool {
		p, _ := props.Get(id)
		return p.Note == endedNote
	})
	if p, _ := props.Get(id); p.State != proposal.Rejected {
		t.Fatalf("반려는 반려로 남습니다: %+v", p)
	}
	if m.retryDue(time.Now().Add(24 * time.Hour)) {
		t.Fatal("끝난 작업에는 다시 전하지 않습니다")
	}
}

// The sweep takes down the cards of a job that is over - called off from the
// 작업 tab, say - and leaves those of a job still waiting alone.
func TestCardsOfAJobThatIsOverComeDown(t *testing.T) {
	svc := &endedSvc{}
	props := proposal.NewStore(bus.New())
	over := askFor(props, 18, "어떻게 할까요?")
	live := askFor(props, 19, "구매할까요?")
	m := New(NewStore(svc.serve(t, live).URL, ""), nil, props, &transcript{}, quietLog())

	m.sweep(context.Background())

	if p, _ := props.Get(over); p.State != proposal.Rejected || !strings.Contains(p.Note, "작업이 끝나 내렸습니다") {
		t.Fatalf("끝난 작업의 카드는 내려야 합니다: %+v", p)
	}
	if p, _ := props.Get(live); p.State != proposal.Pending {
		t.Fatalf("기다리는 작업의 카드는 그대로: %+v", p)
	}
	if got := svc.answers; len(got) != 0 {
		t.Fatalf("내린 카드는 작업에 답으로 가지 않습니다: %v", got)
	}
}
