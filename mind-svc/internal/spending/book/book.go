// Package book turns stored charges into what the screen and the agent read:
// a month's total, where it went, how it is pacing, and what repeats.
//
// Everything here is computed, nothing stored. The charges are the only
// numbers in the database, so there is no second total that can disagree
// with them.
package book

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/spending/category"
)

// Tx is one charge as stored.
type Tx struct {
	ID             int64     `json:"id"`
	Source         string    `json:"source"` // kakao | manual
	Issuer         string    `json:"issuer"`
	CardTail       string    `json:"cardTail"`
	Kind           string    `json:"kind"` // approval | cancel
	AmountKrw      int64     `json:"amountKrw"`
	Currency       string    `json:"currency"`
	ForeignAmount  *float64  `json:"foreignAmount"`
	Estimated      bool      `json:"estimated"`
	Installment    int       `json:"installment"`
	Merchant       string    `json:"merchant"`
	Category       string    `json:"category"`
	CategorySource string    `json:"categorySource"`
	ApprovedAt     time.Time `json:"approvedAt"`
	Memo           string    `json:"memo"`
	Excluded       bool      `json:"excluded"`
	// CancelledBy is set on an approval whose full cancellation arrived.
	CancelledBy *int64 `json:"cancelledBy"`
	// Matched is set on a cancellation some approval points at.
	Matched bool `json:"-"`

	// Items is what a marketplace charge turned out to contain, read from the
	// order page in the background. Empty for everything else.
	Items        []Item  `json:"items"`
	EnrichSource *string `json:"enrichSource"`
	EnrichStatus *string `json:"enrichStatus"`
	EnrichNote   string  `json:"enrichNote"`

	// Status and SignedKrw are derived: what this row does to the total.
	Status    string `json:"status"` // ok | cancelled | cancel | refund | excluded
	SignedKrw int64  `json:"signedKrw"`
}

// Settle decides what a row contributes. A cancelled approval and the
// cancellation that cancelled it both contribute nothing - the pair is shown,
// struck through, so the list still matches the alerts. A cancellation with
// no approval to pair with (a partial refund, or one whose charge predates
// collection) is money back.
func Settle(t *Tx) {
	switch {
	case t.Excluded:
		t.Status, t.SignedKrw = "excluded", 0
	case t.Kind == "cancel" && t.Matched:
		t.Status, t.SignedKrw = "cancel", 0
	case t.Kind == "cancel":
		t.Status, t.SignedKrw = "refund", -t.AmountKrw
	case t.CancelledBy != nil:
		t.Status, t.SignedKrw = "cancelled", 0
	default:
		t.Status, t.SignedKrw = "ok", t.AmountKrw
	}
}

// Item is one product in a marketplace order. AmountKrw is its share of the
// card charge; ListedKrw is the price on the order page.
type Item struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	ListedKrw      int64  `json:"listedKrw"`
	AmountKrw      int64  `json:"amountKrw"`
	Category       string `json:"category"`
	CategorySource string `json:"categorySource"`
}

type CategoryTotal struct {
	Name      string `json:"name"`
	TotalKrw  int64  `json:"totalKrw"`
	Count     int    `json:"count"`
	BudgetKrw *int64 `json:"budgetKrw"`
	// PrevKrw is the same category last month, whole month.
	PrevKrw int64 `json:"prevKrw"`
}

type Day struct {
	Date     string `json:"date"`
	TotalKrw int64  `json:"totalKrw"`
	Count    int    `json:"count"`
}

type Merchant struct {
	Name     string `json:"name"`
	TotalKrw int64  `json:"totalKrw"`
	Count    int    `json:"count"`
}

type Recurring struct {
	Merchant  string `json:"merchant"`
	Category  string `json:"category"`
	AmountKrw int64  `json:"amountKrw"`
	// Day of month it usually lands on.
	Day    int    `json:"day"`
	LastAt string `json:"lastAt"`
	NextAt string `json:"nextAt"`
	Months int    `json:"months"`
	// Run is the months in a row up to the latest.
	Run int `json:"run"`
	// BeforeKrw is the price that held before this month's changed.
	BeforeKrw int64 `json:"beforeKrw,omitempty"`
}

type Summary struct {
	Month       string `json:"month"` // YYYY-MM
	Today       string `json:"today"`
	DaysInMonth int    `json:"daysInMonth"`
	// ElapsedDays is how much of the month has happened: all of it for a past
	// month, none of it for a future one.
	ElapsedDays int   `json:"elapsedDays"`
	TotalKrw    int64 `json:"totalKrw"`
	Count       int   `json:"count"`
	Prev        struct {
		Month    string `json:"month"`
		TotalKrw int64  `json:"totalKrw"`
		// SameDayKrw is last month up to the same day and time - the only fair
		// comparison while this month is still running.
		SameDayKrw int64 `json:"sameDayKrw"`
	} `json:"prev"`
	BudgetKrw    *int64          `json:"budgetKrw"`
	Categories   []CategoryTotal `json:"categories"`
	Daily        []Day           `json:"daily"`
	TopMerchants []Merchant      `json:"topMerchants"`
	Transactions []Tx            `json:"transactions"`
}

// Summarize builds a month. txs are this month's rows, prev last month's;
// budgets maps category to amount, with "" for the whole month.
func Summarize(month time.Time, txs, prev []Tx, budgets map[string]int64, now time.Time) Summary {
	loc := month.Location()
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 1, 0)
	days := int(end.Sub(start).Hours()/24 + 0.5)

	s := Summary{
		Month:       start.Format("2006-01"),
		Today:       now.In(loc).Format("2006-01-02"),
		DaysInMonth: days,
	}
	switch {
	case !now.Before(end):
		s.ElapsedDays = days
	case now.Before(start):
		s.ElapsedDays = 0
	default:
		s.ElapsedDays = now.In(loc).Day()
	}

	if b, ok := budgets[""]; ok {
		s.BudgetKrw = &b
	}

	byCat := map[string]*CategoryTotal{}
	cat := func(name string) *CategoryTotal {
		if c, ok := byCat[name]; ok {
			return c
		}
		c := &CategoryTotal{Name: name}
		if b, ok := budgets[name]; ok {
			c.BudgetKrw = &b
		}
		byCat[name] = c
		return c
	}
	for name := range budgets {
		if name != "" {
			cat(name)
		}
	}

	daily := make([]Day, days)
	for i := range daily {
		daily[i].Date = start.AddDate(0, 0, i).Format("2006-01-02")
	}
	merchants := map[string]*Merchant{}

	s.Transactions = make([]Tx, 0, len(txs))
	for _, t := range txs {
		Settle(&t)
		s.Transactions = append(s.Transactions, t)
		if t.Status == "excluded" || t.Status == "cancelled" || t.Status == "cancel" {
			continue
		}
		s.TotalKrw += t.SignedKrw
		s.Count++
		addToCategories(t, cat)
		if i := t.ApprovedAt.In(loc).Day() - 1; i >= 0 && i < days {
			daily[i].TotalKrw += t.SignedKrw
			daily[i].Count++
		}
		key := category.Key(t.Merchant)
		m, ok := merchants[key]
		if !ok {
			m = &Merchant{Name: t.Merchant}
			merchants[key] = m
		}
		m.TotalKrw += t.SignedKrw
		m.Count++
	}
	sort.SliceStable(s.Transactions, func(i, j int) bool {
		return s.Transactions[i].ApprovedAt.After(s.Transactions[j].ApprovedAt)
	})

	// Last month to the same point. A 31st has no twin in a 30-day month, so
	// the cut is clamped to that month's end.
	prevStart := start.AddDate(0, -1, 0)
	s.Prev.Month = prevStart.Format("2006-01")
	cut := prevStart.AddDate(0, 0, s.ElapsedDays-1)
	if s.ElapsedDays > 0 && s.ElapsedDays < days {
		n := now.In(loc)
		cut = time.Date(cut.Year(), cut.Month(), cut.Day(), n.Hour(), n.Minute(), n.Second(), 0, loc)
	} else {
		cut = start
	}
	if !cut.Before(start) {
		cut = start
	}
	for _, t := range prev {
		Settle(&t)
		if t.SignedKrw == 0 {
			continue
		}
		s.Prev.TotalKrw += t.SignedKrw
		if t.ApprovedAt.Before(cut) {
			s.Prev.SameDayKrw += t.SignedKrw
		}
		if t.Status == "ok" && len(t.Items) > 0 {
			for _, it := range t.Items {
				cat(it.Category).PrevKrw += it.AmountKrw
			}
			continue
		}
		cat(t.Category).PrevKrw += t.SignedKrw
	}

	for _, c := range byCat {
		if c.TotalKrw == 0 && c.Count == 0 && c.BudgetKrw == nil {
			continue
		}
		s.Categories = append(s.Categories, *c)
	}
	sort.Slice(s.Categories, func(i, j int) bool {
		if s.Categories[i].TotalKrw != s.Categories[j].TotalKrw {
			return s.Categories[i].TotalKrw > s.Categories[j].TotalKrw
		}
		return s.Categories[i].Name < s.Categories[j].Name
	})
	if s.Categories == nil {
		s.Categories = []CategoryTotal{}
	}

	for _, m := range merchants {
		s.TopMerchants = append(s.TopMerchants, *m)
	}
	sort.Slice(s.TopMerchants, func(i, j int) bool {
		return s.TopMerchants[i].TotalKrw > s.TopMerchants[j].TotalKrw
	})
	if len(s.TopMerchants) > 5 {
		s.TopMerchants = s.TopMerchants[:5]
	}
	if s.TopMerchants == nil {
		s.TopMerchants = []Merchant{}
	}
	s.Daily = daily
	return s
}

// addToCategories counts a charge where it went. An opened-up marketplace
// order counts by its items - "쿠팡 89,000원" was protein and a frying pan, and
// putting all of it under 쇼핑 is exactly the vagueness the items fix.
func addToCategories(t Tx, cat func(string) *CategoryTotal) {
	if t.Status == "ok" && len(t.Items) > 0 {
		seen := map[string]bool{}
		for _, it := range t.Items {
			c := cat(it.Category)
			c.TotalKrw += it.AmountKrw
			if !seen[it.Category] {
				c.Count++
				seen[it.Category] = true
			}
		}
		return
	}
	c := cat(t.Category)
	c.TotalKrw += t.SignedKrw
	c.Count++
}

// FindRecurring is the ledger's transactions that charge themselves every month
// (Monthly).
func FindRecurring(txs []Tx, now time.Time) []Recurring {
	var charges []Charge
	for _, t := range txs {
		Settle(&t)
		if t.Status == "ok" {
			charges = append(charges, Charge{Merchant: t.Merchant, Category: t.Category, AmountKrw: t.AmountKrw, At: t.ApprovedAt})
		}
	}
	return Monthly(charges, now)
}

// Charge is one payment as either ledger has it: spending's transactions, or
// mind's purchases.
type Charge struct {
	Merchant  string
	Category  string
	AmountKrw int64
	At        time.Time
}

const (
	monthlyRead   = 183 * 24 * time.Hour // what is read: half a year
	monthlySeen   = 125 * 24 * time.Hour // two months of it within this, to be listed
	monthlyLapsed = 45 * 24 * time.Hour  // nothing in this long: it ended
)

// Monthly is what charges itself every month - a subscription, a bill - read
// one way for every screen and every word about it: the same merchant once a
// month in two of the last four months, the latest within 45 days, on about the
// same day (five days apart at most, or either side of a month's turn), at
// about the same amount (15%). This month's may have changed from a price that
// held (5%): Before says from what - two months alone say nothing of a change,
// and must agree. The spending screen, the evening's word on a changed bill and
// the word on one that did not come all read it here, so none of them says
// otherwise.
func Monthly(charges []Charge, now time.Time) []Recurring {
	loc := now.Location()
	groups := map[string][]Charge{}
	for _, c := range charges {
		if c.At.After(now) || now.Sub(c.At) > monthlyRead {
			continue
		}
		k := category.Key(c.Merchant)
		groups[k] = append(groups[k], c)
	}
	out := []Recurring{}
	for _, g := range groups {
		sort.Slice(g, func(i, j int) bool { return g[i].At.Before(g[j].At) })
		months, recent := map[string]bool{}, map[string]bool{}
		for _, c := range g {
			m := c.At.In(loc).Format("2006-01")
			months[m] = true
			if now.Sub(c.At) <= monthlySeen {
				recent[m] = true
			}
		}
		// Once a month: three times in one is a habit, not a bill.
		if len(months) != len(g) || len(recent) < 2 {
			continue
		}
		last := g[len(g)-1]
		if now.Sub(last.At) > monthlyLapsed {
			continue
		}
		minD, maxD := 31, 1
		for _, c := range g {
			d := c.At.In(loc).Day()
			minD, maxD = min(minD, d), max(maxD, d)
		}
		// A charge on the 30th one month and the 1st the next is the same
		// billing day across a month boundary.
		if maxD-minD > 5 && !(minD <= 3 && maxD >= 27) {
			continue
		}
		var before []int64
		for _, c := range g[:len(g)-1] {
			before = append(before, c.AmountKrw)
		}
		mid := median64(before)
		near := func(a int64, share float64) bool { return math.Abs(float64(a-mid)) <= share*float64(mid) }
		steady, held := true, true
		for _, a := range before {
			steady, held = steady && near(a, 0.15), held && near(a, 0.05)
		}
		if !steady || len(g) == 2 && !near(last.AmountKrw, 0.15) {
			continue
		}
		lastDay := last.At.In(loc)
		r := Recurring{
			Merchant:  last.Merchant,
			Category:  last.Category,
			AmountKrw: last.AmountKrw,
			Day:       lastDay.Day(),
			LastAt:    lastDay.Format("2006-01-02"),
			NextAt:    lastDay.AddDate(0, 1, 0).Format("2006-01-02"),
			Months:    len(g),
			Run:       running(months, lastDay),
		}
		if held && len(g) > 2 && !near(last.AmountKrw, 0.05) {
			r.BeforeKrw = mid
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextAt < out[j].NextAt })
	return out
}

// running is how many months in a row, up to the latest, had the charge.
func running(months map[string]bool, last time.Time) int {
	n := 0
	for m := time.Date(last.Year(), last.Month(), 1, 0, 0, 0, 0, last.Location()); months[m.Format("2006-01")]; m = m.AddDate(0, -1, 0) {
		n++
	}
	return n
}

func median64(xs []int64) int64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	if len(s) == 0 {
		return 0
	}
	return s[len(s)/2]
}
