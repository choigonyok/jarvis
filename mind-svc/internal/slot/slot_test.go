package slot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/engine"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

var (
	saved = time.Date(2026, 10, 30, 3, 28, 0, 0, time.UTC)
	moved = saved.AddDate(0, 0, 10)
)

func lives(object string, from time.Time, to *time.Time) model.FactRow {
	return model.FactRow{ID: object, Subject: "self", Predicate: "LIVES_IN", Object: object, From: from, To: to, Single: true}
}

// The test the design set: a move puts the saved address in doubt.
func TestAMoveEndsAnAddress(t *testing.T) {
	r := Shipping.Read([]model.FactRow{lives("수원", saved.AddDate(0, -1, 0), &moved), lives("부산", moved, nil)}, saved, nil)
	if r.Holds || !strings.Contains(r.Why, "사는 곳이 바뀐") || r.Grade != engine.AskBlank {
		t.Fatalf("%+v", r)
	}
}

func TestAnAddressHoldsWithoutAMove(t *testing.T) {
	cases := map[string][]model.FactRow{
		"아무 기록 없음":       nil,
		"지금 사는 곳 그대로":    {lives("수원", saved.AddDate(0, -1, 0), nil)},
		"저장 전의 이사":       {lives("서울", saved.AddDate(-1, 0, 0), ptr(saved.AddDate(0, -1, 0))), lives("수원", saved.AddDate(0, -1, 0), nil)},
		"다른 사람의 이사":      {{Subject: "지수", Predicate: "LIVES_IN", From: saved, To: &moved}},
		"상관없는 사실의 끝":     {{Subject: "self", Predicate: "WORKS_AT", From: saved, To: &moved}},
		"저장 뒤 처음 안 사는 곳": {lives("수원", moved, nil)},
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			r := Shipping.Read(rows, saved, nil)
			if !r.Holds || r.Why != "" {
				t.Fatalf("%+v", r)
			}
			// However sure, an address is confirmed on the card: it is asked filled in.
			if r.Grade != engine.AskFilled {
				t.Fatalf("%+v", r)
			}
		})
	}
}

// Saved again after the move, it holds again.
func TestSavingAgainSettlesIt(t *testing.T) {
	rows := []model.FactRow{lives("수원", saved.AddDate(0, -1, 0), &moved), lives("부산", moved, nil)}
	if r := Shipping.Read(rows, moved.AddDate(0, 0, 1), nil); !r.Holds {
		t.Fatalf("%+v", r)
	}
}

func ptr[T any](v T) *T { return &v }

func TestGet(t *testing.T) {
	if d, ok := Get("shipping.address"); !ok || !d.Secret {
		t.Fatalf("%+v", d)
	}
	if _, ok := Get("nothing"); ok {
		t.Fatal("없는 슬롯입니다")
	}
}

type fakeLLM struct {
	answer string
	err    error
	calls  int
	prompt string
}

func (f *fakeLLM) Complete(_ context.Context, _, user string) (string, error) {
	f.calls++
	f.prompt = user
	return f.answer, f.err
}
func (f *fakeLLM) Ready() bool    { return true }
func (f *fakeLLM) Model() string  { return "test" }
func (f *fakeLLM) Spent() float64 { return 0 }

var _ llm.Client = (*fakeLLM)(nil)

func classifier(f *fakeLLM) (*Classifier, store.Store) {
	st := store.NewMemory()
	return &Classifier{St: st, LLM: f, Now: func() time.Time { return saved }}, st
}

var categories = []string{"식비", "교통", "기타"}

func TestAMerchantIsAskedOnce(t *testing.T) {
	f := &fakeLLM{answer: "```json\n[{\"id\":\"tx:1\",\"category\":\"식비\",\"confidence\":0.95}]\n```"}
	c, _ := classifier(f)
	ctx := context.Background()
	charge := Charge{Kind: "tx", ID: 1, Merchant: "써브웨이강남역점", AmountKrw: 9000}
	got := c.Classes(ctx, []Charge{charge}, categories)
	if got[charge.Key()].Value != "식비" || got[charge.Key()].P != 0.95 || got[charge.Key()].By != model.ClassByLLM {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(f.prompt, "기타") == false && strings.Contains(f.prompt, "분류: 식비, 교통, 기타") {
		t.Fatalf("기타는 고를 수 없습니다: %s", f.prompt)
	}
	// The next charge of the merchant asks nobody.
	again := Charge{Kind: "tx", ID: 2, Merchant: "써브웨이강남역점"}
	if got := c.Classes(ctx, []Charge{again}, categories); got[again.Key()].Value != "식비" || f.calls != 1 {
		t.Fatalf("%+v, 부른 횟수 %d", got, f.calls)
	}
}

// What cannot be placed has no entry: it is asked with the form empty.
func TestAnAnswerThatIsNoAnswer(t *testing.T) {
	for name, answer := range map[string]string{
		"기타":      `[{"id":"tx:1","category":"기타","confidence":0.2}]`,
		"없는 분류":   `[{"id":"tx:1","category":"우주","confidence":0.9}]`,
		"다른 id":   `[{"id":"tx:9","category":"식비","confidence":0.9}]`,
		"읽을 수 없음": `모르겠어요`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := classifier(&fakeLLM{answer: answer})
			if got := c.Classes(context.Background(), []Charge{{Kind: "tx", ID: 1, Merchant: "상호"}}, categories); len(got) != 0 {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestAModelThatFailsLeavesItAskedBlank(t *testing.T) {
	c, _ := classifier(&fakeLLM{err: errors.New("끊김")})
	if got := c.Classes(context.Background(), []Charge{{Kind: "tx", ID: 1, Merchant: "상호"}}, categories); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

// What the operator said is not asked of the model, and not written over.
func TestTheOperatorsWordStands(t *testing.T) {
	f := &fakeLLM{}
	c, st := classifier(f)
	ctx := context.Background()
	_ = st.PutClass(ctx, model.Class{Kind: MerchantCategory.Name, Key: "상호", Value: "교통", P: 1, By: model.ClassByOperator})
	if got := c.Classes(ctx, []Charge{{Kind: "tx", ID: 1, Merchant: "상호"}}, categories); got["상호"].Value != "교통" || f.calls != 0 {
		t.Fatalf("%+v %d", got, f.calls)
	}
	_ = st.PutClass(ctx, model.Class{Kind: MerchantCategory.Name, Key: "상호", Value: "식비", P: 0.9, By: model.ClassByLLM})
	if got, _ := st.Class(ctx, MerchantCategory.Name, "상호"); got.Value != "교통" {
		t.Fatalf("모델이 덮었습니다: %+v", got)
	}
}

func TestAProductIsItsOwnThing(t *testing.T) {
	if k := (Charge{Kind: "item", Merchant: "쿠팡", Name: "장작"}).Key(); k != "쿠팡 · 장작" {
		t.Fatal(k)
	}
}
