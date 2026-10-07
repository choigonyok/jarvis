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
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

type bgRunner struct{ bg bool }

func (r bgRunner) Send(string, []string) error { return nil }
func (r bgRunner) InBackground() bool          { return r.bg }

// While a background task drives the browser, a payment host is refused on
// the spot: no card is opened for a person who did not start anything.
func TestInterceptRefusesDuringBackground(t *testing.T) {
	props := proposal.NewStore(bus.New())
	s := &Server{
		runner:         bgRunner{bg: true},
		proposals:      props,
		interceptToken: "tok",
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest(http.MethodPost, "/intercept",
		strings.NewReader(`{"url":"https://pay.naver.com/pcpay","method":"GET","host":"pay.naver.com"}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	s.postIntercept(rec, req)

	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["decision"] != string(proposal.Reject) {
		t.Fatalf("decision %q", body["decision"])
	}
	if n := len(props.List()); n != 0 {
		t.Fatalf("카드가 %d 장 열렸습니다", n)
	}

	// The sign-in host the operator allowed goes through, still without a card.
	s.backgroundLogin = hostSet([]string{"nid.naver.com"})
	req = httptest.NewRequest(http.MethodPost, "/intercept",
		strings.NewReader(`{"url":"https://nid.naver.com/nidlogin.login","method":"POST","host":"nid.naver.com"}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	s.postIntercept(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["decision"] != string(proposal.Approve) || len(props.List()) != 0 {
		t.Fatalf("로그인 호스트: decision %q, cards %d", body["decision"], len(props.List()))
	}
	// ...and a payment host is still refused.
	req = httptest.NewRequest(http.MethodPost, "/intercept",
		strings.NewReader(`{"url":"https://pay.naver.com/pcpay","method":"POST","host":"pay.naver.com"}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	s.postIntercept(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["decision"] != string(proposal.Reject) {
		t.Fatalf("결제 호스트가 통과했습니다: %q", body["decision"])
	}
}

func TestGuard(t *testing.T) {
	reached := false
	next := func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}

	cases := []struct {
		name   string
		token  string
		header string
		want   int
	}{
		{"unset token lets everything through", "", "", http.StatusOK},
		{"correct token passes", "s3cret", "Bearer s3cret", http.StatusOK},
		{"missing header is refused", "s3cret", "", http.StatusUnauthorized},
		{"wrong token is refused", "s3cret", "Bearer nope", http.StatusUnauthorized},
		{"bare token without the scheme is refused", "s3cret", "s3cret", http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			s := &Server{apiToken: tc.token}
			req := httptest.NewRequest(http.MethodGet, "/thread", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			s.guard(next)(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if got := reached; got != (tc.want == http.StatusOK) {
				t.Fatalf("handler reached = %v, want %v", got, tc.want == http.StatusOK)
			}
		})
	}
}

// editor is a module whose card is a form: it needs a "price" before it may
// run, and an edit replaces the input.
type editor struct{}

func (editor) Name() string                { return "shop" }
func (editor) Start(context.Context) error { return nil }
func (editor) Stop(context.Context) error  { return nil }
func (editor) Specs() []action.Spec        { return []action.Spec{{Kind: "shop.sell"}} }
func (editor) Preview(context.Context, action.Action) (action.Card, error) {
	return action.Card{Title: "sell"}, nil
}
func (editor) Execute(context.Context, action.Action) (action.Result, error) {
	return action.Result{}, nil
}
func (editor) Revise(_ context.Context, a action.Action, edits map[string]string) (action.Action, action.Card, error) {
	if edits["price"] == "" {
		return a, action.Card{}, errors.New("가격을 적어 주세요.")
	}
	raw, _ := json.Marshal(map[string]string{"price": edits["price"]})
	return action.Action{Kind: a.Kind, Input: raw}, action.Card{Title: "sell", Fields: []action.Field{{Key: "price"}}}, nil
}

// Approving a form card goes through the module: a missing value stops the
// approval, and an edited value is what the proposal then carries.
func TestDecisionWithEdits(t *testing.T) {
	props := proposal.NewStore(bus.New())
	reg := module.NewRegistry()
	reg.Add(editor{})
	s := &Server{proposals: props, modules: reg, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p, _ := props.Open(proposal.Proposal{
		Action: action.Action{Kind: "shop.sell", Input: json.RawMessage(`{}`)},
		Card:   action.Card{Title: "sell", Fields: []action.Field{{Key: "price"}}},
	})

	decide := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/proposals/"+p.ID+"/decision", strings.NewReader(body))
		req.SetPathValue("id", p.ID)
		rec := httptest.NewRecorder()
		s.postDecision(rec, req)
		return rec.Code
	}

	if code := decide(`{"decision":"approved"}`); code != http.StatusBadRequest {
		t.Fatalf("approval without a price: %d", code)
	}
	if got, _ := props.Get(p.ID); got.State != proposal.Pending {
		t.Fatalf("state after refused approval: %s", got.State)
	}
	if code := decide(`{"decision":"approved","edits":{"price":"9000"}}`); code != http.StatusNoContent {
		t.Fatalf("approval with a price: %d", code)
	}
	got, _ := props.Get(p.ID)
	if got.State != proposal.Approved || string(got.Action.Input) != `{"price":"9000"}` {
		t.Fatalf("after approval: %s %s", got.State, got.Action.Input)
	}
}
