package main

import (
	"context"
	"sort"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/httpapi"
	"github.com/choigonyok/jarvis/agent/internal/module/jobs"
	"github.com/choigonyok/jarvis/agent/internal/module/spending"
	"github.com/choigonyok/jarvis/agent/internal/module/vault"
)

// chargeLog is the card alerts a job's run reads (jobs.ChargeLog): the
// spending book's transactions, which the card companies' KakaoTalk alerts
// fill in.
type chargeLog struct{ store *spending.Store }

// chargeStatus is what a book row's status says on a run's list: nothing
// for a plain payment.
var chargeStatus = map[string]string{"cancelled": "취소됨", "cancel": "취소", "refund": "환불", "excluded": "가계부 제외"}

func (c chargeLog) Charges(ctx context.Context, since time.Time) (jobs.Charges, error) {
	months := []string{since.Local().Format("2006-01")}
	if now := time.Now().Local().Format("2006-01"); now != months[0] {
		months = append(months, now)
	}
	var out jobs.Charges
	for _, month := range months {
		b, err := c.store.Month(ctx, month)
		if err != nil {
			return out, err
		}
		for _, t := range b.Transactions {
			if t.ApprovedAt.Before(since) {
				continue
			}
			out.List = append(out.List, jobs.Charge{At: t.ApprovedAt, Issuer: t.Issuer, Merchant: t.Merchant, Amount: t.AmountKrw, Status: chargeStatus[t.Status]})
		}
		out.Stale = out.Stale || b.Collector.Stale
		if at := b.Collector.LastAlertAt; at != nil && (out.LastAlertAt == nil || at.After(*out.LastAlertAt)) {
			out.LastAlertAt = at
		}
	}
	sort.SliceStable(out.List, func(i, k int) bool { return out.List[i].At.Before(out.List[k].At) })
	return out, nil
}

// savedAddress is the shipping address the vault keeps, for the payment
// card (nil without a vault).
func savedAddress(v *vault.Module) func(context.Context) (httpapi.SavedAddress, bool) {
	if v == nil {
		return nil
	}
	return func(ctx context.Context) (httpapi.SavedAddress, bool) {
		s, ok, err := v.Shipping(ctx)
		if err != nil || !ok {
			return httpapi.SavedAddress{}, false
		}
		return httpapi.SavedAddress{Recipient: s.Recipient, Address: s.Address, Detail: s.Detail, Doubt: v.AddressDoubt(ctx, s)}, true
	}
}
