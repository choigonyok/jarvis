package assets

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
)

var kst = time.FixedZone("KST", 9*3600)

// Only what a person said was spent becomes a row: at what they said it was
// for, or the account it left; named for the answer, so answering again
// makes a new row.
func TestSpendsAreTheAnsweredSpendParts(t *testing.T) {
	got := Spends([]Outflow{
		{Key: "upbit:a", Date: "2026-03-09", Venue: "upbit", Parts: []Part{
			{Use: "spend", AmountKrw: 30000, Category: "생활·통신", Memo: "관리비", DecidedAt: "2026-03-13T01:00:00.000Z"},
			{Use: "card", AmountKrw: 12000, DecidedAt: "2026-03-13T01:00:00.000Z"},
		}},
		{Key: "auto:kis:2026-03-20", Date: "2026-03-20", Venue: "kis", Parts: []Part{
			{Use: "spend", AmountKrw: 10000.4, Category: "기타", DecidedAt: "2026-03-23T01:00:00.000Z"},
		}},
		// Found, not said: a transfer, and nothing spent.
		{Key: "manual:3", Date: "2026-04-21", Venue: "kis", Parts: []Part{{Use: "transfer", AmountKrw: 90000}}},
		{Key: "upbit:b", Date: "2026-04-27", Venue: "upbit"},
	}, kst)
	if len(got) != 2 {
		t.Fatalf("지출 몫만: %+v", got)
	}
	rent, cash := got[0], got[1]
	if rent.Ref != "asset:upbit:a@2026-03-13T01:00:00.000Z" || rent.Merchant != "관리비" || rent.Issuer != "업비트" ||
		rent.AmountKrw != 30000 || rent.Category != "생활·통신" ||
		!rent.ApprovedAt.Equal(time.Date(2026, 3, 9, 12, 0, 0, 0, kst)) {
		t.Fatalf("관리비 줄: %+v", rent)
	}
	if cash.Merchant != "한국투자증권 출금" || cash.Issuer != "한국투자증권" || cash.AmountKrw != 10000 {
		t.Fatalf("말 없는 지출은 계좌 이름으로: %+v", cash)
	}
}

// A list that did not come whole changes nothing: the rows are only matched
// to an answer that is there. No store: reaching it would panic, which is the
// point.
func TestTakeStopsAtAPartList(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, body := range []string{`{"since":"2026-05-01"}`, `not json`} {
		if err := Take(context.Background(), nil, kst, log, []byte(body)); !errors.Is(err, queue.ErrRefused) {
			t.Fatalf("%s: %v", body, err)
		}
	}
}
