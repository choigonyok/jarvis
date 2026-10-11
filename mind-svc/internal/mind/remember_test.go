package mind

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

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

type down struct{ memory.Client }

func (down) Put(context.Context, []model.Fact) ([]memory.Result, error) {
	return nil, errors.New("memory-svc 가 꺼져 있음")
}

type later struct{ memory.Client }

func (later) Put(_ context.Context, fs []model.Fact) ([]memory.Result, error) {
	out := []memory.Result{}
	for _, f := range fs {
		out = append(out, memory.Result{Ref: f.ID, Later: "오늘 LLM 예산을 다 써서 내일 넣습니다."})
	}
	return out, nil
}

// Memory down or out of budget: the fact stays pending with a later try,
// doubling each time, and is given up on after MaxTries.
func TestRememberRetries(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	st := store.NewMemory()
	clock := func() time.Time { return now }
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = st.PutFact(ctx, pending("mf_1", "지수", "LIKES", "뿌링클", "지수는 뿌링클을 좋아한다", now))

	if _, err := NewRemember(st, down{}, clock, quiet).Flush(ctx); err == nil {
		t.Fatal("꺼져 있으면 오류")
	}
	f, _ := st.Fact(ctx, "mf_1")
	if f.Status != model.FactPending || f.Tries != 1 || !f.NextTry.Equal(now.Add(time.Minute)) {
		t.Fatalf("1분 뒤 다시: %+v", f)
	}
	r := NewRemember(st, later{}, clock, quiet)
	for range MaxTries {
		now = now.Add(7 * time.Hour)
		_, _ = r.Flush(ctx)
	}
	f, _ = st.Fact(ctx, "mf_1")
	if f.Status != model.FactFailed || f.Error == "" {
		t.Fatalf("여러 번 안 되면 포기: %+v", f)
	}
}
