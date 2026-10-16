package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

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
