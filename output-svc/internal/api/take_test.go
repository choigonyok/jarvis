package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/output-svc/internal/live"
	"github.com/choigonyok/jarvis/output-svc/internal/notify"
	"github.com/choigonyok/jarvis/output-svc/internal/push"
	"github.com/choigonyok/jarvis/output-svc/internal/store"
)

type nopSender struct{ sent atomic.Int64 }

func (s *nopSender) Send(context.Context, string, push.Payload) (int, error) {
	s.sent.Add(1)
	return 1, nil
}

var seq atomic.Int64

func key(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), seq.Add(1))
}

func setup(t *testing.T) (*Server, *store.Store, *nopSender, *notify.Service) {
	t.Helper()
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없습니다 (scripts/dbtest output-svc)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := store.New(pool)
	sender := &nopSender{}
	seoul, _ := time.LoadLocation("Asia/Seoul")
	svc := notify.New(st, sender, seoul)
	hub := live.New()
	p := push.New(push.Keys{}, st, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return New(svc, st, p, hub, "tok", slog.New(slog.NewTextHandler(io.Discard, nil))), st, sender, svc
}

func effect(t *testing.T, kind, k string, payload map[string]any) queue.Message {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return queue.Message{ID: 1, Kind: kind, Source: "dbtest", SourceKey: k, OccurredAt: time.Now(), Payload: raw}
}

func inbox(t *testing.T, st *store.Store, who, k string) []store.Notification {
	t.Helper()
	pending, err := st.List(context.Background(), who, 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Notification
	for _, n := range pending.Items {
		if n.DedupeKey != nil && *n.DedupeKey == k {
			out = append(out, n)
		}
	}
	return out
}

// An effect with wording is a notification, once however often it comes; one
// without is passed over; one that cannot be one is refused, not retried.
func TestTakeMakesANotification(t *testing.T) {
	s, st, _, _ := setup(t)
	ctx := context.Background()
	k := key("take")
	m := effect(t, "spending.big", k, map[string]any{"notify": map[string]any{"tier": "log", "title": "큰 결제", "level": "info"}})
	for i := 0; i < 2; i++ {
		if err := s.Take(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if got := inbox(t, st, store.Owner, k); len(got) != 1 || got[0].Title != "큰 결제" || got[0].Source != "dbtest" {
		t.Fatalf("%+v", got)
	}
	if err := s.Take(ctx, effect(t, "mind.card", key("x"), map[string]any{"title": "카드"})); err != nil {
		t.Fatalf("문구가 없는 것은 지나갑니다: %v", err)
	}
	bad := effect(t, "a.b", key("x"), map[string]any{"notify": map[string]any{"tier": "loud", "title": "x"}})
	if err := s.Take(ctx, bad); !errors.Is(err, queue.ErrRefused) {
		t.Fatalf("다시 시도해도 안 될 것은 거절: %v", err)
	}
	if err := s.Take(ctx, queue.Message{Kind: "a", Payload: json.RawMessage(`[1`)}); !errors.Is(err, queue.ErrRefused) {
		t.Fatalf("읽지 못하는 것: %v", err)
	}
}

// The guest's goes to the guest's inbox.
func TestTakeKeepsTheAudience(t *testing.T) {
	s, st, _, _ := setup(t)
	k := key("guest")
	if err := s.Take(context.Background(), effect(t, "remind.asked", k, map[string]any{
		"notify": map[string]any{"tier": "log", "title": "약", "audience": "guest"}})); err != nil {
		t.Fatal(err)
	}
	if got := inbox(t, st, store.Guest, k); len(got) != 1 {
		t.Fatalf("손님 알림함: %+v", got)
	}
	if got := inbox(t, st, store.Owner, k); len(got) != 0 {
		t.Fatalf("운영자 알림함: %+v", got)
	}
}

// A card decided closes its "결재 대기"; a raised card takes the place of a
// digest line; being in a meeting holds pushes.
func TestTakeHearsWhatIsNotAPush(t *testing.T) {
	s, st, sender, _ := setup(t)
	ctx := context.Background()
	waiting := key("waiting")
	if err := s.Take(ctx, effect(t, "approval.pending", waiting, map[string]any{"notify": map[string]any{"tier": "log", "title": "결재 대기"}})); err != nil {
		t.Fatal(err)
	}
	if err := s.Take(ctx, effect(t, "proposal.decided", key("d"), map[string]any{"notifyKey": waiting})); err != nil {
		t.Fatal(err)
	}
	got := inbox(t, st, store.Owner, waiting)
	if len(got) != 1 || got[0].ReadAt == nil {
		t.Fatalf("읽음으로: %+v", got)
	}
	line := key("line")
	if err := s.Take(ctx, effect(t, "assets.band", line, map[string]any{"notify": map[string]any{"tier": "digest", "title": "이탈"}})); err != nil {
		t.Fatal(err)
	}
	if err := s.Take(ctx, effect(t, "suggestion.raised", key("s"), map[string]any{"supersedes": []string{line}})); err != nil {
		t.Fatal(err)
	}
	if got := inbox(t, st, store.Owner, line); len(got) != 1 || got[0].DeliveredAt == nil {
		t.Fatalf("보낸 것으로: %+v", got)
	}
	if sender.sent.Load() != 0 {
		t.Fatalf("푸시 %d", sender.sent.Load())
	}
}
