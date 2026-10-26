package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/spending-svc/internal/enrich"
	"github.com/choigonyok/jarvis/spending-svc/internal/parse"
)

// These run against a real Postgres with every migration applied
// (scripts/dbtest starts a throwaway one). Without JARVIS_TEST_DSN they skip.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("JARVIS_TEST_DSN")
	if dsn == "" {
		t.Skip("JARVIS_TEST_DSN 이 없어 건너뜁니다 (scripts/dbtest)")
	}
	s, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	// raw_events is append-only; every row here has an id of its own instead,
	// and only the ledger is emptied - with DELETE, which the service's own
	// role (svc_spending) is allowed, unlike TRUNCATE.
	for _, q := range []string{`delete from card_transaction_items`, `delete from card_transactions`} {
		if _, err := s.pool.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

type ledger struct {
	t   *testing.T
	s   *Store
	seq int
}

// add records one alert the way ingest does: the raw message, then the alert.
func (l *ledger) add(kind, merchant, tail string, amount int64, at time.Time) int64 {
	l.t.Helper()
	ctx := context.Background()
	l.seq++
	ref := fmt.Sprintf("%s-%d-%d", l.t.Name(), time.Now().UnixNano(), l.seq)
	ev, err := l.s.RawEvent(ctx, ref, "테스트카드", merchant, at)
	if err != nil {
		l.t.Fatal(err)
	}
	a := parse.Alert{Issuer: "테스트카드", CardTail: tail, Kind: kind, AmountKrw: amount, Currency: "KRW", Merchant: merchant, ApprovedAt: at}
	if _, err := l.s.AddAlert(ctx, ref, ev, a, amount, false); err != nil {
		l.t.Fatal(err)
	}
	var id int64
	if err := l.s.pool.QueryRow(ctx, `select id from card_transactions where source_ref = $1`, ref).Scan(&id); err != nil {
		l.t.Fatal(err)
	}
	return id
}

func (l *ledger) cancelledBy(id int64) int64 {
	l.t.Helper()
	var by *int64
	if err := l.s.pool.QueryRow(context.Background(), `select cancelled_by from card_transactions where id = $1`, id).Scan(&by); err != nil {
		l.t.Fatal(err)
	}
	if by == nil {
		return 0
	}
	return *by
}

// G-D6: a cancellation pairs with the charge it undoes - the same merchant
// first. The newest charge of that amount on the same card used to win even
// at another merchant.
func TestCancelPairsWithItsOwnCharge(t *testing.T) {
	l := &ledger{t: t, s: testStore(t)}
	t0 := time.Date(2026, 10, 16, 9, 0, 0, 0, time.FixedZone("KST", 9*3600))

	coffee := l.add(parse.KindApproval, "스타벅스 강남점", "1234", 5000, t0)
	store := l.add(parse.KindApproval, "GS25 역삼점", "1234", 5000, t0.Add(time.Hour))
	cancel := l.add(parse.KindCancel, "스타벅스 강남점", "1234", 5000, t0.Add(2*time.Hour))
	if got := l.cancelledBy(coffee); got != cancel {
		t.Fatalf("같은 가맹점의 승인이 취소돼야 합니다: coffee.cancelled_by=%d, want %d", got, cancel)
	}
	if got := l.cancelledBy(store); got != 0 {
		t.Fatalf("다른 가맹점의 승인은 그대로여야 합니다: store.cancelled_by=%d", got)
	}

	// The card alone is not enough when it made two such charges.
	a := l.add(parse.KindApproval, "가게일", "9876", 7000, t0)
	b := l.add(parse.KindApproval, "가게이", "9876", 7000, t0.Add(10*time.Minute))
	l.add(parse.KindCancel, "알수없는곳", "9876", 7000, t0.Add(time.Hour))
	if l.cancelledBy(a) != 0 || l.cancelledBy(b) != 0 {
		t.Fatal("카드만 같고 후보가 둘이면 짝짓지 않아야 합니다")
	}

	// ...and is enough when there is only one.
	only := l.add(parse.KindApproval, "가게삼", "5555", 8000, t0)
	c := l.add(parse.KindCancel, "다른 표기의 가게", "5555", 8000, t0.Add(time.Hour))
	if got := l.cancelledBy(only); got != c {
		t.Fatalf("카드가 같은 후보가 하나면 짝지어야 합니다: %d, want %d", got, c)
	}

	// The same merchant pairs even from another card (a reissued card).
	old := l.add(parse.KindApproval, "올리브영", "1111", 12000, t0)
	re := l.add(parse.KindCancel, "올리브영", "2222", 12000, t0.Add(time.Hour))
	if got := l.cancelledBy(old); got != re {
		t.Fatalf("같은 가맹점이면 카드가 달라도 짝지어야 합니다: %d, want %d", got, re)
	}
}

// What the rules could not place is what gets asked about: a charge left in
// 기타 by a rule, and a product put in 기타. Not one a person set - to 기타
// too, which is how "do not ask again" is kept - nor a cancelled or excluded
// charge, nor one before the window. Answered, it drops off the list.
func TestUnclearIsWhatTheRulesLeftInOther(t *testing.T) {
	s := testStore(t)
	l := &ledger{t: t, s: s}
	ctx := context.Background()
	t0 := time.Now().Add(-2 * time.Hour)

	unknown := l.add(parse.KindApproval, "어느극장(주)", "1234", 30000, t0)
	l.add(parse.KindApproval, "스타벅스 강남점", "1234", 5000, t0.Add(time.Minute))
	decided := l.add(parse.KindApproval, "어느가게", "1234", 7000, t0.Add(2*time.Minute))
	other := "기타"
	if _, err := s.Update(ctx, decided, Patch{Category: &other}); err != nil {
		t.Fatal(err)
	}
	gone := l.add(parse.KindApproval, "이름모를곳", "1234", 9000, t0.Add(3*time.Minute))
	yes := true
	if _, err := s.Update(ctx, gone, Patch{Excluded: &yes}); err != nil {
		t.Fatal(err)
	}
	l.add(parse.KindApproval, "취소된가게", "1234", 4000, t0.Add(4*time.Minute))
	l.add(parse.KindCancel, "취소된가게", "1234", 4000, t0.Add(5*time.Minute))
	l.add(parse.KindApproval, "옛날가게", "1234", 3000, t0.Add(-48*time.Hour))
	order := l.add(parse.KindApproval, "쿠팡", "1234", 20000, t0.Add(6*time.Minute))
	if err := s.SaveItems(ctx, order, []enrich.Item{
		{Name: "알 수 없는 물건", Quantity: 1, ListedKrw: 12000, Category: "기타"},
		{Name: "커피 원두", Quantity: 1, ListedKrw: 8000, Category: "카페·간식"},
	}, ""); err != nil {
		t.Fatal(err)
	}

	got, err := s.Unclear(ctx, t0.Add(-time.Hour), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != "item" || got[0].Name != "알 수 없는 물건" || got[0].TxID != order || got[0].Merchant != "쿠팡" ||
		got[1].Kind != "tx" || got[1].ID != unknown || got[1].AmountKrw != 30000 {
		t.Fatalf("물을 것: %+v", got)
	}

	culture := "문화·여가"
	if _, err := s.Update(ctx, unknown, Patch{Category: &culture, Remember: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetItemCategory(ctx, got[0].ID, "기타"); err != nil {
		t.Fatal(err)
	}
	if again, err := s.Unclear(ctx, t0.Add(-time.Hour), 20); err != nil || len(again) != 0 {
		t.Fatalf("답한 것은 다시 묻지 않아야 합니다: %+v %v", again, err)
	}
}

// Signed in again, the book picks up what stopped at the login page - and
// only that: a lookup that found nothing is not run again for it.
func TestResumeIsOnlyWhatTheLoginStopped(t *testing.T) {
	s := testStore(t)
	l := &ledger{t: t, s: s}
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	stopped := l.add(parse.KindApproval, "쿠팡", "1234", 34350, t0)
	missing := l.add(parse.KindApproval, "쿠팡", "1234", 8900, t0.Add(time.Minute))
	// Not found three times first: a login wall stops every lookup still
	// waiting on the site, and this one has to be done by then.
	for i := 0; i < 3; i++ {
		if err := s.Report(ctx, missing, "not_found", "주문목록에 없음"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Report(ctx, stopped, "login_required", "로그인 화면"); err != nil {
		t.Fatal(err)
	}
	status := func(id int64) string {
		var st string
		if err := s.pool.QueryRow(ctx, `select enrich_status from card_transactions where id=$1`, id).Scan(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	if status(stopped) != "login_required" || status(missing) != "not_found" {
		t.Fatalf("준비: %s %s", status(stopped), status(missing))
	}
	n, err := s.Resume(ctx, "coupang")
	if err != nil || n != 1 || status(stopped) != "pending" || status(missing) != "not_found" {
		t.Fatalf("로그인으로 멈춘 것만: n=%d err=%v %s %s", n, err, status(stopped), status(missing))
	}
}

// The book's asset rows follow the answers on the assets screen: a new answer
// comes in, a changed or withdrawn one leaves. What was changed here in the
// meantime - the category, the total - stays while its answer does. They are
// not deleted here: the answer would put them back.
func TestAssetSpendsFollowTheAnswers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	day := time.Date(2026, 3, 9, 12, 0, 0, 0, time.FixedZone("KST", 9*3600))
	rent := AssetSpend{Ref: "asset:upbit:a@2026-03-13T01:00:00.000Z", ApprovedAt: day, Issuer: "업비트", Merchant: "관리비", AmountKrw: 30000, Category: "생활·통신"}
	cash := AssetSpend{Ref: "asset:manual:9@2026-03-13T01:00:00.000Z", ApprovedAt: day, Issuer: "한국투자증권", Merchant: "한국투자증권 출금", AmountKrw: 10000, Category: "없는 분류"}

	added, removed, err := s.SyncAssetSpends(ctx, []AssetSpend{rent, cash})
	if err != nil || added != 2 || removed != 0 {
		t.Fatalf("처음: added=%d removed=%d err=%v", added, removed, err)
	}
	var id int64
	var src, cat, catSrc, issuer string
	if err := s.pool.QueryRow(ctx, `select id, source, category, category_source, issuer from card_transactions where source_ref = $1`, rent.Ref).
		Scan(&id, &src, &cat, &catSrc, &issuer); err != nil {
		t.Fatal(err)
	}
	if src != "asset" || cat != "생활·통신" || catSrc != "manual" || issuer != "업비트" {
		t.Fatalf("들어간 줄: %s %s %s %s", src, cat, catSrc, issuer)
	}
	if err := s.pool.QueryRow(ctx, `select category from card_transactions where source_ref = $1`, cash.Ref).Scan(&cat); err != nil || cat != "기타" {
		t.Fatalf("모르는 분류는 기타로: %q %v", cat, err)
	}

	food := "식비"
	if _, err := s.Update(ctx, id, Patch{Category: &food}); err != nil {
		t.Fatal(err)
	}
	if added, removed, err := s.SyncAssetSpends(ctx, []AssetSpend{rent, cash}); err != nil || added != 0 || removed != 0 {
		t.Fatalf("그대로면 아무것도 안 바뀝니다: %d %d %v", added, removed, err)
	}
	if err := s.pool.QueryRow(ctx, `select category from card_transactions where id = $1`, id).Scan(&cat); err != nil || cat != "식비" {
		t.Fatalf("가계부에서 고친 분류는 남습니다: %q %v", cat, err)
	}
	var kept KeptError
	if err := s.Delete(ctx, id); !errors.As(err, &kept) {
		t.Fatalf("투자금 줄은 여기서 지우지 않습니다: %v", err)
	}

	again := rent
	again.Ref = "asset:upbit:a@2026-03-20T01:00:00.000Z"
	again.AmountKrw = 25000
	if added, removed, err := s.SyncAssetSpends(ctx, []AssetSpend{again}); err != nil || added != 1 || removed != 2 {
		t.Fatalf("다시 답한 것은 새 줄, 사라진 답의 줄은 빠집니다: %d %d %v", added, removed, err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `select count(*) from card_transactions where source = 'asset'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("남은 투자금 줄: %d %v", n, err)
	}
}
