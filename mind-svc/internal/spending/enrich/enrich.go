// Package enrich decides which charges are worth opening up, and how the card
// amount is shared out among the items an order turns out to contain.
//
// A card alert from a marketplace says "쿠팡 8,900원" and nothing else; what
// was bought is on the marketplace's order page. The agent reads that page in
// the background and reports the items. This package is the part of that
// which needs no browser: who qualifies, and how the numbers are reconciled.
package enrich

import (
	"errors"
	"math"
	"sort"
	"strings"
)

const (
	Coupang  = "coupang"
	NaverPay = "naverpay"
)

// SourceOf says which order history a charge can be looked up in, or "".
// 쿠팡이츠 is left out: a delivery order is one meal, and 식비 already says so.
func SourceOf(merchant string) string {
	m := strings.ToUpper(strings.TrimSpace(merchant))
	switch {
	case strings.HasPrefix(m, "쿠팡이츠"), strings.HasPrefix(m, "COUPANG EATS"), strings.HasPrefix(m, "COUPANGEATS"):
		return ""
	case strings.HasPrefix(m, "쿠팡"), strings.HasPrefix(m, "COUPANG"):
		return Coupang
	// 네이버페이's history lives on the same hosts as its checkout. The
	// browser's payment guard lets exactly those history pages through
	// (browser/payment-hosts.json "view"); everything else there is still
	// asked, and refused outright while a background task holds the browser.
	case strings.HasPrefix(m, "네이버페이"), strings.HasPrefix(m, "NAVERPAY"), strings.HasPrefix(m, "NAVER PAY"),
		strings.HasPrefix(m, "네이버파이낸셜"), strings.HasPrefix(m, "(주)네이버파이낸셜"):
		return NaverPay
	}
	return ""
}

// Label is the site's name as the person knows it.
func Label(source string) string {
	switch source {
	case Coupang:
		return "쿠팡"
	case NaverPay:
		return "네이버페이"
	}
	return source
}

type Item struct {
	Name      string
	Quantity  int
	ListedKrw int64
	Category  string
	// AmountKrw is this item's share of what the card was actually charged.
	AmountKrw int64
}

// Extra is the line added when the card paid more than the items list -
// shipping, usually.
const Extra = "배송비·기타"

var ErrMismatch = errors.New("주문 금액과 카드 결제 금액의 차이가 너무 큽니다. 다른 주문일 가능성이 높으니 결제 시각과 금액이 맞는 주문을 다시 찾으세요.")

// Allocate shares the card amount out over the items.
//
// The card can pay less than the items list (a coupon, 쿠팡캐시) - then every
// item gets its proportional share, so the categories still add up to the
// charge. It can pay more (shipping) - then the items keep their prices and
// the difference becomes one more line. A charge less than a third of the
// order, or more than half again above it, is not this order.
func Allocate(cardKrw int64, items []Item, fallbackCategory string) ([]Item, error) {
	if len(items) == 0 {
		return nil, errors.New("상품이 없습니다.")
	}
	var listed int64
	for _, it := range items {
		if it.ListedKrw < 0 {
			return nil, errors.New("상품 금액이 음수입니다.")
		}
		listed += it.ListedKrw
	}
	if listed <= 0 {
		return nil, errors.New("상품 금액의 합이 0 입니다.")
	}
	if cardKrw*3 < listed || cardKrw*2 > listed*3 {
		return nil, ErrMismatch
	}

	out := make([]Item, len(items))
	copy(out, items)
	if cardKrw >= listed {
		for i := range out {
			out[i].AmountKrw = out[i].ListedKrw
		}
		if diff := cardKrw - listed; diff > 0 {
			out = append(out, Item{Name: Extra, Quantity: 1, ListedKrw: diff, AmountKrw: diff, Category: fallbackCategory})
		}
		return out, nil
	}

	// Proportional, rounded down, then the leftover won goes to the largest
	// remainders so the parts add up to the charge exactly.
	type rem struct {
		i int
		r float64
	}
	var given int64
	rems := make([]rem, len(out))
	for i := range out {
		exact := float64(cardKrw) * float64(out[i].ListedKrw) / float64(listed)
		out[i].AmountKrw = int64(math.Floor(exact))
		given += out[i].AmountKrw
		rems[i] = rem{i, exact - math.Floor(exact)}
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].r > rems[b].r })
	for k := 0; given < cardKrw; k++ {
		out[rems[k%len(rems)].i].AmountKrw++
		given++
	}
	return out, nil
}
