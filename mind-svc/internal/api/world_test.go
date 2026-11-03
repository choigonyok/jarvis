package api

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

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// The conversation reads the loops still open, the plans around now with what
// each rests on, and what mind asked lately with why - the questions no tool
// answered ("지수한테 뭐 보내주기로 했지?", "왜 그 카드 올렸어?").
func TestTheWorldForTheConversation(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Date(2026, 11, 3, 20, 0, 0, 0, seoul)
	st := store.NewMemory()
	m := mind.New(mind.Options{
		Store: st, Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(),
		Notifier: &act.MemoryNotifier{}, Loc: seoul, Now: func() time.Time { return now }, Logger: quiet, Policy: gate.DefaultPolicy(), Mode: "stack",
	})
	srv := httptest.NewServer(New(m, "secret", "", false, nil, quiet).Handler())
	defer srv.Close()
	get := func(path string, into any) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if into != nil {
			_ = json.NewDecoder(resp.Body).Decode(into)
		}
		return resp.StatusCode
	}
	ctx := context.Background()
	said := func(q string) world.Meta {
		return world.Meta{Actor: "interpreter", Evidence: []model.Evidence{{Ref: "kakao:1", Quote: q}}, Confidence: 0.9}
	}
	plan, err := m.World.Create(ctx, model.KindPlan, "room-1", model.State{Title: "지수와 저녁", Status: model.Confirmed, Date: "2026-11-07", Start: "19:00", People: []string{"지수"}}, said("토요일 7시에 보자"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.World.Create(ctx, model.KindPlan, "room-1", model.State{Title: "렌터카 대여", Status: model.Confirmed, Date: "2026-11-08", Declined: "대여는 일정이 아니야"}, said("차 빌리자")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.World.Create(ctx, model.KindPlan, "room-1", model.State{Title: "작년 일", Status: model.Done, Date: "2025-11-01"}, said("작년")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.World.Create(ctx, model.KindLoop, "room-1", model.State{Title: "지수에게 숙소 링크 보내기", Owner: "me", Status: model.Open, Due: "2026-11-05", PlanID: plan.ID}, said("링크 보내줄게")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.World.Create(ctx, model.KindLoop, "room-1", model.State{Title: "책 돌려받기", Owner: "them", Status: model.Open}, said("다음에 줄게")); err != nil {
		t.Fatal(err)
	}
	if err := st.PutIntent(ctx, model.Intent{ID: "in-1", Skill: "schedule", Kind: "calendar.create", Title: "11/8 렌터카 대여 넣을까요?",
		Reason: "카톡에서 확정", Why: "확신도 66%라 저녁 요약으로", Decision: model.Digest, Status: model.Rejected, Error: "대여는 일정이 아니야",
		Card: true, Evidence: []model.Evidence{{Quote: "차 빌리자"}}, CreatedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutIntent(ctx, model.Intent{ID: "in-old", Skill: "schedule", Title: "오래된 것", CreatedAt: now.Add(-20 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}

	var loops struct{ Loops []mind.LoopView }
	if code := get("/api/loops", &loops); code != http.StatusOK || len(loops.Loops) != 2 {
		t.Fatalf("loops: %d %+v", code, loops)
	}
	first := loops.Loops[0]
	if first.Title != "지수에게 숙소 링크 보내기" || first.Plan != "지수와 저녁" || first.Said != "링크 보내줄게" || first.Owner != "me" {
		t.Fatalf("기한이 가까운 것부터, 일정과 근거와 함께: %+v", first)
	}
	var plans struct{ Plans []mind.PlanView }
	if code := get("/api/plans", &plans); code != http.StatusOK || len(plans.Plans) != 2 {
		t.Fatalf("plans (어제부터 두 달): %d %+v", code, plans)
	}
	if p := plans.Plans[1]; p.Title != "렌터카 대여" || p.Declined != "대여는 일정이 아니야" || p.Calendar {
		t.Fatalf("반려한 일정은 반려 이유와 함께: %+v", p)
	}
	if code := get("/api/plans?from=2025-10-01&to=2025-11-30", &plans); code != http.StatusOK || len(plans.Plans) != 1 {
		t.Fatalf("기간: %d %+v", code, plans)
	}
	if code := get("/api/plans?from=11-01", nil); code != http.StatusBadRequest {
		t.Fatalf("날짜 모양: %d", code)
	}
	var asked struct{ Asked []mind.AskedView }
	if code := get("/api/asked", &asked); code != http.StatusOK || len(asked.Asked) != 1 {
		t.Fatalf("asked (7일): %d %+v", code, asked)
	}
	if a := asked.Asked[0]; a.Answer != "대여는 일정이 아니야" || a.Why == "" || len(a.Evidence) != 1 {
		t.Fatalf("왜 물었고 어떻게 답했는지: %+v", a)
	}
}

// A rule the operator gave stands until taken back - on the notes' own door -
// and comes again when said again; the situation carries the rules standing,
// and the operator hears of a new one.
func TestRulesStandUntilTakenBack(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := &act.MemoryNotifier{}
	m := mind.New(mind.Options{
		Store: store.NewMemory(), Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(),
		Notifier: notifier, Loc: seoul, Now: time.Now, Logger: quiet, Policy: gate.DefaultPolicy(), Mode: "stack",
	})
	srv := httptest.NewServer(New(m, "secret", "", false, nil, quiet).Handler())
	defer srv.Close()
	call := func(method, path, body string, into any) int {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if into != nil {
			_ = json.NewDecoder(resp.Body).Decode(into)
		}
		return resp.StatusCode
	}
	var set struct {
		Rule model.Note `json:"rule"`
		New  bool       `json:"new"`
	}
	if code := call(http.MethodPost, "/api/rules", `{"text":"렌터카  대여는 일정에 넣지 않는다","said":"대여는 일정 아니야"}`, &set); code != http.StatusOK || !set.New || set.Rule.Scope != model.RuleScope || set.Rule.Status != model.Confirmed {
		t.Fatalf("남겨야 합니다: %d %+v", code, set)
	}
	if set.Rule.Text != "렌터카 대여는 일정에 넣지 않는다" || !strings.Contains(set.Rule.Reason, "대여는 일정 아니야") {
		t.Fatalf("말한 그대로, 말한 이유와 함께: %+v", set.Rule)
	}
	if code := call(http.MethodPost, "/api/rules", `{"text":"렌터카 대여는 일정에 넣지 않는다"}`, &set); code != http.StatusOK || set.New {
		t.Fatalf("같은 규칙은 하나입니다: %d %+v", code, set)
	}
	if code := call(http.MethodPost, "/api/rules", `{"text":"응"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("너무 짧은 규칙: %d", code)
	}
	var now struct{ Rules []string }
	if code := call(http.MethodGet, "/now", "", &now); code != http.StatusOK || len(now.Rules) != 1 {
		t.Fatalf("지금 상황에 규칙이 함께: %d %+v", code, now)
	}
	if n := notifier.All(); len(n) != 1 || n[0].Kind != "memory.rule" || !strings.Contains(n[0].Body, "렌터카 대여") {
		t.Fatalf("새 규칙은 알립니다: %+v", n)
	}
	id := set.Rule.ID
	if code := call(http.MethodPost, "/api/notes/"+id, `{"status":"rejected","reason":"이제 넣어도 돼"}`, nil); code != http.StatusOK {
		t.Fatalf("노트의 문으로 거둡니다: %d", code)
	}
	var rules struct{ Rules []struct{ ID string } }
	if code := call(http.MethodGet, "/api/rules", "", &rules); code != http.StatusOK || len(rules.Rules) != 0 {
		t.Fatalf("거둔 규칙은 없습니다: %d %+v", code, rules)
	}
	if code := call(http.MethodGet, "/now", "", &now); code != http.StatusOK || now.Rules == nil || len(now.Rules) != 0 {
		t.Fatalf("규칙이 없으면 빈 목록: %d %+v", code, now)
	}
	if code := call(http.MethodPost, "/api/rules", `{"text":"렌터카 대여는 일정에 넣지 않는다"}`, &set); code != http.StatusOK || !set.New || set.Rule.Version != 3 {
		t.Fatalf("다시 말하면 다시 섭니다: %d %+v", code, set)
	}
}
