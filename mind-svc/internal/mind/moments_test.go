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
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/weather"
)

type momentsFixture struct {
	m      *Mind
	st     *store.Memory
	clk    *clock.Clock
	notice *act.MemoryNotifier
	here   []moments.Visit
}

func newMomentsFixture(t *testing.T, at time.Time, sky *weather.Service) *momentsFixture {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := &momentsFixture{st: store.NewMemory(), clk: clock.New(), notice: &act.MemoryNotifier{}}
	f.clk.Pin(at)
	f.m = New(Options{Store: f.st, Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(), Notifier: f.notice,
		Loc: at.Location(), Now: f.clk.Now, Logger: quiet, Interpret: interpret.Config{Owner: "나"}, Policy: gate.DefaultPolicy(), Mode: "eval",
		Reminders: moments.NewMemory(), Here: func(context.Context) ([]moments.Visit, error) { return f.here, nil }, Weather: sky})
	return f
}

func (f *momentsFixture) at(h, m int) time.Time {
	now := f.clk.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
}

func (f *momentsFixture) handle(t *testing.T, o obs.Observation) {
	t.Helper()
	if err := f.m.Handle(context.Background(), o); err != nil {
		t.Fatal(err)
	}
}

func (f *momentsFixture) move(t *testing.T, typ, region, kind string) {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"region": region, "kind": kind})
	f.handle(t, obs.Observation{Source: "context", Type: typ, Subject: region, Data: data, OccurredAt: f.clk.Now()})
}

func (f *momentsFixture) tick(t *testing.T) {
	t.Helper()
	if err := f.m.Moments.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (f *momentsFixture) intents(topic string) []model.Intent {
	ins, _ := f.st.Intents(context.Background(), 1000)
	var out []model.Intent
	for _, in := range ins {
		if strings.HasPrefix(in.Topic, topic) && in.Status != model.Dropped {
			out = append(out, in)
		}
	}
	return out
}

func band(title, body, key string) obs.Observation {
	wording, _ := json.Marshal(map[string]string{"title": title, "body": body})
	return obs.Observation{Source: "assets", Type: AssetsBand, Key: key, Notify: wording}
}

// A portfolio out of band in the morning is brought up on coming home after
// work - one question for every bucket out - and not at once, nor in the
// evening's summary.
func TestBandWaitsForComingHome(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	f := newMomentsFixture(t, time.Date(2026, 10, 20, 9, 30, 0, 0, seoul), nil)
	f.handle(t, band("코인 비중이 범위를 벗어났어요", "지금 30% · 목표 20% (+10%p)", "assets:band:coin:2026-10-20"))
	f.handle(t, band("금 비중이 범위를 벗어났어요", "지금 4% · 목표 10% (-6%p)", "assets:band:gold:2026-10-20"))
	if ins := f.intents("rebalance"); len(ins) != 0 {
		t.Fatalf("아침에 바로 묻지 않아야 합니다: %+v", ins)
	}
	rs, _ := f.m.Moments.List(context.Background(), false)
	if len(rs) != 1 || rs[0].Text != outOfBand || !strings.Contains(rs[0].Body, "코인: 지금 30%") || !strings.Contains(rs[0].Body, "금: 지금 4%") {
		t.Fatalf("버킷 둘이 알림 하나로 기다려야 합니다: %+v", rs)
	}
	if got := rs[0].When(seoul); got != "10/20(화) 17:00부터 집에 있거나 도착할 때 · 늦어도 22:00" {
		t.Fatalf("퇴근 뒤 집에서: %q", got)
	}

	f.clk.Pin(f.at(17, 0))
	f.tick(t) // not home yet
	f.clk.Pin(f.at(18, 40))
	f.move(t, LocationEnter, "회사", "work")
	if ins := f.intents("rebalance"); len(ins) != 0 {
		t.Fatalf("집에 오기 전에 물었습니다: %+v", ins)
	}
	f.move(t, LocationEnter, "집", "home")
	ins := f.intents("rebalance")
	if len(ins) != 1 || ins[0].Tier != "now" || !strings.Contains(ins[0].Body, "코인: 지금 30%") {
		t.Fatalf("집에 오면 지금 알려야 합니다(저녁 요약 아님): %+v", ins)
	}
	rs, _ = f.m.Moments.List(context.Background(), true)
	if len(rs) != 1 || rs[0].Status != moments.Sent || rs[0].SentBy != moments.ByArrive || rs[0].Result != "푸시" {
		t.Fatalf("보낸 것으로 남아야 합니다: %+v", rs)
	}
}

// Home already when the evening starts: then. Never home by 22:00 (or the
// phone never said): then.
func TestBandHomeAlreadyAndTheLatest(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	f := newMomentsFixture(t, time.Date(2026, 10, 20, 19, 30, 0, 0, seoul), nil)
	f.here = []moments.Visit{{Region: "집", Kind: "home"}}
	f.handle(t, band("코인 비중이 범위를 벗어났어요", "지금 30% · 목표 20%", "assets:band:coin:2026-10-20"))
	f.tick(t)
	if ins := f.intents("rebalance"); len(ins) != 1 {
		t.Fatalf("집에 있으면 바로: %+v", ins)
	}

	f = newMomentsFixture(t, time.Date(2026, 10, 20, 12, 0, 0, 0, seoul), nil)
	f.handle(t, band("코인 비중이 범위를 벗어났어요", "지금 30% · 목표 20%", "assets:band:coin:2026-10-20"))
	f.clk.Pin(f.at(21, 59))
	f.tick(t)
	if ins := f.intents("rebalance"); len(ins) != 0 {
		t.Fatalf("22시 전에 말했습니다: %+v", ins)
	}
	f.clk.Pin(f.at(22, 0))
	f.tick(t)
	if ins := f.intents("rebalance"); len(ins) != 1 {
		t.Fatalf("늦어도 22시에는: %+v", ins)
	}

	// Past 22:00: the next evening.
	from, latest := evening(time.Date(2026, 10, 20, 23, 10, 0, 0, seoul), seoul)
	if from != time.Date(2026, 10, 21, 17, 0, 0, 0, seoul) || latest != time.Date(2026, 10, 21, 22, 0, 0, 0, seoul) {
		t.Fatalf("다음 날 저녁: %v %v", from, latest)
	}
}

// What the operator asked for goes out in their words, as remind.asked -
// which output-svc rings even in quiet hours - and the conversation is
// shown it waiting, with its id.
func TestAskedReminder(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	f := newMomentsFixture(t, time.Date(2026, 10, 23, 7, 0, 0, 0, seoul), nil)
	ctx := context.Background()
	at := f.at(7, 30)
	r, err := f.m.Moments.Set(ctx, moments.Reminder{Text: "우산 챙기세요", At: &at})
	if err != nil {
		t.Fatal(err)
	}
	now, err := f.m.Situation(ctx)
	if err != nil || !strings.Contains(now.Text, "맡아 둔 알림") || !strings.Contains(now.Text, r.ID) || !strings.Contains(now.Text, "10/23(금) 07:30") {
		t.Fatalf("지금 상황에 기다리는 알림이 보여야 합니다: %q %v", now.Text, err)
	}
	f.clk.Pin(at)
	f.tick(t)
	ns := f.notice.All()
	if len(ns) != 1 || ns[0].Kind != "remind.asked" || ns[0].Title != r.Text || ns[0].Tier != "now" || !strings.Contains(ns[0].Body, "부탁하신 알림") {
		t.Fatalf("부탁한 말 그대로 remind.asked 로: %+v", ns)
	}
	if now, _ := f.m.Situation(ctx); strings.Contains(now.Text, "맡아 둔 알림") {
		t.Fatalf("보낸 것은 지금 상황에서 빠져야 합니다: %q", now.Text)
	}

	// Down at its time: said when back, and said to be late.
	at = f.at(8, 0)
	if _, err := f.m.Moments.Set(ctx, moments.Reminder{Text: "약 먹기", At: &at}); err != nil {
		t.Fatal(err)
	}
	f.clk.Pin(f.at(8, 40))
	f.tick(t)
	if ns := f.notice.All(); len(ns) != 2 || !strings.Contains(ns[1].Body, "10/23(금) 08:00에 알려 드렸어야 했는데 늦었어요") {
		t.Fatalf("늦었으면 늦었다고: %+v", ns)
	}
}

// Leaving home on a day it will still rain: when, and the umbrella - at the
// door, once a day, as remind.rain. Not on a dry day, not at night, not once
// the rain is over.
func TestRainAtTheDoor(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, seoul) }
	hours := func(d int, wet ...int) []weather.Hour {
		var out []weather.Hour
		for h := range 24 {
			prob := 10
			for _, w := range wet {
				if w == h {
					prob = 80
				}
			}
			out = append(out, weather.Hour{At: day(d).Add(time.Duration(h) * time.Hour), Prob: prob})
		}
		return out
	}
	sky := &weather.Service{Home: weather.StaticHome, Source: weather.Static{
		Days: map[string][]weather.Day{"": {
			{Date: "2026-10-20", Code: 61, Precip: 12, Prob: 80, Min: 11, Max: 18},
			{Date: "2026-10-21", Code: 1, Precip: 0, Prob: 10, Min: 9, Max: 19},
		}},
		Hours: map[string][]weather.Hour{"": append(hours(20, 15, 16, 17), hours(21)...)},
	}}
	f := newMomentsFixture(t, time.Date(2026, 10, 20, 8, 10, 0, 0, seoul), sky)
	f.move(t, LocationLeave, "회사", "work")
	if ins := f.intents("rain.out"); len(ins) != 0 {
		t.Fatalf("집이 아닌 곳을 나설 때는: %+v", ins)
	}
	f.move(t, LocationLeave, "집", "home")
	ins := f.intents("rain.out")
	if len(ins) != 1 || ins[0].Title != "15~18시 비 · 우산 챙기세요" || ins[0].Tier != "now" || ins[0].Moment != "rain" {
		t.Fatalf("집을 나설 때 비 소식: %+v", ins)
	}
	ns := f.notice.All()
	if len(ns) != 1 || ns[0].Kind != "remind.rain" || !strings.Contains(ns[0].Body, "비 80%") {
		t.Fatalf("remind.rain 으로: %+v", ns)
	}
	f.clk.Pin(f.at(12, 0))
	f.move(t, LocationLeave, "집", "home")
	if ins := f.intents("rain.out"); len(ins) != 1 {
		t.Fatalf("하루 한 번만: %+v", ins)
	}

	// Raining now, till night.
	if got, ok := rainAhead(weather.Day{Prob: 80, Precip: 9}, hours(20, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23), f.at(9, 20), seoul); !ok || got != "지금 비 · 밤까지" {
		t.Fatalf("지금 비: %q %v", got, ok)
	}
	// Over before leaving.
	if _, ok := rainAhead(weather.Day{Prob: 80, Precip: 9}, hours(20, 6, 7), f.at(9, 20), seoul); ok {
		t.Fatal("이미 그친 비")
	}

	// A dry day.
	f = newMomentsFixture(t, time.Date(2026, 10, 21, 8, 10, 0, 0, seoul), sky)
	f.move(t, LocationLeave, "집", "home")
	if ins := f.intents("rain.out"); len(ins) != 0 {
		t.Fatalf("맑은 날: %+v", ins)
	}
	// At night, a leaving is more likely the phone drifting.
	f = newMomentsFixture(t, time.Date(2026, 10, 20, 3, 0, 0, 0, seoul), sky)
	f.move(t, LocationLeave, "집", "home")
	if ins := f.intents("rain.out"); len(ins) != 0 {
		t.Fatalf("밤에는: %+v", ins)
	}
}

// Without a reminders store (a replay without one, the old wiring), a band
// is raised at once as before.
func TestBandWithoutMomentsIsRaisedNow(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	m, st, _ := testMind(t, time.Date(2026, 10, 20, 9, 30, 0, 0, seoul))
	if err := m.Handle(context.Background(), band("코인 비중이 범위를 벗어났어요", "지금 30%", "k")); err != nil {
		t.Fatal(err)
	}
	ins, _ := st.Intents(context.Background(), 10)
	if len(ins) != 1 || ins[0].Topic != "rebalance" || ins[0].Tier != "digest" {
		t.Fatalf("예전처럼 바로(저녁 요약): %+v", ins)
	}
}

// The guest's reminders are the guest's: said on the guest's phone's moves
// and the guest's being there, to the guest's devices - never on the
// operator's moves, and never in the operator's conversation or situation.
func TestGuestReminders(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	f := newMomentsFixture(t, time.Date(2026, 10, 24, 17, 0, 0, 0, seoul), nil)
	var guestHere []moments.Visit
	f.m.Moments.GuestHere = func(context.Context) ([]moments.Visit, error) { return guestHere, nil }
	ctx := context.Background()
	guest, err := f.m.Moments.Set(ctx, moments.Reminder{Who: moments.Guest, Text: "학원 가방 챙기기", Place: "home", Move: moments.Arrive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Moments.Set(ctx, moments.Reminder{Text: "우산 말리기", Place: "home", Move: moments.Arrive}); err != nil {
		t.Fatal(err)
	}
	if sit, _ := f.m.Situation(ctx); strings.Contains(sit.Text, "학원 가방") || !strings.Contains(sit.Text, "우산 말리기") {
		t.Fatalf("운영자의 지금 상황에는 운영자의 알림만:\n%s", sit.Text)
	}

	f.clk.Pin(f.at(18, 0))
	f.move(t, LocationEnter, "집", "home")
	if ns := f.notice.All(); len(ns) != 1 || ns[0].Title != "우산 말리기" || ns[0].Audience != "" {
		t.Fatalf("운영자가 집에 오면 운영자의 것만: %+v", ns)
	}
	f.clk.Pin(f.at(18, 30))
	f.move(t, LocationGuestEnter, "집", "home")
	ns := f.notice.All()
	if len(ns) != 2 || ns[1].Title != "학원 가방 챙기기" || ns[1].Audience != "guest" || ns[1].Data["who"] != "guest" {
		t.Fatalf("손님이 집에 오면 손님의 것이 손님에게: %+v", ns)
	}
	if r, _ := f.m.Moments.Get(ctx, guest.ID); r.Status != moments.Sent {
		t.Fatalf("손님 알림은 보냄: %+v", r)
	}

	// Being there already: the guest's own whereabouts count for the
	// guest's, the operator's for the operator's.
	from := f.at(19, 0)
	if _, err := f.m.Moments.Set(ctx, moments.Reminder{Who: moments.Guest, Text: "숙제 사진 보내기", Place: "home", Move: moments.Arrive, After: &from}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Moments.Set(ctx, moments.Reminder{Text: "택배 뜯기", Place: "home", Move: moments.Arrive, After: &from}); err != nil {
		t.Fatal(err)
	}
	guestHere, f.here = []moments.Visit{{Region: "집", Kind: "home"}}, nil
	f.clk.Pin(from)
	f.tick(t)
	if ns := f.notice.All(); len(ns) != 3 || ns[2].Title != "숙제 사진 보내기" || ns[2].Audience != "guest" {
		t.Fatalf("집에 있는 손님에게만: %+v", ns)
	}
	if mine, _ := f.m.Moments.ListOf(ctx, moments.Guest, false); len(mine) != 0 {
		t.Fatalf("손님의 기다리는 알림은 다 보냄: %+v", mine)
	}
	if theirs, _ := f.m.Moments.List(ctx, false); len(theirs) != 1 || theirs[0].Text != "택배 뜯기" {
		t.Fatalf("운영자의 것은 운영자가 집에 와야: %+v", theirs)
	}
}
