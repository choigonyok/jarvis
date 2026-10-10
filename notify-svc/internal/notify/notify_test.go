package notify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/notify-svc/internal/push"
	"github.com/choigonyok/jarvis/notify-svc/internal/store"
)

type fakeStore struct {
	rows     []store.Notification
	settings []byte
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

func (f *fakeStore) Pending(_ context.Context, tier string) ([]store.Notification, error) {
	var out []store.Notification
	for _, r := range f.rows {
		if r.Tier == tier && r.DeliveredAt == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) Settings(_ context.Context, v any) error {
	if f.settings == nil {
		return nil
	}
	return json.Unmarshal(f.settings, v)
}

func (f *fakeStore) WriteSettings(_ context.Context, v any) error {
	f.settings, _ = json.Marshal(v)
	return nil
}

type fakeSender struct{ sent []push.Payload }

func (f *fakeSender) Send(_ context.Context, p push.Payload) (int, error) {
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
	if p, _ := st.Pending(ctx, "digest"); len(p) != 1 {
		t.Fatalf("늦은 건은 내일 요약으로 남아야 합니다: %d", len(p))
	}
}

func TestMutedCategoryIsKeptButNotPushed(t *testing.T) {
	svc, st, snd, _ := setup("2026-10-10 14:00")
	ctx := context.Background()
	set := Defaults()
	set.Muted = []string{"calendar"}
	if _, err := svc.SaveSettings(ctx, set); err != nil {
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
	if _, err := svc.SaveSettings(context.Background(), set); err == nil {
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
