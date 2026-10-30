package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/gocommon/record"
)

func recordingServer(t *testing.T, d Deps) (*Server, *record.Memory) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w, mem := record.NewMemory(log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	d.Records, d.Log, d.InterceptToken = w, log, "tok"
	return New(d), mem
}

func kindIs(kind string) func(record.Record) bool {
	return func(r record.Record) bool { return r.Kind == kind }
}

// One payment leaves its chain: the question, the card it raised, how the
// card was answered and what the question came to.
func TestAPaymentLeavesItsChain(t *testing.T) {
	props := proposal.NewStore(bus.New())
	jobs := &fakeJobs{id: 3, title: "LP"}
	s, mem := recordingServer(t, Deps{Proposals: props, Runner: &switchRunner{bg: true}, FollowUp: &followUps{}, Jobs: jobs})
	s.holdDur = 3 * time.Second

	got := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/intercept", strings.NewReader(
			`{"url":"https://stdpay.inicis.com/payMain/pay?oid=SECRET&resultCode=0000","method":"POST","host":"stdpay.inicis.com","window":true,"amount":"113000","item":"LP"}`))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		s.postIntercept(rec, req)
		got <- rec.Code
	}()
	asked, ok := mem.Find(kindIs(KindGuardQuery))
	if !ok {
		t.Fatal("질의가 먼저 남아야 합니다")
	}
	raised, ok := mem.Find(kindIs(KindPaymentRequested))
	if !ok {
		t.Fatal("카드가 남아야 합니다")
	}
	if raised.CausationID != asked.SourceKey {
		t.Fatalf("카드는 질의에서 나왔습니다: %q", raised.CausationID)
	}
	card := raised.Payload.(map[string]any)["card"].(string)
	if err := props.Decide(card, proposal.Approve); err != nil {
		t.Fatal(err)
	}
	<-got
	answer, ok := mem.Find(kindIs(KindGuardAnswer))
	if !ok {
		t.Fatal("답이 남아야 합니다")
	}
	body := answer.Payload.(map[string]any)
	if answer.CausationID != asked.SourceKey || body["decision"] != "approved" || body["why"] != "held" || body["card"] != card {
		t.Fatalf("답: %+v", answer)
	}
	decided, ok := mem.Find(kindIs(KindIntentAnswer))
	if !ok {
		t.Fatal("카드의 답이 남아야 합니다")
	}
	if decided.Actor != "operator" || decided.Tier != record.Durable || decided.CausationID != "requested:"+card {
		t.Fatalf("카드의 답: %+v", decided)
	}
	for _, r := range []record.Record{asked, raised, answer, decided} {
		if r.TraceID != "job:3" {
			t.Fatalf("%s 의 흐름: %q", r.Kind, r.TraceID)
		}
		if r.Tier != record.Durable {
			t.Fatalf("%s 의 층: %q", r.Kind, r.Tier)
		}
		raw, _ := json.Marshal(r.Payload)
		if strings.Contains(string(raw), "SECRET") {
			t.Fatalf("%s 에 값이 남았습니다: %s", r.Kind, raw)
		}
	}
	if shape := asked.Payload.(map[string]any)["url"]; shape != "stdpay.inicis.com/payMain/pay?oid&resultCode=0000" {
		t.Fatalf("주소의 모양: %v", shape)
	}
}

// A form's fields and a reason stay out of the record of an answer.
func TestAnAnswerKeepsNoWords(t *testing.T) {
	props := proposal.NewStore(bus.New())
	_, mem := recordingServer(t, Deps{Proposals: props, Runner: bgRunner{}})
	p, _ := props.Open(proposal.Proposal{
		Origin: proposal.OriginChat,
		Action: action.Action{Kind: "mcp__vault__save_login", Input: json.RawMessage(`{"password":"hunter2"}`)},
		Card:   action.Card{Title: "로그인 저장", Body: "비밀 본문"},
	})
	if err := props.DecideWhy(p.ID, proposal.Reject, "이건 내 전화번호라서"); err != nil {
		t.Fatal(err)
	}
	r, ok := mem.Find(kindIs(KindIntentAnswer))
	if !ok {
		t.Fatal("답이 남아야 합니다")
	}
	raw, _ := json.Marshal(r.Payload)
	for _, no := range []string{"hunter2", "비밀 본문", "전화번호"} {
		if strings.Contains(string(raw), no) {
			t.Fatalf("%q 가 남았습니다: %s", no, raw)
		}
	}
	if r.Tier != record.Raw || r.Payload.(map[string]any)["hasReason"] != true {
		t.Fatalf("기록: %+v", r)
	}
}

func TestGuardRecords(t *testing.T) {
	s, mem := recordingServer(t, Deps{Proposals: proposal.NewStore(bus.New()), Runner: bgRunner{}, Jobs: &fakeJobs{id: 5}})
	post := func(token, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/intercept/records", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	good := `{"records":[{"kind":"guard.refused","key":"b-1","at":"2026-10-30T01:00:00Z","subject":"pay.danal.co.kr","payload":{"why":"blocked"}},
		{"kind":"guard.flow","key":"b-2","at":"2026-10-30T01:00:01Z","subject":"stdpay.inicis.com","payload":{"tab":"abc"}}]}`
	if code := post("nope", good); code != http.StatusUnauthorized {
		t.Fatalf("토큰 없이: %d", code)
	}
	if code := post("tok", good); code != http.StatusNoContent {
		t.Fatalf("응답: %d", code)
	}
	refused, ok := mem.Find(kindIs("guard.refused"))
	if !ok || refused.Source != "guard" || refused.Tier != record.Durable || refused.TraceID != "job:5" || refused.OccurredAt.IsZero() {
		t.Fatalf("막은 기록: %+v", refused)
	}
	flow, ok := mem.Find(kindIs("guard.flow"))
	if !ok || flow.Tier != record.Raw {
		t.Fatalf("길의 기록: %+v", flow)
	}
	if code := post("tok", `{"records":[{"kind":"intent.answer","key":"x","at":"2026-10-30T01:00:00Z"}]}`); code != http.StatusBadRequest {
		t.Fatalf("가드는 사람의 답을 낼 수 없습니다: %d", code)
	}
}

func TestURLShapeKeepsNoValues(t *testing.T) {
	for in, want := range map[string]string{
		"https://stdpay.inicis.com/payMain/pay":                 "stdpay.inicis.com/payMain/pay",
		"https://a.example/p?token=abc&amount=1000":             "a.example/p?token&amount",
		"https://a.example/p?resultCode=0000&x=1":               "a.example/p?resultCode=0000&x",
		"https://a.example/pay;PAY_JSESSIONID=SECRET?oid=1":     "a.example/pay;…?oid",
		"https://a.example/p?resultcode=0000000000000000000000": "a.example/p?resultcode=000000000000",
	} {
		if got := urlShape(in); got != want {
			t.Errorf("%s: %q", in, got)
		}
	}
}

func TestHealthReportsTheRecordsLoop(t *testing.T) {
	s, _ := recordingServer(t, Deps{Proposals: proposal.NewStore(bus.New()), Runner: bgRunner{}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("/health: %d %s", rec.Code, rec.Body.String())
	}
}
