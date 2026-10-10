package watch

import (
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/spending-svc/internal/book"
)

func TestEvents(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	now := time.Date(2026, 10, 10, 20, 30, 0, 0, kst)
	budget, food := int64(1_000_000), int64(100_000)
	sum := book.Summary{
		Month: "2026-10", TotalKrw: 850_000, BudgetKrw: &budget,
		Categories: []book.CategoryTotal{{Name: "식비", TotalKrw: 120_000, BudgetKrw: &food}},
		Daily:      []book.Day{{Date: "2026-10-10", TotalKrw: 45_000, Count: 3}},
		Transactions: []book.Tx{
			{ID: 1, Status: "ok", AmountKrw: 150_000, Currency: "KRW", Merchant: "가구점", ApprovedAt: now.Add(-time.Hour)},
			{ID: 2, Status: "ok", AmountKrw: 30_000, Currency: "USD", Merchant: "Steam", ApprovedAt: now.Add(-2 * time.Hour)},
			{ID: 3, Status: "refund", SignedKrw: -12_000, Merchant: "쿠팡", ApprovedAt: now.Add(-3 * time.Hour)},
			{ID: 4, Status: "ok", AmountKrw: 9_000, Currency: "KRW", Merchant: "카페", ApprovedAt: now.Add(-time.Hour)},
			{ID: 5, Status: "ok", AmountKrw: 500_000, Currency: "KRW", Merchant: "지난 결제", ApprovedAt: now.Add(-100 * time.Hour)},
		},
	}
	got := map[string]string{}
	for _, e := range Events(sum, now) {
		got[e.Key] = e.Tier + " " + e.Title
	}
	want := map[string]string{
		"spending:big:1":                  "digest 큰 결제 ₩150,000",
		"spending:foreign:2":              "digest 해외 결제 ₩30,000",
		"spending:refund:3":               "digest 환불 ₩12,000",
		"spending:budget:2026-10::80":     "digest 이번 달 예산의 80%를 썼어요",
		"spending:budget:2026-10:식비:100": "now 식비 예산을 넘었어요",
		"spending:daily:2026-10-10":       "digest 오늘 ₩45,000 썼어요 (3건)",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
	for k := range got {
		if strings.Contains(k, ":4") || strings.Contains(k, ":5") {
			t.Errorf("작은 결제나 오래된 결제가 알림이 됐습니다: %s", k)
		}
	}
	if len(got) != len(want) {
		t.Errorf("알림 수: got %d want %d: %v", len(got), len(want), got)
	}
}
