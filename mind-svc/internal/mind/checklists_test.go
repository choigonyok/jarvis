package mind

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/checklist"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
)

// A reminder that carries a list is said with it: the push says what is
// left, and the notice asks for a card in the conversation, naming the list
// for the card to show as it stands (2026-10-21: "집 가서 할 일" reached the
// phone only). The conversation's situation names the open lists.
func TestReminderShowsItsChecklist(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	f := newMomentsFixture(t, time.Date(2026, 10, 21, 21, 0, 0, 0, seoul), nil)
	f.m.Lists = &checklist.Service{St: checklist.NewMemory(), Now: f.clk.Now}
	ctx := context.Background()
	l, err := f.m.Lists.Make(ctx, checklist.New{Title: "집에 가서 할 일", Items: []string{"빨래", "택배 뜯기", "장갑 챙기기"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Lists.Set(ctx, l.ID, "i3", true, checklist.ByCard); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Moments.Set(ctx, moments.Reminder{Text: "집에 왔어요 - 할 일 확인", Place: "home", Move: moments.Arrive, Checklist: l.ID}); err != nil {
		t.Fatal(err)
	}

	f.clk.Pin(f.at(21, 30))
	f.move(t, LocationEnter, "집", "home")
	all := f.notice.All()
	if len(all) != 1 {
		t.Fatalf("알림 %d개", len(all))
	}
	n := all[0]
	if n.Kind != "remind.asked" || !n.Card || n.Data["checklist"] != l.ID || n.Data["by"] != moments.ByArrive {
		t.Fatalf("대화 카드로도, 목록과 함께: %+v", n)
	}
	if !strings.Contains(n.Body, "남은 것: 빨래, 택배 뜯기") || strings.Contains(n.Body, "장갑") || !strings.Contains(n.Body, "집에 도착하셔서") {
		t.Fatalf("푸시에 남은 것만: %q", n.Body)
	}

	sit, err := f.m.Situation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sit.Text, "체크리스트") || !strings.Contains(sit.Text, "["+l.ID+"] 집에 가서 할 일 1/3") {
		t.Fatalf("지금 상황에 열린 목록이 없습니다:\n%s", sit.Text)
	}
}

// What travels to the agent: a notice that asks for a card carries that and
// its data in the event; one that does not keeps the empty data it had.
func TestNoticeCarriesItsCard(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	log := obs.NewMemoryLog(quiet)
	n := noticeLog{log}
	ctx := context.Background()
	if err := n.AppendNotice(ctx, "remind.asked", act.Notice{Title: "장갑", Tier: "now", Key: "remind:a", Card: true,
		Data: map[string]any{"reminder": "rem_a", "checklist": "chk_a"}}); err != nil {
		t.Fatal(err)
	}
	if err := n.AppendNotice(ctx, "mind.notice", act.Notice{Title: "오늘 일정 2개", Tier: "now", Key: "mind:b"}); err != nil {
		t.Fatal(err)
	}
	got, _ := log.Read(ctx, 0, 10)
	if len(got) != 2 {
		t.Fatalf("관측 %d개", len(got))
	}
	var d map[string]any
	_ = json.Unmarshal(got[0].Data, &d)
	if d["card"] != true || d["checklist"] != "chk_a" || d["reminder"] != "rem_a" {
		t.Fatalf("카드 알림의 데이터: %s", got[0].Data)
	}
	if string(got[1].Data) != "{}" {
		t.Fatalf("카드 없는 알림은 그대로: %s", got[1].Data)
	}
}

// mind can put a list in front of the operator by itself: the agent is
// asked, through the log, to show it now.
func TestShowChecklistAsksTheAgent(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	f := newMomentsFixture(t, time.Date(2026, 10, 23, 19, 0, 0, 0, seoul), nil)
	f.m.Lists = &checklist.Service{St: checklist.NewMemory(), Now: f.clk.Now}
	ctx := context.Background()
	l, _ := f.m.Lists.Make(ctx, checklist.New{Title: "캠핑 준비물", Items: []string{"장갑"}})
	if _, err := f.m.ShowChecklist(ctx, l.ID, "내일 캠핑이에요"); err != nil {
		t.Fatal(err)
	}
	got, _ := f.m.Opts.Log.(*obs.MemoryLog).Read(ctx, 0, 10)
	if len(got) == 0 || got[len(got)-1].Type != ChecklistShown || !strings.Contains(string(got[len(got)-1].Data), l.ID) {
		t.Fatalf("보여 달라는 이벤트: %+v", got)
	}
	if _, err := f.m.ShowChecklist(ctx, "chk_none", ""); err == nil {
		t.Fatal("없는 목록을 보여 달라고 했습니다")
	}
}

// A list for a day is brought up by mind itself the evening before, at
// home: what is left, as a card - nothing at all when nothing is left. A
// list made when that evening has begun is in front of the operator already;
// one whose day is taken away has no moment.
func TestDatedListComesUpTheEveningBefore(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	f := newMomentsFixture(t, time.Date(2026, 10, 22, 10, 0, 0, 0, seoul), nil)
	f.m.Lists = &checklist.Service{St: checklist.NewMemory(), Now: f.clk.Now}
	ctx := context.Background()
	camp, err := f.m.MakeList(ctx, checklist.New{Title: "캠핑 준비물", Date: "2026-10-24", Items: []string{"장갑", "베개", "랜턴"}})
	if err != nil {
		t.Fatal(err)
	}
	f.m.Lists.Set(ctx, camp.ID, "i3", true, checklist.ByCard)
	done, _ := f.m.MakeList(ctx, checklist.New{Title: "장보기", Date: "2026-10-24", Items: []string{"물"}})
	f.m.Lists.Set(ctx, done.ID, "i1", true, checklist.ByCard)

	rs, _ := f.m.Moments.List(ctx, false)
	if len(rs) != 2 {
		t.Fatalf("목록마다 전날 저녁 알림 하나: %+v", rs)
	}
	for _, r := range rs {
		if r.Source != moments.Lists || r.Place != "home" || r.After.In(seoul).Format("01-02 15:04") != "10-23 18:00" || r.At.In(seoul).Format("15:04") != "21:30" {
			t.Fatalf("전날 18시부터 집에서, 늦어도 21:30: %+v", r)
		}
	}
	if sit, _ := f.m.Situation(ctx); !strings.Contains(sit.Text, "("+"10/24 용)") || !strings.Contains(sit.Text, "체크리스트 "+camp.ID+" 의 남은 것") {
		t.Fatalf("지금 상황에 목록의 날과 mind 가 잡은 때가 없습니다:\n%s", sit.Text)
	}

	f.clk.Pin(time.Date(2026, 10, 23, 19, 10, 0, 0, seoul))
	f.move(t, LocationEnter, "집", "home")
	all := f.notice.All()
	if len(all) != 1 {
		t.Fatalf("남은 것이 있는 목록만 말해야 합니다: %+v", all)
	}
	n := all[0]
	if n.Kind != "remind.list" || !n.Card || n.Data["checklist"] != camp.ID || n.Title != "내일 캠핑 준비물" || !strings.Contains(n.Body, "남은 것: 장갑, 베개") {
		t.Fatalf("전날 저녁의 알림: %+v", n)
	}
	rs, _ = f.m.Moments.List(ctx, true)
	for _, r := range rs {
		if r.Checklist == done.ID && !strings.Contains(r.Result, "다 해서") {
			t.Fatalf("다 한 목록은 말하지 않고 그렇게 남깁니다: %+v", r)
		}
	}

	// Made that evening: no moment. Its day taken away: none either.
	late, _ := f.m.MakeList(ctx, checklist.New{Title: "늦게 만든 목록", Date: "2026-10-24", Items: []string{"하나"}})
	later, _ := f.m.MakeList(ctx, checklist.New{Title: "다음 주 목록", Date: "2026-10-30", Items: []string{"둘"}})
	none := ""
	if _, err := f.m.ChangeList(ctx, later.ID, checklist.Change{Date: &none}); err != nil {
		t.Fatal(err)
	}
	waiting, _ := f.m.Moments.List(ctx, false)
	for _, r := range waiting {
		if r.Checklist == late.ID || r.Checklist == later.ID {
			t.Fatalf("잡지 않았어야 할 알림: %+v", r)
		}
	}
}

// The night closes the lists done with: every item done a while ago, or a
// day past. One ticked off this evening, and one still in use, stay open.
func TestNightClosesListsDoneWith(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	f := newMomentsFixture(t, time.Date(2026, 10, 24, 9, 0, 0, 0, seoul), nil)
	f.m.Lists = &checklist.Service{St: checklist.NewMemory(), Now: f.clk.Now}
	ctx := context.Background()
	old, _ := f.m.MakeList(ctx, checklist.New{Title: "다 한 목록", Items: []string{"하나"}})
	f.m.Lists.Set(ctx, old.ID, "i1", true, checklist.ByCard)
	past, _ := f.m.MakeList(ctx, checklist.New{Title: "어제 캠핑", Date: "2026-10-24", Items: []string{"장갑"}})
	open, _ := f.m.MakeList(ctx, checklist.New{Title: "집에서 할 일", Items: []string{"빨래"}})

	f.clk.Pin(time.Date(2026, 10, 25, 3, 0, 0, 0, seoul))
	fresh, _ := f.m.MakeList(ctx, checklist.New{Title: "방금 끝낸 목록", Items: []string{"둘"}})
	f.m.Lists.Set(ctx, fresh.ID, "i1", true, checklist.ByCard)
	if err := f.m.closeLists(ctx); err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	all, _ := f.m.Lists.All(ctx, true)
	for _, l := range all {
		status[l.ID] = l.Status
	}
	want := map[string]string{old.ID: checklist.Archived, past.ID: checklist.Archived, open.ID: checklist.Active, fresh.ID: checklist.Active}
	for id, s := range want {
		if status[id] != s {
			t.Errorf("%s: %s, want %s", id, status[id], s)
		}
	}
}
