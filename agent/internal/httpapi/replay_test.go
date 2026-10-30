package httpapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/holder"
)

// A replay feeds recorded payments to the payment path as it is today and
// compares what comes out with what was recorded, in the order they were
// received. The scenarios under testdata/replay hold what must not change -
// stage 7 of the to-be structure moves this path and has to pass them - and
// scripts/harness replay runs a real stretch of the records through here:
//
//	JARVIS_REPLAY_FILE=records.jsonl go test ./internal/httpapi -run Replay
type replayRecord struct {
	Kind        string          `json:"kind"`
	SourceKey   string          `json:"source_key"`
	CausationID string          `json:"causation_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Payload     json.RawMessage `json:"payload"`
}

func readReplay(path string) ([]replayRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []replayRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for line := 1; sc.Scan(); line++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var r replayRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

type replayClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *replayClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.After(c.now) {
		c.now = t
	}
}

func (c *replayClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

type replayCall struct {
	done chan map[string]string
	got  map[string]string
}

type replayQuery struct {
	Payment
	Job        int64 `json:"job"`
	Background bool  `json:"background"`
}

// replayer is one run: the path as it is, a clock that is the records', and
// the cards as the records name them against the ones this run raised.
type replayer struct {
	s      *Server
	props  *proposal.Store
	jobs   *fakeJobs
	runner *switchRunner
	fu     *followUps
	clock  *replayClock
	cards  map[string]string // recorded card → card of this run
	calls  map[string]*replayCall
	wrong  []string
}

func newReplayer() *replayer {
	r := &replayer{
		props: proposal.NewStore(bus.New()), jobs: &fakeJobs{}, runner: &switchRunner{}, fu: &followUps{},
		clock: &replayClock{}, cards: map[string]string{}, calls: map[string]*replayCall{},
	}
	r.s = New(Deps{Proposals: r.props, Runner: r.runner, InterceptToken: "tok", FollowUp: r.fu, Jobs: r.jobs,
		BackgroundLogin: []string{"nid.naver.com"},
		Log:             slog.New(slog.NewTextHandler(io.Discard, nil))})
	r.s.payments.Now = r.clock.Now
	return r
}

func (r *replayer) failf(i int, format string, args ...any) {
	r.wrong = append(r.wrong, fmt.Sprintf("%d번째 기록: %s", i+1, fmt.Sprintf(format, args...)))
}

// holdFor is how long a question will be held in this run. Only the answer
// comes in the records' time: an answer inside the hold is waited for, and a
// hold that ran out in the records is over at once here.
func holdFor(recs []replayRecord, i int) time.Duration {
	for _, next := range recs[i+1:] {
		if next.OccurredAt.IsZero() {
			continue
		}
		if next.OccurredAt.Sub(recs[i].OccurredAt) > holder.Hold {
			break
		}
		if next.Kind == KindIntentAnswer {
			return 20 * time.Second
		}
	}
	return time.Millisecond
}

// ask puts the question and waits until it was answered or is held at a card.
func (r *replayer) ask(q replayQuery, hold time.Duration) *replayCall {
	r.runner.mu.Lock()
	r.runner.bg = q.Background
	r.runner.mu.Unlock()
	r.jobs.running(q.Job)
	r.s.holdDur = hold

	body, _ := json.Marshal(q.Payment)
	c := &replayCall{done: make(chan map[string]string, 1)}
	before := r.holding()
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/intercept", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		r.s.postIntercept(rec, req)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		c.done <- out
	}()
	// Held, the run has read holdDur: the next question may change it.
	for i := 0; i < 2000; i++ {
		select {
		case c.got = <-c.done:
			return c
		default:
		}
		if r.holding() > before {
			return c
		}
		time.Sleep(time.Millisecond)
	}
	return c
}

// holding is how many stopped requests are held on a card.
func (r *replayer) holding() int {
	return r.s.payments.Holding()
}

func (c *replayCall) answer() map[string]string {
	if c.got == nil {
		select {
		case c.got = <-c.done:
		case <-time.After(30 * time.Second):
		}
	}
	return c.got
}

func (r *replayer) run(recs []replayRecord) []string {
	for i, rec := range recs {
		if !rec.OccurredAt.IsZero() {
			r.clock.set(rec.OccurredAt)
		}
		switch rec.Kind {
		case "job.run_start":
			var p struct {
				JobID int64 `json:"job_id"`
			}
			_ = json.Unmarshal(rec.Payload, &p)
			r.jobs.running(p.JobID)
		case "job.run_end":
			r.jobs.running(0)
		case KindGuardQuery:
			var q replayQuery
			if err := json.Unmarshal(rec.Payload, &q); err != nil {
				r.failf(i, "%v", err)
				continue
			}
			r.calls[rec.SourceKey] = r.ask(q, holdFor(recs, i))
		case KindPaymentRequested:
			r.requested(i, rec)
		case KindIntentAnswer:
			r.answered(i, rec)
		case KindGuardAnswer:
			r.compare(i, rec)
		}
	}
	for key, c := range r.calls {
		if c.got == nil && len(c.done) == 0 {
			r.wrong = append(r.wrong, "답이 기록되지 않은 질의가 아직 멈춰 있습니다: "+key)
		}
	}
	return r.wrong
}

// requested finds the card this run raised where the records raised one.
func (r *replayer) requested(i int, rec replayRecord) {
	var p struct {
		Card  string `json:"card"`
		Title string `json:"title"`
		Site  string `json:"site"`
		Job   int64  `json:"job"`
	}
	_ = json.Unmarshal(rec.Payload, &p)
	if _, seen := r.cards[p.Card]; seen {
		return
	}
	known := map[string]bool{}
	for _, id := range r.cards {
		known[id] = true
	}
	for _, q := range r.props.List() {
		pay := paymentOf(q.Action.Input)
		if q.Origin != proposal.OriginIntercept || known[q.ID] || pay.grantSite() != p.Site || pay.JobID != p.Job {
			continue
		}
		if q.Card.Title != p.Title {
			r.failf(i, "카드 %s 의 제목이 %q 인데 재생에서는 %q", p.Card, p.Title, q.Card.Title)
		}
		r.cards[p.Card] = q.ID
		return
	}
	r.failf(i, "%s 에 카드 %s 가 올라가야 하는데 올라가지 않았습니다", p.Site, p.Card)
}

// answered gives the recorded answer to the card, and waits until the
// payment path has acted on it.
func (r *replayer) answered(i int, rec replayRecord) {
	var p struct {
		Card     string `json:"card"`
		Decision string `json:"decision"`
	}
	_ = json.Unmarshal(rec.Payload, &p)
	id, ok := r.cards[p.Card]
	if !ok {
		return // a card that is not a payment's, or one this run did not raise
	}
	acted := func() int {
		r.jobs.mu.Lock()
		defer r.jobs.mu.Unlock()
		r.fu.mu.Lock()
		defer r.fu.mu.Unlock()
		return len(r.jobs.decided) + len(r.fu.prompts)
	}
	before := acted()
	if err := r.props.Decide(id, proposal.Decision(p.Decision)); err != nil {
		r.failf(i, "카드 %s: %v", p.Card, err)
		return
	}
	for j := 0; j < 2000 && acted() == before; j++ {
		time.Sleep(time.Millisecond)
	}
}

// compare holds what came of a question to what was recorded.
func (r *replayer) compare(i int, rec replayRecord) {
	var want struct {
		Decision string `json:"decision"`
		Why      string `json:"why"`
		Card     string `json:"card"`
	}
	_ = json.Unmarshal(rec.Payload, &want)
	c := r.calls[rec.CausationID]
	if c == nil {
		r.failf(i, "질의 %s 가 없습니다", rec.CausationID)
		return
	}
	got := c.answer()
	if got == nil {
		r.failf(i, "질의 %s 에 답이 없습니다", rec.CausationID)
		return
	}
	if got["decision"] != want.Decision || got["why"] != want.Why {
		r.failf(i, "질의 %s: 기록은 %s(%s), 재생은 %s(%s)", rec.CausationID, want.Decision, want.Why, got["decision"], got["why"])
	}
	if card := r.cards[want.Card]; want.Card != "" && got["card"] != card {
		r.failf(i, "질의 %s: 카드가 %s 여야 하는데 %s", rec.CausationID, want.Card, got["card"])
	}
}

func TestReplayScenarios(t *testing.T) {
	files, err := filepath.Glob("testdata/replay/*.jsonl")
	if err != nil || len(files) == 0 {
		t.Fatalf("재생할 시나리오가 없습니다: %v", err)
	}
	for _, f := range files {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".jsonl"), func(t *testing.T) {
			recs, err := readReplay(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range newReplayer().run(recs) {
				t.Error(w)
			}
		})
	}
}

// A stretch of real records, exported by scripts/harness.
func TestReplayFile(t *testing.T) {
	path := os.Getenv("JARVIS_REPLAY_FILE")
	if path == "" {
		t.Skip("JARVIS_REPLAY_FILE 이 없습니다")
	}
	recs, err := readReplay(path)
	if err != nil {
		t.Fatal(err)
	}
	wrong := newReplayer().run(recs)
	for _, w := range wrong {
		t.Error(w)
	}
	t.Logf("기록 %d개를 재생했고 다른 곳이 %d개입니다", len(recs), len(wrong))
}

// The replay has to catch a change: a card where the records had none, and a
// verdict that differs, must both be told.
func TestReplayCatchesADifference(t *testing.T) {
	recs := []replayRecord{
		{Kind: KindGuardQuery, SourceKey: "q", OccurredAt: time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC),
			Payload: json.RawMessage(`{"url":"pay.naver.com/x","method":"POST","host":"pay.naver.com"}`)},
		{Kind: KindGuardAnswer, CausationID: "q", OccurredAt: time.Date(2026, 10, 30, 1, 0, 1, 0, time.UTC),
			Payload: json.RawMessage(`{"decision":"approved","why":"granted"}`)},
	}
	wrong := newReplayer().run(recs)
	if len(wrong) != 1 || !strings.Contains(wrong[0], "기록은 approved(granted), 재생은 rejected(waiting)") {
		t.Fatalf("다른 곳을 말해야 합니다: %v", wrong)
	}
}
