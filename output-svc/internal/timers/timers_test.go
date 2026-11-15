package timers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/output-svc/internal/push"
	"github.com/choigonyok/jarvis/output-svc/internal/store"
)

// devices is the way out as the tests see it: whose each endpoint is, and
// what went to each.
type devices struct {
	whose map[string]string
	sent  []delivery
}

type delivery struct {
	audience, endpoint string
	push.Payload
}

func (d *devices) Owns(_ context.Context, audience, endpoint string) (bool, error) {
	return d.whose[endpoint] == audience, nil
}

func (d *devices) SendTo(_ context.Context, audience, endpoint string, p push.Payload) (bool, error) {
	if d.whose[endpoint] != audience {
		return false, nil
	}
	d.sent = append(d.sent, delivery{audience, endpoint, p})
	return true, nil
}

// clock is time that moves only when a test moves it, and what is set to go
// off on it.
type clock struct {
	now    time.Time
	alarms []*alarm
}

type alarm struct {
	at   time.Time
	f    func()
	done bool // went off, or was stopped
}

func (c *clock) after(d time.Duration, f func()) func() bool {
	a := &alarm{at: c.now.Add(d), f: f}
	c.alarms = append(c.alarms, a)
	return func() bool {
		was := !a.done
		a.done = true
		return was
	}
}

// pass moves the clock on, and what came due goes off.
func (c *clock) pass(d time.Duration) {
	c.now = c.now.Add(d)
	for _, a := range c.alarms {
		if !a.done && !a.at.After(c.now) {
			a.done = true
			a.f()
		}
	}
}

const (
	phone   = "https://web.push.apple.com/phone"
	laptop  = "https://fcm.googleapis.com/laptop"
	partner = "https://web.push.apple.com/partner"
)

func setup() (*Timers, *devices, *clock) {
	d := &devices{whose: map[string]string{phone: store.Owner, laptop: store.Owner, partner: store.Guest}}
	c := &clock{now: time.Date(2026, 11, 15, 19, 0, 0, 0, time.UTC)}
	t := New(d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.now = func() time.Time { return c.now }
	t.after = c.after
	return t, d, c
}

// rest is the operator's phone asking for the end of a rest.
func rest(at time.Time) Ask {
	return Ask{Audience: store.Owner, Endpoint: phone, At: at, Tag: "rest", Title: "휴식이 끝났어요", Body: "다음 세트를 할 시간이에요.", URL: "/workout"}
}

// A rest's end goes to the phone at its time, once: urgent, and dropped
// rather than told late.
func TestItGoesAtItsTime(t *testing.T) {
	tm, d, c := setup()
	got, err := tm.Schedule(context.Background(), rest(c.now.Add(90*time.Second)))
	if err != nil || got.ID == "" || !got.At.Equal(c.now.Add(90*time.Second)) {
		t.Fatalf("%+v %v", got, err)
	}
	c.pass(89 * time.Second)
	if len(d.sent) != 0 {
		t.Fatalf("일찍 갔습니다: %+v", d.sent)
	}
	c.pass(time.Second)
	if len(d.sent) != 1 {
		t.Fatalf("제 시각에 한 번 가야 합니다: %+v", d.sent)
	}
	p := d.sent[0]
	if p.audience != store.Owner || p.endpoint != phone || p.Tag != "rest" || p.Title != "휴식이 끝났어요" ||
		p.Body != "다음 세트를 할 시간이에요." || p.URL != "/workout" {
		t.Fatalf("%+v", p)
	}
	if !p.Urgent || p.TTL <= 0 || p.TTL > 5*time.Minute {
		t.Fatalf("급하고 짧게 살아야 합니다: urgent=%v ttl=%v", p.Urgent, p.TTL)
	}
	c.pass(time.Hour)
	if len(d.sent) != 1 {
		t.Fatalf("두 번 갔습니다: %+v", d.sent)
	}
	if tm.Cancel(store.Owner, got.ID) {
		t.Fatal("이미 간 것이 취소됐습니다")
	}
}

// A nudged rest is one push, at its new end. Another tag on the same
// device, or the same tag on another device, is a push of its own.
func TestTheSameDeviceAndTagReplaces(t *testing.T) {
	tm, d, c := setup()
	ctx := context.Background()
	first, err := tm.Schedule(ctx, rest(c.now.Add(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Schedule(ctx, rest(c.now.Add(75*time.Second))); err != nil {
		t.Fatal(err)
	}
	plank := rest(c.now.Add(60 * time.Second))
	plank.Tag = "plank"
	desk := rest(c.now.Add(60 * time.Second))
	desk.Endpoint = laptop
	for _, a := range []Ask{plank, desk} {
		if _, err := tm.Schedule(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	c.pass(60 * time.Second)
	if len(d.sent) != 2 {
		t.Fatalf("다른 tag 와 다른 기기의 것만 가야 합니다: %+v", d.sent)
	}
	for _, s := range d.sent {
		if s.endpoint == phone && s.Tag == "rest" {
			t.Fatal("바뀌기 전의 휴식 끝이 갔습니다")
		}
	}
	c.pass(15 * time.Second)
	if len(d.sent) != 3 || d.sent[2].endpoint != phone || d.sent[2].Tag != "rest" {
		t.Fatalf("바뀐 시각에 가야 합니다: %+v", d.sent)
	}
	if tm.Cancel(store.Owner, first.ID) {
		t.Fatal("바뀐 앞의 것이 남아 있었습니다")
	}
}

// A rest ended early sends nothing; only its own person takes it back.
func TestCancel(t *testing.T) {
	tm, d, c := setup()
	ctx := context.Background()
	got, err := tm.Schedule(ctx, rest(c.now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if tm.Cancel(store.Guest, got.ID) {
		t.Fatal("손님이 운영자의 예약을 취소했습니다")
	}
	if !tm.Cancel(store.Owner, got.ID) {
		t.Fatal("취소하지 못했습니다")
	}
	if tm.Cancel(store.Owner, got.ID) {
		t.Fatal("두 번 취소됐습니다")
	}
	c.pass(time.Hour)
	if len(d.sent) != 0 {
		t.Fatalf("취소한 것이 갔습니다: %+v", d.sent)
	}
	// Taken back just as it went off: the alarm ran, the push does not go.
	again, err := tm.Schedule(ctx, rest(c.now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	tm.Cancel(store.Owner, again.ID)
	tm.fire(again.ID)
	if len(d.sent) != 0 {
		t.Fatalf("울리던 중에 취소한 것이 갔습니다: %+v", d.sent)
	}
}

// How far ahead, how far behind, and how many for one device.
func TestCaps(t *testing.T) {
	tm, d, c := setup()
	ctx := context.Background()
	if _, err := tm.Schedule(ctx, rest(c.now.Add(MaxAhead+time.Second))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("30분 넘게 뒤가 예약됐습니다: %v", err)
	}
	if _, err := tm.Schedule(ctx, rest(c.now.Add(MaxAhead))); err != nil {
		t.Fatalf("30분 뒤는 됩니다: %v", err)
	}
	if _, err := tm.Schedule(ctx, rest(c.now.Add(-time.Minute))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("지난 시각이 예약됐습니다: %v", err)
	}
	// A phone's clock a few seconds off: taken, and it goes now.
	if _, err := tm.Schedule(ctx, rest(c.now.Add(-3*time.Second))); err != nil {
		t.Fatal(err)
	}
	c.pass(0)
	if len(d.sent) != 1 {
		t.Fatalf("몇 초 지난 것은 바로 가야 합니다: %+v", d.sent)
	}

	for _, tag := range []string{"a", "b", "c", "d"} {
		a := rest(c.now.Add(time.Minute))
		a.Tag = tag
		if _, err := tm.Schedule(ctx, a); err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
	}
	e := rest(c.now.Add(time.Minute))
	e.Tag = "e"
	if _, err := tm.Schedule(ctx, e); !errors.Is(err, ErrTooMany) {
		t.Fatalf("한 기기에 %d개를 넘게 받았습니다: %v", PerDevice, err)
	}
	// Replacing one of them is not one more; another device has its own room.
	b := rest(c.now.Add(2 * time.Minute))
	b.Tag = "b"
	e.Endpoint = laptop
	for _, a := range []Ask{b, e} {
		if _, err := tm.Schedule(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
}

// Never someone else's device: not when it is asked for, nor when it goes
// off after the device was signed in as the other person.
func TestOnlyTheirOwnDevice(t *testing.T) {
	tm, d, c := setup()
	ctx := context.Background()
	theirs := rest(c.now.Add(time.Minute))
	theirs.Audience = store.Guest // the guest, naming the operator's phone
	unknown := rest(c.now.Add(time.Minute))
	unknown.Endpoint = "https://web.push.apple.com/nobody"
	toGuest := rest(c.now.Add(time.Minute))
	toGuest.Endpoint = partner // the operator, naming the guest's phone
	for _, a := range []Ask{theirs, unknown, toGuest} {
		if _, err := tm.Schedule(ctx, a); !errors.Is(err, ErrNotYours) {
			t.Fatalf("%s 의 요청으로 %s 에 예약됐습니다: %v", a.Audience, a.Endpoint, err)
		}
	}
	mine := rest(c.now.Add(time.Minute))
	mine.Audience, mine.Endpoint = store.Guest, partner
	if _, err := tm.Schedule(ctx, mine); err != nil {
		t.Fatalf("손님은 자기 폰에 예약합니다: %v", err)
	}
	if _, err := tm.Schedule(ctx, rest(c.now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	d.whose[phone] = store.Guest // signed in as the guest since
	c.pass(time.Minute)
	if len(d.sent) != 1 || d.sent[0].endpoint != partner || d.sent[0].audience != store.Guest {
		t.Fatalf("손님 폰에 손님 것만 가야 합니다: %+v", d.sent)
	}
}

// What the console sends is made fit for a notification on the device, or refused.
func TestTheAskIsChecked(t *testing.T) {
	tm, d, c := setup()
	ctx := context.Background()
	for i, bad := range []func(*Ask){
		func(a *Ask) { a.Endpoint = "" },
		func(a *Ask) { a.Tag = "" },
		func(a *Ask) { a.Tag = "approval.pending" }, // a notification's kind: not a timer's to replace
		func(a *Ask) { a.Tag = "Rest" },
		func(a *Ask) { a.Title = "  " },
		func(a *Ask) { a.At = time.Time{} },
	} {
		a := rest(c.now.Add(time.Minute))
		bad(&a)
		if _, err := tm.Schedule(ctx, a); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%d: %+v: %v", i, a, err)
		}
	}
	// A tap stays in the console.
	for _, url := range []string{"https://evil.example", "//evil.example", `/\evil.example`, "/\t/evil.example", ""} {
		a := rest(c.now)
		a.URL = url
		if _, err := tm.Schedule(ctx, a); err != nil {
			t.Fatal(err)
		}
		c.pass(0)
		if got := d.sent[len(d.sent)-1].URL; got != "/" {
			t.Fatalf("%q 가 %q 로 남았습니다", url, got)
		}
	}
	long := rest(c.now)
	long.Title = strings.Repeat("휴", 200)
	if _, err := tm.Schedule(ctx, long); err != nil {
		t.Fatal(err)
	}
	c.pass(0)
	if got := []rune(d.sent[len(d.sent)-1].Title); len(got) != 120 {
		t.Fatalf("제목이 %d자입니다", len(got))
	}
}
