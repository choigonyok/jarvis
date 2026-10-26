package notify

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/notify-svc/internal/push"
	"github.com/choigonyok/jarvis/notify-svc/internal/store"
)

type fakeStore struct {
	rows     []store.Notification
	settings map[string][]byte
}

func (f *fakeStore) Insert(_ context.Context, n store.Notification) (store.Notification, bool, error) {
	if n.DedupeKey != nil {
		for _, r := range f.rows {
			if r.DedupeKey != nil && *r.DedupeKey == *n.DedupeKey {
				return store.Notification{}, true, nil
			}
		}
	}
	n.ID = int64(len(f.rows) + 1)
	f.rows = append(f.rows, n)
	return n, false, nil
}

func (f *fakeStore) MarkDelivered(_ context.Context, ids []int64) error {
	now := time.Now()
	for _, id := range ids {
		f.rows[id-1].DeliveredAt = &now
	}
	return nil
}

func (f *fakeStore) Pending(_ context.Context, audience, tier string) ([]store.Notification, error) {
	var out []store.Notification
	for _, r := range f.rows {
		if r.Audience == audience && r.Tier == tier && r.DeliveredAt == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) Settings(_ context.Context, audience string, v any) error {
	if f.settings[audience] == nil {
		return nil
	}
	return json.Unmarshal(f.settings[audience], v)
}

func (f *fakeStore) WriteSettings(_ context.Context, audience string, v any) error {
	if f.settings == nil {
		f.settings = map[string][]byte{}
	}
	f.settings[audience], _ = json.Marshal(v)
	return nil
}

// fakeSender keeps what went out, and to whom (the guest's pushes in guest).
type fakeSender struct{ sent, guest []push.Payload }

func (f *fakeSender) Send(_ context.Context, audience string, p push.Payload) (int, error) {
	if audience == store.Guest {
		f.guest = append(f.guest, p)
		return 1, nil
	}
	f.sent = append(f.sent, p)
	return 1, nil
}

var kst = time.FixedZone("KST", 9*60*60)

func setup(at string) (*Service, *fakeStore, *fakeSender, *time.Time) {
	st, snd := &fakeStore{}, &fakeSender{}
	svc := New(st, snd, kst)
	now, _ := time.ParseInLocation("2006-01-02 15:04", at, kst)
	svc.now = func() time.Time { return now }
	return svc, st, snd, &now
}

func ev(kind, tier, title, key string) Event {
	return Event{Source: "test", Kind: kind, Tier: tier, Title: title, URL: "/assets", Key: key}
}

func TestNowPushesAtOnceAndLogNever(t *testing.T) {
	svc, st, snd, _ := setup("2026-10-10 14:00")
	ctx := context.Background()
	if _, _, err := svc.Accept(ctx, ev("approval.pending", "now", "결재 대기", "")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Accept(ctx, ev("spending.enriched", "log", "품목 분류 완료", "")); err != nil {
		t.Fatal(err)
	}
	if len(snd.sent) != 1 || snd.sent[0].Title != "결재 대기" || snd.sent[0].URL != "/assets" {
		t.Fatalf("즉시 알림 하나만 나가야 합니다: %+v", snd.sent)
	}
	for _, r := range st.rows {
		if r.DeliveredAt == nil {
			t.Fatalf("%s 가 대기로 남았습니다", r.Title)
		}
	}
}

func TestDuplicateKeyIsOneNotification(t *testing.T) {
	svc, st, snd, _ := setup("2026-10-10 14:00")
	ctx := context.Background()
	svc.Accept(ctx, ev("collect.fail", "now", "카톡 수집 멈춤", "k1"))
	_, dup, _ := svc.Accept(ctx, ev("collect.fail", "now", "카톡 수집 멈춤", "k1"))
	if !dup || len(st.rows) != 1 || len(snd.sent) != 1 {
		t.Fatalf("dup=%v rows=%d sent=%d", dup, len(st.rows), len(snd.sent))
	}
}

func TestQuietHoursHoldThenOnePushInTheMorning(t *testing.T) {
	svc, _, snd, now := setup("2026-10-10 02:00")
	ctx := context.Background()
	svc.Accept(ctx, ev("collect.fail", "now", "카톡 수집 멈춤", ""))
	svc.Accept(ctx, ev("job.login", "now", "쿠팡 로그인 필요", ""))
	svc.Tick(ctx)
	if len(snd.sent) != 0 {
		t.Fatalf("방해 금지 시간에 나갔습니다: %+v", snd.sent)
	}
	*now = now.Add(6 * time.Hour) // 08:00
	svc.Tick(ctx)
	if len(snd.sent) != 1 || !strings.HasPrefix(snd.sent[0].Title, "밤사이 알림 2건") {
		t.Fatalf("아침에 한 번에 나가야 합니다: %+v", snd.sent)
	}
	svc.Tick(ctx)
	if len(snd.sent) != 1 {
		t.Fatalf("두 번 나갔습니다: %+v", snd.sent)
	}
}

func TestBusyHoldsUntilItEnds(t *testing.T) {
	svc, _, snd, now := setup("2026-10-10 18:00")
	ctx := context.Background()
	svc.Busy(true, "운전")
	svc.Accept(ctx, ev("mind.card", "now", "늦는다고 보낼까요?", ""))
	svc.Accept(ctx, ev("collect.fail", "now", "카톡 수집 멈춤", ""))
	svc.Tick(ctx)
	if len(snd.sent) != 0 {
		t.Fatalf("운전 중에 나갔습니다: %+v", snd.sent)
	}
	svc.Busy(false, "")
	svc.Tick(ctx)
	if len(snd.sent) != 1 || !strings.HasPrefix(snd.sent[0].Title, "운전·회의 중 알림 2건") {
		t.Fatalf("운전이 끝나면 한 번에 나가야 합니다: %+v", snd.sent)
	}
	// Never heard the end: it lapses.
	svc.Busy(true, "회의")
	svc.Accept(ctx, ev("mind.card", "now", "카드", ""))
	*now = now.Add(busyLapse + time.Minute)
	svc.Tick(ctx)
	if len(snd.sent) != 2 {
		t.Fatalf("끝을 못 들어도 풀려야 합니다: %+v", snd.sent)
	}
}

// A moment - the time asked for, the door - rings then: quiet hours and a
// drive do not hold it, the category turned off does.
func TestMomentRingsThroughQuietHoursAndBusy(t *testing.T) {
	svc, st, snd, _ := setup("2026-10-10 06:00")
	ctx := context.Background()
	svc.Accept(ctx, ev("remind.asked", "now", "6시에 깨워 달라고 하셨어요", ""))
	svc.Accept(ctx, ev("mind.notice", "now", "아침 브리핑", ""))
	if len(snd.sent) != 1 || snd.sent[0].Title != "6시에 깨워 달라고 하셨어요" {
		t.Fatalf("부탁한 시각에는 방해 금지 시간에도 울려야 합니다: %+v", snd.sent)
	}
	svc.Busy(true, "운전")
	svc.Accept(ctx, ev("remind.rain", "now", "15시부터 비 · 우산 챙기세요", ""))
	if len(snd.sent) != 2 {
		t.Fatalf("운전 중에도 울려야 합니다: %+v", snd.sent)
	}
	svc.Busy(false, "")
	set := svc.Settings(ctx, store.Owner)
	set.Muted = []string{"remind"}
	if _, err := svc.SaveSettings(ctx, store.Owner, set); err != nil {
		t.Fatal(err)
	}
	svc.Accept(ctx, ev("remind.asked", "now", "꺼 둔 분류", ""))
	if len(snd.sent) != 2 || st.rows[len(st.rows)-1].DeliveredAt == nil {
		t.Fatalf("꺼 둔 분류는 알림함에만: %+v", snd.sent)
	}
}

// When to leave on a route is a moment too - for the guest as well, whose
// settings list the kind so it can be turned off.
func TestTransitRingsAtItsMinute(t *testing.T) {
	svc, _, snd, _ := setup("2026-10-10 07:10")
	ctx := context.Background()
	svc.Accept(ctx, ev("transit.pack", "now", "슬슬 짐 싸야 해요", ""))
	if len(snd.sent) != 1 || snd.sent[0].Title != "슬슬 짐 싸야 해요" {
		t.Fatalf("출발 알림은 방해 금지 시간에도 울려야 합니다: %+v", snd.sent)
	}
	guest := ev("transit.go", "now", "지금 나가요", "")
	guest.Audience = store.Guest
	svc.Accept(ctx, guest)
	if len(snd.guest) != 1 || snd.guest[0].Title != "지금 나가요" {
		t.Fatalf("손님의 출발 알림: %+v", snd.guest)
	}
	if !slices.ContainsFunc(CategoriesFor(store.Guest), func(c Category) bool { return c.ID == "transit" }) {
		t.Fatal("손님 설정에도 출발 분류가 있어야 끌 수 있습니다")
	}
}

func TestDigestOnceAtItsTime(t *testing.T) {
	svc, st, snd, now := setup("2026-10-10 12:00")
	ctx := context.Background()
	svc.Accept(ctx, ev("spending.big", "digest", "큰 결제 ₩120,000", ""))
	svc.Accept(ctx, ev("assets.flow", "digest", "한투 입금 감지", ""))
	svc.Tick(ctx)
	if len(snd.sent) != 0 {
		t.Fatalf("요약 시각 전에 나갔습니다")
	}
	*now = now.Add(9 * time.Hour) // 21:00
	svc.Tick(ctx)
	if len(snd.sent) != 1 || snd.sent[0].Title != "오늘 요약 2건" {
		t.Fatalf("요약이 한 번 나가야 합니다: %+v", snd.sent)
	}
	svc.Accept(ctx, ev("spending.big", "digest", "늦은 결제", ""))
	*now = now.Add(time.Hour)
	svc.Tick(ctx)
	if len(snd.sent) != 1 {
		t.Fatalf("하루에 두 번 나갔습니다")
	}
	if p, _ := st.Pending(ctx, store.Owner, "digest"); len(p) != 1 {
		t.Fatalf("늦은 건은 내일 요약으로 남아야 합니다: %d", len(p))
	}
}

func TestMutedCategoryIsKeptButNotPushed(t *testing.T) {
	svc, st, snd, _ := setup("2026-10-10 14:00")
	ctx := context.Background()
	set := Defaults()
	set.Muted = []string{"calendar"}
	if _, err := svc.SaveSettings(ctx, store.Owner, set); err != nil {
		t.Fatal(err)
	}
	svc.Accept(ctx, ev("calendar.partner", "now", "새 공유 일정", ""))
	if len(snd.sent) != 0 || len(st.rows) != 1 || st.rows[0].DeliveredAt == nil {
		t.Fatalf("끈 분류는 저장만: sent=%d rows=%d", len(snd.sent), len(st.rows))
	}
}

func TestDigestInsideQuietHoursIsRefused(t *testing.T) {
	svc, _, _, _ := setup("2026-10-10 14:00")
	set := Defaults()
	set.DigestAt = "07:00"
	if _, err := svc.SaveSettings(context.Background(), store.Owner, set); err == nil {
		t.Fatal("방해 금지 시간 안의 요약 시각이 저장됐습니다")
	}
}

func TestInvalidEvent(t *testing.T) {
	svc, _, _, _ := setup("2026-10-10 14:00")
	if _, _, err := svc.Accept(context.Background(), Event{Source: "x", Kind: "a.b", Tier: "soon", Title: "t"}); err == nil {
		t.Fatal("잘못된 tier 가 통과했습니다")
	}
	n, _, _ := svc.Accept(context.Background(), Event{Source: "x", Kind: "a.b", Tier: "log", Title: "t", URL: "https://evil"})
	if n.URL != "/" {
		t.Fatalf("바깥 주소가 남았습니다: %q", n.URL)
	}
}

// A grouped push goes to the screen its notifications are about, and to
// the inbox only when they are about different ones.
func TestGroupURL(t *testing.T) {
	n := func(urls ...string) []store.Notification {
		out := make([]store.Notification, len(urls))
		for i, u := range urls {
			out[i] = store.Notification{URL: u}
		}
		return out
	}
	for _, c := range []struct {
		in   []store.Notification
		want string
	}{
		{n("/spending", "/spending"), "/spending"},
		{n("/?proposal=p-1", "/?proposal=p-2"), "/"},
		{n("/spending", "/assets"), "/?inbox=1"},
	} {
		if got := groupURL(c.in); got != c.want {
			t.Fatalf("%v: %q, want %q", c.in, got, c.want)
		}
	}
}

// The guest's notifications go to the guest's devices only, on the guest's
// own settings; being busy is the operator's and does not hold them.
func TestGuestIsTheirOwnPerson(t *testing.T) {
	svc, st, snd, now := setup("2026-10-10 18:00")
	ctx := context.Background()
	svc.Busy(true, "운전")
	guest := ev("approval.pending", "now", "결재 대기", "")
	guest.Audience = store.Guest
	if _, _, err := svc.Accept(ctx, guest); err != nil {
		t.Fatal(err)
	}
	if len(snd.guest) != 1 || len(snd.sent) != 0 {
		t.Fatalf("손님 카드는 손님에게 바로: guest=%d owner=%d", len(snd.guest), len(snd.sent))
	}
	if st.rows[0].Audience != store.Guest {
		t.Fatalf("받는 사람이 저장되지 않았습니다: %q", st.rows[0].Audience)
	}

	// The guest's quiet hours hold the guest's push, not the operator's.
	set := Defaults()
	set.QuietStart, set.QuietEnd = "18:00", "19:00"
	if _, err := svc.SaveSettings(ctx, store.Guest, set); err != nil {
		t.Fatal(err)
	}
	svc.Busy(false, "")
	svc.Accept(ctx, guest)
	svc.Accept(ctx, ev("collect.fail", "now", "카톡 수집 멈춤", ""))
	if len(snd.guest) != 1 || len(snd.sent) != 1 {
		t.Fatalf("손님 방해 금지는 손님 것만: guest=%d owner=%d", len(snd.guest), len(snd.sent))
	}
	*now = now.Add(time.Hour)
	svc.Tick(ctx)
	if len(snd.guest) != 2 || len(snd.sent) != 1 {
		t.Fatalf("끝나면 손님에게 나가야 합니다: guest=%d owner=%d", len(snd.guest), len(snd.sent))
	}
	if svc.Settings(ctx, store.Owner).QuietStart != Defaults().QuietStart {
		t.Fatal("손님 설정이 운영자 설정을 바꿨습니다")
	}

	bad := ev("approval.pending", "now", "t", "")
	bad.Audience = "everyone"
	if _, _, err := svc.Accept(ctx, bad); err == nil {
		t.Fatal("모르는 받는 사람이 통과했습니다")
	}
}

func TestGuestSeesOnlyTheirCategories(t *testing.T) {
	memory := false
	for _, c := range CategoriesFor(store.Guest) {
		if c.ID == "location" || c.ID == "assets" || c.ID == "spending" {
			t.Fatalf("운영자 전용 분류가 손님에게 보입니다: %s", c.ID)
		}
		memory = memory || c.ID == "memory"
	}
	if !memory {
		t.Fatal("손님 자비스의 기억 알림을 끌 수 있어야 합니다")
	}
	if len(CategoriesFor(store.Owner)) != len(Categories) {
		t.Fatal("운영자 분류가 줄었습니다")
	}
}
