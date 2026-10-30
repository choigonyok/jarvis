package act

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type bookCall struct{ method, path, body, auth string }

func fakeBook(t *testing.T, status int) (Spending, *[]bookCall) {
	t.Helper()
	var calls []bookCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, bookCall{r.Method, r.URL.Path, string(b), r.Header.Get("Authorization")})
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return Spending{URL: srv.URL, Token: "tok"}, &calls
}

func raw(p CategoryPayload) json.RawMessage {
	b, _ := json.Marshal(p)
	return b
}

func TestFilingACharge(t *testing.T) {
	s, calls := fakeBook(t, 200)
	ctx := context.Background()
	res, undo, err := s.Execute(ctx, KindCategory, raw(CategoryPayload{Kind: "tx", ID: 12, Merchant: "상호", Proposed: "식비",
		Answers: map[string]string{"category": "식비", "remember": RememberYes}}))
	if err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if c.method != http.MethodPatch || c.path != "/transactions/12" || c.auth != "Bearer tok" || !strings.Contains(c.body, `"remember":true`) {
		t.Fatalf("%+v", c)
	}
	if !strings.Contains(string(res), `"corrected":false`) || len(undo) == 0 {
		t.Fatalf("%s", res)
	}
}

// Changing the answer on the form is a correction: what was said was wrong.
func TestACorrectionIsSaid(t *testing.T) {
	s, calls := fakeBook(t, 200)
	res, _, err := s.Execute(context.Background(), KindCategory, raw(CategoryPayload{Kind: "tx", ID: 1, Proposed: "식비",
		Answers: map[string]string{"category": "교통", "remember": RememberNo}}))
	if err != nil || !strings.Contains(string(res), `"corrected":true`) || !strings.Contains((*calls)[0].body, `"remember":false`) {
		t.Fatalf("%s %v %+v", res, err, *calls)
	}
}

// Asked with the form empty, picking anything is no correction of nothing.
func TestABlankFormCorrectsNothing(t *testing.T) {
	s, _ := fakeBook(t, 200)
	res, _, err := s.Execute(context.Background(), KindCategory, raw(CategoryPayload{Kind: "tx", ID: 1, Answers: map[string]string{"category": "교통"}}))
	if err != nil || !strings.Contains(string(res), `"corrected":false`) {
		t.Fatalf("%s %v", res, err)
	}
}

// Done without asking, it files what was proposed.
func TestDoneWithoutAskingUsesWhatWasProposed(t *testing.T) {
	s, calls := fakeBook(t, 200)
	if _, _, err := s.Execute(context.Background(), KindCategory, raw(CategoryPayload{Kind: "tx", ID: 1, Proposed: "식비"})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*calls)[0].body, `"category":"식비"`) || !strings.Contains((*calls)[0].body, `"remember":true`) {
		t.Fatalf("%+v", *calls)
	}
}

func TestNothingToFile(t *testing.T) {
	s, _ := fakeBook(t, 200)
	if _, _, err := s.Execute(context.Background(), KindCategory, raw(CategoryPayload{Kind: "tx", ID: 1})); err == nil {
		t.Fatal("고른 분류가 없습니다")
	}
}

func TestAProductAndUndoAndSettle(t *testing.T) {
	s, calls := fakeBook(t, 200)
	ctx := context.Background()
	item := raw(CategoryPayload{Kind: "item", ID: 5, Answers: map[string]string{"category": "식비"}})
	if _, _, err := s.Execute(ctx, KindCategory, item); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].path != "/items/5" {
		t.Fatalf("%+v", (*calls)[0])
	}
	if _, err := s.Undo(ctx, KindCategory, item); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*calls)[1].body, `"category":"기타"`) {
		t.Fatalf("%+v", (*calls)[1])
	}
	if err := s.Settle(ctx, raw(CategoryPayload{Kind: "tx", ID: 9})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*calls)[2].body, `"remember":false`) || (*calls)[2].path != "/transactions/9" {
		t.Fatalf("%+v", (*calls)[2])
	}
}

func TestABookThatRefuses(t *testing.T) {
	s, _ := fakeBook(t, 500)
	if _, _, err := s.Execute(context.Background(), KindCategory, raw(CategoryPayload{Kind: "tx", ID: 1, Proposed: "식비"})); err == nil {
		t.Fatal("실패를 말해야 합니다")
	}
}
