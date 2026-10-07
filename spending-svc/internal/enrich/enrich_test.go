package enrich

import "testing"

func TestSourceOf(t *testing.T) {
	for merchant, want := range map[string]string{
		"쿠팡": Coupang, "쿠팡(쿠페이)": Coupang, "COUPANG": Coupang,
		"쿠팡이츠": "", "네이버페이": NaverPay, "네이버파이낸셜": NaverPay, "스타벅스": "",
	} {
		if got := SourceOf(merchant); got != want {
			t.Errorf("%s: %q, want %q", merchant, got, want)
		}
	}
}

func sum(items []Item) (n int64) {
	for _, it := range items {
		n += it.AmountKrw
	}
	return n
}

func TestAllocate(t *testing.T) {
	items := []Item{{Name: "a", ListedKrw: 6000}, {Name: "b", ListedKrw: 3000}, {Name: "c", ListedKrw: 1000}}

	// 같으면 그대로
	got, err := Allocate(10000, items, "쇼핑")
	if err != nil || len(got) != 3 || got[0].AmountKrw != 6000 {
		t.Fatalf("%v %+v", err, got)
	}
	// 쿠폰: 비율로 나누고 합은 정확히 카드 금액
	got, err = Allocate(8999, items, "쇼핑")
	if err != nil || sum(got) != 8999 || len(got) != 3 {
		t.Fatalf("쿠폰: %v %+v", err, got)
	}
	if got[0].AmountKrw < got[1].AmountKrw {
		t.Fatalf("비율이 뒤집혔습니다: %+v", got)
	}
	// 배송비: 차액이 한 줄로
	got, err = Allocate(13000, items, "쇼핑")
	if err != nil || len(got) != 4 || got[3].Name != Extra || got[3].AmountKrw != 3000 || got[3].Category != "쇼핑" {
		t.Fatalf("배송비: %v %+v", err, got)
	}
	// 다른 주문
	if _, err := Allocate(2000, items, "쇼핑"); err != ErrMismatch {
		t.Fatalf("너무 작은 결제: %v", err)
	}
	if _, err := Allocate(20000, items, "쇼핑"); err != ErrMismatch {
		t.Fatalf("너무 큰 결제: %v", err)
	}
}
