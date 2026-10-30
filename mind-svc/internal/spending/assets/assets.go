// Package assets brings into the book what was paid out of the investment
// accounts.
//
// A card alert never sees money taken out of Upbit or KIS and then spent by
// transfer or in cash. assets-svc pairs each withdrawal with a deposit into
// another of the accounts; what nothing pairs is put to the operator on a
// card, and what they say was spent comes here in the record assets-svc leaves
// when the list changes (assets.outflows), and is kept as a row of its own
// (source 'asset'). Paying a card bill is not spent here - those charges are
// in the book already, from their alerts.
//
// The list comes whole every time and the rows are made to match it, the way
// a cursor would not: an answer changed or asked again takes its row out.
package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/store"
)

// Part and Outflow are the slice of assets-svc's list read here.
type Part struct {
	Use       string  `json:"use"`
	AmountKrw float64 `json:"amountKrw"`
	Category  string  `json:"category"`
	Memo      string  `json:"memo"`
	DecidedAt string  `json:"decidedAt"`
}

type Outflow struct {
	Key   string `json:"key"`
	Date  string `json:"date"`
	Venue string `json:"venue"`
	Parts []Part `json:"parts"`
}

var venueName = map[string]string{"upbit": "업비트", "kis": "한국투자증권", "gold": "금현물 계좌"}

// Spends is what the book should hold: one row per withdrawal a person said
// was (partly) spent, at what they said it was for ("월세") or, saying
// nothing, the account it left. A row is named for the answer, so answering
// again replaces it. The day is the withdrawal's - when the money left the
// pot; a hand-kept day has no hour, so it sits at noon. The memo is left to
// the 가계부 screen, as on any row.
func Spends(outflows []Outflow, loc *time.Location) []store.AssetSpend {
	var out []store.AssetSpend
	for _, o := range outflows {
		day, err := time.ParseInLocation("2006-01-02", o.Date, loc)
		if err != nil {
			continue
		}
		account := venueName[o.Venue]
		if account == "" {
			account = o.Venue
		}
		for _, p := range o.Parts {
			if p.Use != "spend" || p.DecidedAt == "" || p.AmountKrw < 1 {
				continue
			}
			merchant := strings.TrimSpace(p.Memo)
			if merchant == "" {
				merchant = account + " 출금"
			}
			out = append(out, store.AssetSpend{
				Ref:        "asset:" + o.Key + "@" + p.DecidedAt,
				ApprovedAt: day.Add(12 * time.Hour),
				Issuer:     account,
				Merchant:   merchant,
				AmountKrw:  int64(math.Round(p.AmountKrw)),
				Category:   p.Category,
			})
		}
	}
	return out
}

// Take is one list from assets-svc. A list that did not come whole changes
// nothing: rows are only taken out on one that did.
func Take(ctx context.Context, st *store.Store, loc *time.Location, log *slog.Logger, payload []byte) error {
	var p struct {
		Outflows *[]Outflow `json:"outflows"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("%w: %v", queue.ErrRefused, err)
	}
	if p.Outflows == nil {
		return fmt.Errorf("%w: %w", queue.ErrRefused, errors.New("자산 서비스가 출금 목록 없이 기록했습니다"))
	}
	added, removed, err := st.SyncAssetSpends(ctx, Spends(*p.Outflows, loc))
	if err != nil {
		return err
	}
	if added > 0 || removed > 0 {
		log.Info("투자 계좌에서 쓴 돈을 맞췄습니다", "added", added, "removed", removed)
	}
	return nil
}
