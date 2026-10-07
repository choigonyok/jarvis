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
