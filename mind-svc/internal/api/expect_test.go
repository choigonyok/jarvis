package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/predict"
)

// What a rule that has come true expects of today is listed with its id; the
// operator's "오늘은 안 시켜" on it comes by the stream, keeps it off what
// the conversation is told - also when it is predicted again - and leaves
// it to be scored by what happens.
func TestAnExpectationTheOperatorDenies(t *testing.T) {
	s, h, m := commandServer(t)
	ctx := context.Background()
	st, now := m.Opts.Store, m.Opts.Now()
	_ = st.PutNote(ctx, model.Note{ID: "daily", Text: "배달은 주로 토요일", Status: model.Hypothesis, Version: 1, Confidence: 0.8,
		Rule: &model.Rule{Kind: predict.Daily, Key: predict.Delivery, Rates: []float64{0.8, 0.8, 0.8, 0.8, 0.8, 0.8, 0.8}, Base: 0.4}})
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, m.Opts.Loc)
	pr := model.Prediction{Target: "day:" + predict.Delivery + ":" + day.Format("2006-01-02"), Text: "오늘 배달 주문 80%", Prob: 0.8, Base: 0.4,
		Key: predict.Delivery, From: &day, Due: day.Add(24*time.Hour - time.Minute), Basis: []string{"daily"}}
	saved, err := m.Predict.Put(ctx, pr, predict.Delivery)
	if err != nil {
		t.Fatal(err)
	}
	list := func() []mind.Expected {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/expectations", nil)
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out struct {
			Expected []mind.Expected `json:"expected"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		return out.Expected
	}
	if got := list(); len(got) != 1 || got[0].ID != saved.ID || got[0].Denied != "" || !strings.Contains(got[0].Text, "80%") {
		t.Fatalf("오늘 기대: %+v", got)
	}
	if err := s.Command(ctx, command("expect.denied", "cmd-e1", "operator", "POST", "/api/expectations/"+saved.ID+"/deny", `{"said":"오늘은 안 시켜 먹어"}`)); err != nil {
		t.Fatal(err)
	}
	if code, out := read(t, h, "tok", "cmd-e1"); code != http.StatusOK || out["status"] != float64(200) {
		t.Fatalf("%d %v", code, out)
	}
	if got := list(); len(got) != 1 || got[0].Denied != "오늘은 안 시켜 먹어" {
		t.Fatalf("접은 기대: %+v", got)
	}
	if _, err := m.Predict.Put(ctx, pr, predict.Delivery); err != nil {
		t.Fatal(err)
	}
	if got := list(); got[0].Denied == "" {
		t.Fatalf("다시 예측해도 접힌 그대로: %+v", got)
	}
	// Scored by what happens all the same.
	o := model.Occurrence{ID: "s1", Kind: "purchase", At: now.Add(time.Hour), Title: "주식회사 우아한형제", Class: "식비", Amount: 20000}
	_ = st.PutOccurrence(ctx, o)
	if met, err := m.Predict.Match(ctx, o); err != nil || len(met) != 1 {
		t.Fatalf("접어도 일어나면 맞힘: %v %v", met, err)
	}
	_ = s.Command(ctx, command("expect.denied", "cmd-e2", "operator", "POST", "/api/expectations/pred_none/deny", `{}`))
	if _, out := read(t, h, "tok", "cmd-e2"); out["status"] != float64(http.StatusNotFound) {
		t.Fatalf("없는 예측: %v", out)
	}
}
