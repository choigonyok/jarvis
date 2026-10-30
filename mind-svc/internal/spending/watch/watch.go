// Package watch turns the ledger into news for output-svc: a big payment, one
// abroad, money coming back, a budget being crossed, and in the evening what
// today came to. It reads the same month the console shows (book.Summarize)
// every few minutes and says what is new since it last looked (unsaid) -
// each payment, budget line or alert once.
package watch

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/gocommon/notify"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/book"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/store"
)

// BigKrw is a payment worth a line in tonight's digest.
const BigKrw = 100_000

type Watcher struct {
	store  *store.Store
	notify *notify.Client
	log    *slog.Logger
	now    func() time.Time
	// Loops, when set, hears each check for /health ("watch").
	Loops *health.Loops
	// said is the keys already sent, and when (unsaid). Only the Run loop
	// touches it.
	said map[string]time.Time
}

func New(s *store.Store, n *notify.Client, log *slog.Logger) *Watcher {
	return &Watcher{store: s, notify: n, log: log, now: time.Now}
}

func (w *Watcher) Run(ctx context.Context) {
	if w.notify == nil {
		return
	}
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		if err := w.check(ctx); err != nil {
			w.log.Warn("가계부 알림을 확인하지 못했습니다", "err", err)
			w.Loops.Fail("watch", err)
		} else {
			w.Loops.OK("watch")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Watcher) check(ctx context.Context) error {
	now := w.now()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	txs, err := w.store.Range(ctx, month, month.AddDate(0, 1, 0))
	if err != nil {
		return err
	}
	prev, err := w.store.Range(ctx, month.AddDate(0, -1, 0), month)
	if err != nil {
		return err
	}
	budgets, err := w.store.Budgets(ctx)
	if err != nil {
		return err
	}
	events := Events(book.Summarize(month, txs, prev, budgets, now), now)
	unparsed, err := w.store.Unparsed(ctx)
	if err != nil {
		return err
	}
	for _, u := range unparsed {
		events = append(events, notify.Event{
			Source: "spending", Kind: "spending.unparsed", Tier: "digest", Level: "warn",
			Title: "읽지 못한 카드 알림", Body: u.ChatName + ": 합계에서 빠져 있어요", URL: "/spending?focus=unparsed",
			Key: "spending:unparsed:" + u.MessageID,
		})
	}
	for _, e := range w.unsaid(events, now) {
		w.notify.Send(e)
	}
	return nil
}

// saidFor is how long a sent key is remembered: past the 72 hours a payment
// is told for and the month a budget line is keyed by.
const saidFor = 40 * 24 * time.Hour

// unsaid is what of events was not sent yet. The check says what the month
// says now, every five minutes, and every payment, budget line and alert in
// it went out again each time - dropped by input-svc's dedupe, but an
// outbox row, a delivery and a refused insert apiece, every five minutes
// (2026-10-27). A restart forgets, and says each once more; input-svc drops
// that.
func (w *Watcher) unsaid(events []notify.Event, now time.Time) []notify.Event {
	if w.said == nil {
		w.said = map[string]time.Time{}
	}
	for k, at := range w.said {
		if now.Sub(at) > saidFor {
			delete(w.said, k)
		}
	}
	var out []notify.Event
	for _, e := range events {
		if e.Key != "" {
			if _, ok := w.said[e.Key]; ok {
				continue
			}
			w.said[e.Key] = now
		}
		out = append(out, e)
	}
	return out
}

// Events is what this month says right now. Only recent rows count, so the
// first run after a deploy does not report a month of old payments.
//
// Each one's tap lands on what it is about (?focus=, see web/src/lib/focus.ts):
// the payment's row, the budget line, the day's bar.
func Events(sum book.Summary, now time.Time) []notify.Event {
	var out []notify.Event
	page := "/spending?month=" + sum.Month + "&focus="
	add := func(kind, tier, level, title, body, key, focus string) {
		out = append(out, notify.Event{Source: "spending", Kind: kind, Tier: tier, Level: level,
			Title: title, Body: body, URL: page + url.QueryEscape(focus), Key: key})
	}
	recent := now.Add(-72 * time.Hour)
	for _, t := range sum.Transactions {
		if t.ApprovedAt.Before(recent) {
			continue
		}
		id := fmt.Sprint(t.ID)
		when := t.ApprovedAt.Format("1/2 15:04")
		row := "tx:" + id
		switch {
		case t.Status == "refund":
			add("spending.refund", "digest", "info", "환불 "+won(-t.SignedKrw), t.Merchant+" · "+when, "spending:refund:"+id, row)
		case t.Status == "cancel":
			add("spending.cancel", "digest", "info", "결제 취소 "+won(t.AmountKrw), t.Merchant+" · "+when, "spending:cancel:"+id, row)
		case t.Status != "ok":
		case t.Currency != "" && t.Currency != "KRW":
			add("spending.foreign", "digest", "info", "해외 결제 "+won(t.AmountKrw), t.Merchant+" · "+when, "spending:foreign:"+id, row)
		case t.AmountKrw >= BigKrw:
			add("spending.big", "digest", "info", "큰 결제 "+won(t.AmountKrw), t.Merchant+" · "+when, "spending:big:"+id, row)
		}
	}

	// A budget crossed: 80% is worth knowing tonight, 100% now.
	cross := func(name string, total, budget int64) {
		if budget <= 0 {
			return
		}
		label, focus := "이번 달", "budget"
		if name != "" {
			label, focus = name, "category:"+name
		}
		ratio := float64(total) / float64(budget)
		switch {
		case ratio >= 1:
			add("spending.budget", "now", "warn", label+" 예산을 넘었어요", won(total)+" / 예산 "+won(budget), "spending:budget:"+sum.Month+":"+name+":100", focus)
		case ratio >= 0.8:
			add("spending.budget", "digest", "info", label+" 예산의 80%를 썼어요", won(total)+" / 예산 "+won(budget), "spending:budget:"+sum.Month+":"+name+":80", focus)
		}
	}
	if sum.BudgetKrw != nil {
		cross("", sum.TotalKrw, *sum.BudgetKrw)
	}
	for _, c := range sum.Categories {
		if c.BudgetKrw != nil {
			cross(c.Name, c.TotalKrw, *c.BudgetKrw)
		}
	}

	// What today came to, between eight and nine so it rides tonight's digest.
	if now.Hour() == 20 {
		today := now.Format("2006-01-02")
		var spent int64
		var count int
		for _, d := range sum.Daily {
			if d.Date == today {
				spent, count = d.TotalKrw, d.Count
			}
		}
		body := "이번 달 " + won(sum.TotalKrw)
		if sum.BudgetKrw != nil && *sum.BudgetKrw > 0 {
			body += fmt.Sprintf(" (예산 %d%%)", sum.TotalKrw*100 / *sum.BudgetKrw)
		}
		title := fmt.Sprintf("오늘 %s 썼어요 (%d건)", won(spent), count)
		if count == 0 {
			title = "오늘은 쓴 돈이 없어요"
		}
		add("spending.daily", "digest", "info", title, body, "spending:daily:"+today, "day:"+today)
	}
	return out
}

func won(v int64) string {
	s := fmt.Sprint(v)
	if v < 0 {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if v < 0 {
		return "−₩" + s
	}
	return "₩" + s
}
