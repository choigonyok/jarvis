package category

import "testing"

func TestClassify(t *testing.T) {
	for merchant, want := range map[string]string{
		"스타벅스 강남역점":    "카페·간식",
		"쿠팡이츠":         "식비",
		"주식회사 우아한형제":   "식비",
		"(주)우아한형제들":    "식비",
		"쿠팡(주)":        "쇼핑",
		"KTX 서울역":      "교통",
		"KT 통신요금":      "생활·통신",
		"CU역삼점":        "편의점·마트",
		"CUCINA 이탈리안":  "기타",
		"ANTHROPIC":    "구독",
		"우리동네 김밥천국":    "식비",
		"애플스토어 가로수길":   "기타",
		"OO정형외과의원":     "의료·건강",
		"짐박스 피트니스 역삼점": "운동",
		"준오헤어 강남점":     "미용·뷰티",
		"올리브영 역삼점":     "미용·뷰티",
		"피부과의원":        "의료·건강",
	} {
		if got := Classify(merchant); got != want {
			t.Errorf("%s: %s, want %s", merchant, got, want)
		}
	}
	for _, c := range All {
		if !Valid(c) {
			t.Errorf("%s 가 Valid 가 아닙니다", c)
		}
	}
}

func TestKey(t *testing.T) {
	if Key("스타벅스 강남역점") != Key("스타벅스강남역점") {
		t.Fatal("공백 차이로 다른 가맹점이 됐습니다")
	}
}

// One list each for the delivery apps and the online stores, for every reader: a
// delivery app is not online, though 쿠팡이츠 holds 쿠팡 - its meal is eaten where
// the operator is, and a dinner at home is where it links.
func TestDeliveryIsNotOnline(t *testing.T) {
	for _, m := range []string{"쿠팡이츠", "쿠팡이츠 강남", "배달의민족", "(주)우아한형제들", "요기요"} {
		if !IsDelivery(m) || IsOnline(m) {
			t.Fatalf("%s: 배달 %v 온라인 %v", m, IsDelivery(m), IsOnline(m))
		}
	}
	for _, m := range []string{"쿠팡", "COUPANG", "네이버페이", "SSG.COM", "APPLE.COM/BILL", "Netflix.com"} {
		if !IsOnline(m) || IsDelivery(m) {
			t.Fatalf("%s: 온라인 %v", m, IsOnline(m))
		}
	}
	for _, m := range []string{"스타벅스 강남", "BSSG식당", "합정식당"} {
		if IsOnline(m) {
			t.Fatalf("%s 는 그 자리의 결제입니다", m)
		}
	}
	if Classify("쿠팡이츠") != "식비" || Classify("쿠팡") != "쇼핑" || Classify("유니클로 강남") != "쇼핑" {
		t.Fatal("분류는 그대로")
	}
}
