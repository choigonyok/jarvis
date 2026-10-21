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
