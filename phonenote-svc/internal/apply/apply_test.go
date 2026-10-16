package apply

import (
	"context"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/phonenote-svc/internal/note"
)

type book struct {
	txs   []Tx
	items map[int64][]note.Item
	memo  map[int64]string
}

func (b *book) Month(context.Context, string) ([]Tx, error) { return b.txs, nil }
func (b *book) SaveItems(_ context.Context, id int64, items []note.Item, _ string) error {
	b.items[id] = items
	return nil
}
func (b *book) SetMemo(_ context.Context, id int64, memo string) error {
	b.memo[id] = memo
	return nil
}

var kst = time.FixedZone("KST", 9*3600)

func order(at time.Time, amount int64, items ...string) note.Note {
	r := &note.Reading{Kind: note.DeliveryOrder, Meaningful: true, Merchant: "교촌치킨", AmountKrw: amount}
	for _, it := range items {
		r.Items = append(r.Items, note.Item{Name: it, Quantity: 1, Category: "식비"})
	}
	return note.Note{ID: 1, App: "com.jawebs.baedal", At: at, Reading: r}
}

// 1: a Baemin order's menu goes onto the card payment it belongs to - the
// one with the same amount, near in time.
func TestTheMenuGoesOntoItsPayment(t *testing.T) {
	at := time.Date(2026, 10, 17, 19, 2, 0, 0, kst)
	b := &book{items: map[int64][]note.Item{}, memo: map[int64]string{}, txs: []Tx{
		{ID: 10, AmountKrw: 25000, Merchant: "우아한형제들", ApprovedAt: at.Add(-2 * time.Hour), Status: "ok"},
		{ID: 11, AmountKrw: 25000, Merchant: "우아한형제들", ApprovedAt: at.Add(time.Minute), Status: "ok"},
		{ID: 12, AmountKrw: 9000, Merchant: "스타벅스", ApprovedAt: at, Status: "ok"},
	}}
	a := Applier{Spending: b, Loc: kst, Now: func() time.Time { return at.Add(time.Minute) }}
	applied, done, err := a.Apply(context.Background(), order(at, 25000, "허니콤보", "콜라"))
	if err != nil || !done {
		t.Fatal(err, done)
	}
	if len(b.items[11]) != 2 || b.memo[11] != "교촌치킨" || applied != "가계부 #11 에 상품 2개 · 가게 교촌치킨" {
		t.Fatalf("가까운 같은 금액 결제에 붙어야 합니다: %q %+v", applied, b.items)
	}

	// No amount read: the delivery app's nearest charge.
	b.items = map[int64][]note.Item{}
	if _, _, err := a.Apply(context.Background(), order(at, 0, "허니콤보")); err != nil || len(b.items[11]) != 1 {
		t.Fatalf("금액이 없으면 배달앱 결제 중 가장 가까운 것: %+v", b.items)
	}
}

// The card alert comes later: wait for it, then let go.
func TestWaitsForThePaymentThenLetsGo(t *testing.T) {
	at := time.Date(2026, 10, 17, 19, 2, 0, 0, kst)
	b := &book{items: map[int64][]note.Item{}, memo: map[int64]string{}}
	now := at.Add(10 * time.Minute)
	a := Applier{Spending: b, Loc: kst, Now: func() time.Time { return now }}
	if _, done, _ := a.Apply(context.Background(), order(at, 25000, "허니콤보")); done {
		t.Fatal("결제가 아직 안 들어왔으면 기다려야 합니다")
	}
	now = at.Add(Wait + time.Minute)
	if _, done, _ := a.Apply(context.Background(), order(at, 25000, "허니콤보")); !done {
		t.Fatal("오래 기다렸으면 놓아야 합니다")
	}
}

// A payment that already has its items keeps them.
func TestAnItemizedPaymentIsLeftAlone(t *testing.T) {
	at := time.Date(2026, 10, 17, 19, 2, 0, 0, kst)
	tx := Tx{ID: 20, AmountKrw: 25000, Merchant: "우아한형제들", Memo: "직접 적음", ApprovedAt: at, Status: "ok"}
	tx.Items = append(tx.Items, struct {
		Name string `json:"name"`
	}{Name: "허니콤보"})
	b := &book{items: map[int64][]note.Item{}, memo: map[int64]string{}, txs: []Tx{tx}}
	applied, done, _ := Applier{Spending: b, Loc: kst}.Apply(context.Background(), order(at, 25000, "허니콤보"))
	if !done || applied != "" || len(b.items) != 0 || len(b.memo) != 0 {
		t.Fatalf("이미 상품이 있으면 그대로: %q", applied)
	}
}
