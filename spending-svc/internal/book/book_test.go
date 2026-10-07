package book

import (
	"testing"
	"time"
)

var kst = time.FixedZone("KST", 9*3600)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, kst)
	if err != nil {
		panic(err)
	}
	return t
}

func tx(id int64, when string, amount int64, merchant, cat string) Tx {
	return Tx{ID: id, Kind: "approval", AmountKrw: amount, Merchant: merchant, Category: cat, ApprovedAt: at(when)}
}

func TestSummarizeSettlesCancelsAndRefunds(t *testing.T) {
	cancelID := int64(3)
	paid := tx(1, "2026-10-02 12:00", 12000, "스타벅스", "카페·간식")
	cancelled := tx(2, "2026-10-03 12:00", 50000, "무신사", "쇼핑")
	cancelled.CancelledBy = &cancelID
	cancel := tx(3, "2026-10-04 09:00", 50000, "무신사", "쇼핑")
	cancel.Kind, cancel.Matched = "cancel", true
	refund := tx(4, "2026-10-05 10:00", 3000, "쿠팡", "쇼핑")
	refund.Kind = "cancel"
	excluded := tx(5, "2026-10-05 11:00", 99000, "회사 회식", "식비")
	excluded.Excluded = true

	s := Summarize(at("2026-10-01 00:00"), []Tx{paid, cancelled, cancel, refund, excluded}, nil,
		map[string]int64{"": 1_000_000, "식비": 300_000}, at("2026-10-06 12:00"))

	if s.TotalKrw != 12000-3000 {
		t.Fatalf("합계 %d, want %d", s.TotalKrw, 9000)
	}
	if len(s.Transactions) != 5 || s.Transactions[0].ID != 5 {
		t.Fatalf("내역은 전부, 최신순이어야 합니다: %+v", s.Transactions)
	}
	if s.ElapsedDays != 6 || s.DaysInMonth != 31 {
		t.Fatalf("elapsed %d / %d", s.ElapsedDays, s.DaysInMonth)
	}
	if s.BudgetKrw == nil || *s.BudgetKrw != 1_000_000 {
		t.Fatalf("전체 예산이 없습니다")
	}
	// 쓴 적 없어도 예산이 걸린 분류는 보인다 - "식비 예산이 아직 그대로"도 답이다.
	var food *CategoryTotal
	for i := range s.Categories {
		if s.Categories[i].Name == "식비" {
			food = &s.Categories[i]
		}
	}
	if food == nil || food.TotalKrw != 0 || *food.BudgetKrw != 300_000 {
		t.Fatalf("식비: %+v", food)
	}
	if s.Daily[4].TotalKrw != -3000 {
		t.Fatalf("5일 = %d", s.Daily[4].TotalKrw)
	}
}

// While a month is running, it is compared with last month up to the same
// moment, not with last month's whole total.
func TestSameDayComparison(t *testing.T) {
	prev := []Tx{
		tx(1, "2026-09-03 10:00", 10000, "a", "기타"),
		tx(2, "2026-09-06 11:00", 20000, "b", "기타"), // 같은 날, 지금(12:00)보다 앞
		tx(3, "2026-09-06 13:00", 40000, "c", "기타"), // 같은 날, 지금보다 뒤
		tx(4, "2026-09-20 10:00", 80000, "d", "기타"),
	}
	s := Summarize(at("2026-10-01 00:00"), nil, prev, nil, at("2026-10-06 12:00"))
	if s.Prev.SameDayKrw != 30000 || s.Prev.TotalKrw != 150000 {
		t.Fatalf("sameDay %d total %d", s.Prev.SameDayKrw, s.Prev.TotalKrw)
	}
	// 지난달 전체를 보는 달이면 같은 날짜까지가 곧 전체다.
	past := Summarize(at("2026-09-01 00:00"), nil, []Tx{tx(1, "2026-08-31 23:00", 5000, "a", "기타")}, nil, at("2026-10-06 12:00"))
	if past.ElapsedDays != 30 || past.Prev.SameDayKrw != 5000 {
		t.Fatalf("지난 달: elapsed %d sameDay %d", past.ElapsedDays, past.Prev.SameDayKrw)
	}
}

func TestFindRecurring(t *testing.T) {
	now := at("2026-10-06 12:00")
	txs := []Tx{
		tx(1, "2026-08-05 03:00", 29000, "ANTHROPIC", "구독"),
		tx(2, "2026-09-05 03:00", 29000, "ANTHROPIC", "구독"),
		tx(3, "2026-10-05 03:00", 29000, "ANTHROPIC", "구독"),
		// 한 달에 여러 번 = 습관이지 정기 결제가 아니다
		tx(4, "2026-09-01 08:00", 4500, "스타벅스", "카페·간식"),
		tx(5, "2026-09-15 08:00", 4500, "스타벅스", "카페·간식"),
		tx(6, "2026-10-01 08:00", 4500, "스타벅스", "카페·간식"),
		// 금액이 들쭉날쭉
		tx(7, "2026-09-10 08:00", 10000, "쿠팡", "쇼핑"),
		tx(8, "2026-10-10 08:00", 60000, "쿠팡", "쇼핑"),
		// 월말과 월초를 오가는 같은 결제일
		tx(9, "2026-08-31 09:00", 17000, "NETFLIX", "구독"),
		tx(10, "2026-10-01 09:00", 17000, "NETFLIX", "구독"),
		// 끊긴 구독
		tx(11, "2026-06-03 09:00", 9900, "왓챠", "구독"),
		tx(12, "2026-07-03 09:00", 9900, "왓챠", "구독"),
	}
	got := FindRecurring(txs, now)
	if len(got) != 2 || got[0].Merchant != "NETFLIX" || got[1].Merchant != "ANTHROPIC" {
		t.Fatalf("%+v", got)
	}
	if got[1].NextAt != "2026-11-05" || got[1].Months != 3 {
		t.Fatalf("%+v", got[1])
	}
}

// An opened-up order counts by its items, and the total stays the charge.
func TestItemsSplitCategories(t *testing.T) {
	order := tx(1, "2026-10-06 22:55", 8900, "쿠팡", "쇼핑")
	order.Items = []Item{
		{Name: "프로틴 바", AmountKrw: 5900, Category: "식비"},
		{Name: "수세미", AmountKrw: 3000, Category: "생활·통신"},
	}
	s := Summarize(at("2026-10-01 00:00"), []Tx{order}, nil, nil, at("2026-10-06 23:00"))
	if s.TotalKrw != 8900 {
		t.Fatalf("total %d", s.TotalKrw)
	}
	got := map[string]int64{}
	for _, c := range s.Categories {
		got[c.Name] = c.TotalKrw
	}
	if got["식비"] != 5900 || got["생활·통신"] != 3000 || got["쇼핑"] != 0 {
		t.Fatalf("%v", got)
	}
}
