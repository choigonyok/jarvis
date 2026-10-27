package mind

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

func pending(id, subject, relation, object, text string, at time.Time) model.Fact {
	p := func(n string) model.Party {
		if n == "나" {
			return model.Party{Name: "나", Self: true}
		}
		return model.Party{Name: n, Kind: "Person"}
	}
	return model.Fact{ID: id, Room: "r", Subject: p(subject), Relation: relation, Object: p(object), Text: text,
		Status: model.FactPending, NextTry: at, CreatedAt: at}
}

// A pending fact goes to memory; memory's judgement comes back onto the
// ledger, and the names it placed go into the alias table - so the next fact
// about the same person goes by node.
func TestRememberStoresAndLearnsNames(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	st := store.NewMemory()
	mem := memory.NewFake(func() time.Time { return now })
	r := NewRemember(st, mem, func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_ = st.PutFact(ctx, pending("mf_1", "지수", "DISLIKES", "매운 음식", "지수는 매운 음식을 못 먹는다", now))
	_ = st.PutFact(ctx, pending("mf_2", "나", "FRIEND_OF", "지수", "지수는 나의 대학 동기다", now))
	if n, err := r.Flush(ctx); err != nil || n != 2 {
		t.Fatalf("둘 다 가야 합니다: %d %v", n, err)
	}
	f, _ := st.Fact(ctx, "mf_1")
	if f.Status != model.FactStored || f.Memory == nil || f.Subject.Node == "" || f.StoredAt == nil {
		t.Fatalf("판단이 장부에: %+v", f)
	}
	friend, _ := st.Fact(ctx, "mf_2")
	if friend.Object.Node != f.Subject.Node {
		t.Fatalf("같은 지수여야 합니다: %s vs %s", friend.Object.Node, f.Subject.Node)
	}
	as, _ := st.Aliases(ctx)
	if len(as) != 2 || as[0].Name != "매운 음식" || as[1].Name != "지수" || as[1].Seen != 2 {
		t.Fatalf("별칭: %+v", as)
	}

	// The same sentence again is a duplicate; a new fact under the same
	// relation between the same two retires the old one.
	_ = st.PutFact(ctx, pending("mf_3", "지수", "DISLIKES", "매운 음식", "지수는 매운 음식을 못 먹는다", now))
	_ = st.PutFact(ctx, pending("mf_4", "지수", "DISLIKES", "매운 음식", "지수는 이제 매운 것도 조금 먹는다", now.Add(time.Second)))
	now = now.Add(time.Second)
	if _, err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if dup, _ := st.Fact(ctx, "mf_3"); dup.Status != model.FactDuplicate {
		t.Fatalf("중복: %+v", dup)
	}
	if newer, _ := st.Fact(ctx, "mf_4"); newer.Memory == nil || len(newer.Memory.Ended) != 1 || newer.Memory.Ended[0].Fact != "지수는 매운 음식을 못 먹는다" {
		t.Fatalf("끝낸 사실: %+v", newer.Memory)
	}
}

type down struct {
	memory.Client
	calls *int
}

func (d down) Put(context.Context, []model.Fact) ([]memory.Result, error) {
	if d.calls != nil {
		*d.calls++
	}
	return nil, errors.New("memory-svc 가 꺼져 있음")
}

// later answers every fact with later: an outage of memory's own (budget)
// when outage is set, else a failure of that fact.
type later struct {
	memory.Client
	outage bool
}

func (l later) Put(_ context.Context, fs []model.Fact) ([]memory.Result, error) {
	out := []memory.Result{}
	for _, f := range fs {
		out = append(out, memory.Result{Ref: f.ID, Later: "그래프에 넣지 못했습니다", Outage: l.outage})
	}
	return out, nil
}

// refuses turns down any batch with the fact bad in it, as memory-svc's 422.
type refuses struct {
	*memory.Fake
	bad string
}

func (r refuses) Put(ctx context.Context, fs []model.Fact) ([]memory.Result, error) {
	for _, f := range fs {
		if f.ID == r.bad {
			return nil, fmt.Errorf("%w: memory-svc POST /facts: 422", model.ErrRefused)
		}
	}
	return r.Fake.Put(ctx, fs)
}

// Memory down, or out of its own budget, is an outage: the fact keeps its
// tries, flushing pauses longer each time, and nothing is given up on.
func TestAnOutageCostsAFactNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	st := store.NewMemory()
	clock := func() time.Time { return now }
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = st.PutFact(ctx, pending("mf_1", "지수", "LIKES", "뿌링클", "지수는 뿌링클을 좋아한다", now))

	calls := 0
	r := NewRemember(st, down{calls: &calls}, clock, quiet)
	for i := 0; i < 30; i++ {
		_, _ = r.Flush(ctx)
		now = now.Add(20 * time.Second)
	}
	f, _ := st.Fact(ctx, "mf_1")
	if f.Status != model.FactPending || f.Tries != 0 {
		t.Fatalf("꺼진 기억은 사실의 시도가 아닙니다: %+v", f)
	}
	if calls >= 10 {
		t.Fatalf("10분 동안 %d번 두드렸습니다: 갈수록 오래 쉬어야 합니다", calls)
	}

	budget := NewRemember(st, later{outage: true}, clock, quiet)
	for i := 0; i < 3*health.MaxTries; i++ {
		now = now.Add(7 * time.Hour)
		_, _ = budget.Flush(ctx)
	}
	if f, _ = st.Fact(ctx, "mf_1"); f.Status != model.FactPending || f.Tries != 0 || f.Error == "" {
		t.Fatalf("예산이 다 된 것도 장애입니다: %+v", f)
	}
}

// A fact that fails on its own is tried later each time and held after
// health.MaxTries - not given up: it goes again once when memory comes back
// from an outage, a person can release or drop it, and the life ledgers keep
// reading it until it is dropped.
func TestAFactThatKeepsFailingIsHeldNotGivenUp(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	st := store.NewMemory()
	clock := func() time.Time { return now }
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = st.PutFact(ctx, pending("mf_1", "지수", "LIKES", "뿌링클", "지수는 뿌링클을 좋아한다", now))

	r := NewRemember(st, later{}, clock, quiet)
	if _, err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	f, _ := st.Fact(ctx, "mf_1")
	if f.Status != model.FactPending || f.Tries != 1 || !f.NextTry.Equal(now.Add(time.Minute)) {
		t.Fatalf("1분 뒤 다시: %+v", f)
	}
	for range health.MaxTries {
		now = now.Add(7 * time.Hour)
		_, _ = r.Flush(ctx)
	}
	f, _ = st.Fact(ctx, "mf_1")
	if f.Status != model.FactHeld || f.Error == "" || f.Tries != health.MaxTries {
		t.Fatalf("여러 번 안 되면 보류: %+v", f)
	}
	if held, _ := st.HeldFacts(ctx); len(held) != 1 {
		t.Fatalf("보류함: %+v", held)
	}

	// Memory out, then back: what was held goes once more, and stored.
	mem := memory.NewFake(clock)
	r.Memory = down{}
	_, _ = r.Flush(ctx) // nothing due: not even asked
	_ = st.PutFact(ctx, pending("mf_2", "나", "FRIEND_OF", "지수", "지수는 나의 대학 동기다", now))
	_, _ = r.Flush(ctx)
	r.Memory = mem
	now = now.Add(time.Hour)
	if _, err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if f, _ = st.Fact(ctx, "mf_1"); f.Status != model.FactPending {
		t.Fatalf("기억이 돌아오면 보류한 사실을 다시 보냅니다: %+v", f)
	}
	if _, err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if f, _ = st.Fact(ctx, "mf_1"); f.Status != model.FactStored {
		t.Fatalf("다시 보낸 사실이 들어가야 합니다: %+v", f)
	}

	// Held again; a person drops it.
	_ = st.PutFact(ctx, model.Fact{ID: "mf_3", Room: "r", Subject: model.Party{Name: "나", Self: true}, Relation: "WANTS",
		Object: model.Party{Name: "캠핑 의자"}, Text: "캠핑 의자를 사고 싶다", Status: model.FactHeld, CreatedAt: now})
	if !wish(model.Fact{Status: model.FactHeld, Relation: "WANTS", Object: model.Party{Name: "캠핑 의자"}}) {
		t.Fatal("보류한 사실도 생활 장부에는 남습니다")
	}
	if err := r.Drop(ctx, "mf_3"); err != nil {
		t.Fatal(err)
	}
	if f, _ = st.Fact(ctx, "mf_3"); f.Status != model.FactDropped || wish(f) {
		t.Fatalf("버린 사실은 장부에서도 빠집니다: %+v", f)
	}
	if err := r.Drop(ctx, "mf_1"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("보류하지 않은 사실은 버리지 않습니다: %v", err)
	}
}

// A batch memory turns down as a whole goes again one fact at a time: the
// fact it will not take is held at once, the others go in.
func TestARefusedBatchCostsOnlyTheFactRefused(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	st := store.NewMemory()
	clock := func() time.Time { return now }
	r := NewRemember(st, refuses{memory.NewFake(clock), "mf_2"}, clock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_ = st.PutFact(ctx, pending("mf_1", "지수", "LIKES", "뿌링클", "지수는 뿌링클을 좋아한다", now))
	_ = st.PutFact(ctx, pending("mf_2", "지수", "LIKES", "", "", now))
	_ = st.PutFact(ctx, pending("mf_3", "나", "FRIEND_OF", "지수", "지수는 나의 대학 동기다", now))
	if _, err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"mf_1": model.FactStored, "mf_2": model.FactHeld, "mf_3": model.FactStored} {
		if f, _ := st.Fact(ctx, id); f.Status != want {
			t.Fatalf("%s: %s, want %s (%s)", id, f.Status, want, f.Error)
		}
	}
	if n, err := r.Release(ctx, "mf_2"); err != nil || n != 1 {
		t.Fatalf("사람이 다시 보내기: %d %v", n, err)
	}
	if f, _ := st.Fact(ctx, "mf_2"); f.Status != model.FactPending {
		t.Fatalf("%+v", f)
	}
}
