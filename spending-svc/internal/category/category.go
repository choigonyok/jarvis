// Package category decides what a charge was for, from the merchant name.
//
// Keywords get most charges right on day one. They will not know a local
// restaurant from a local hair salon, so the person correcting one is the
// other half: a correction can be remembered as a rule for that merchant, and
// learned rules win over keywords (see store.Classify).
package category

import (
	"strings"
	"unicode"
)

const Other = "기타"

// All is the fixed list, in the order the screen offers them. Fixed because a
// budget, a rule and a month's breakdown all key on these names, and a
// category that can be renamed is one whose history splits in two.
var All = []string{
	"식비", "카페·간식", "편의점·마트", "교통", "쇼핑", "구독",
	"생활·통신", "의료·건강", "미용·뷰티", "운동", "문화·여가", "여행", Other,
}

func Valid(name string) bool {
	for _, c := range All {
		if c == name {
			return true
		}
	}
	return false
}

// rules are checked in order, so the narrower name comes first: 쿠팡이츠
// before 쿠팡, KTX before KT.
var rules = []struct {
	category string
	words    []string
}{
	// 배민 결제는 카드사마다 "배달의민족", "우아한형제들", "주식회사 우아한형제"로 찍힌다.
	{"식비", []string{"쿠팡이츠", "배달의민족", "배민", "우아한형제", "요기요", "땡겨요"}},
	{"교통", []string{"KTX", "SRT", "코레일", "카카오T", "카카오모빌리티", "택시", "티머니", "고속버스", "시외버스",
		"주유", "SK에너지", "GS칼텍스", "S-OIL", "에쓰오일", "현대오일", "주차", "하이패스", "쏘카", "그린카", "타다", "따릉이"}},
	{"구독", []string{"NETFLIX", "넷플릭스", "YOUTUBE", "유튜브", "SPOTIFY", "멜론", "지니뮤직", "APPLE.COM", "ICLOUD",
		"디즈니", "DISNEY", "티빙", "웨이브", "왓챠", "CLAUDE", "ANTHROPIC", "OPENAI", "CHATGPT", "GITHUB", "GOOGLE ONE",
		"쿠팡와우", "네이버플러스", "밀리의서재", "리디"}},
	{"카페·간식", []string{"스타벅스", "투썸", "이디야", "메가커피", "메가MGC", "컴포즈", "빽다방", "폴바셋", "블루보틀",
		"할리스", "탐앤탐스", "파스쿠찌", "커피빈", "커피", "카페", "베이커리", "파리바게뜨", "뚜레쥬르", "배스킨", "던킨", "설빙"}},
	{"편의점·마트", []string{"GS25", "지에스25", "CU", "씨유", "세븐일레븐", "이마트24", "미니스톱", "이마트", "홈플러스",
		"롯데마트", "코스트코", "트레이더스", "하나로마트", "노브랜드", "마트"}},
	{"운동", []string{"헬스", "피트니스", "GYM", "짐", "필라테스", "요가", "클라이밍", "수영", "크로스핏", "PT"}},
	{"미용·뷰티", []string{"미용실", "헤어", "네일", "왁싱", "피부관리", "에스테틱", "올리브영", "OLIVEYOUNG",
		"아리따움", "이니스프리", "시코르", "세포라", "SEPHORA", "러쉬", "LUSH", "아모레", "화장품", "뷰티"}},
	{"의료·건강", []string{"병원", "의원", "약국", "치과", "한의원", "안과", "피부과", "정형외과", "내과", "이비인후과"}},
	{"여행", []string{"호텔", "숙박", "야놀자", "여기어때", "에어비앤비", "AIRBNB", "AGODA", "아고다", "BOOKING", "항공",
		"대한항공", "아시아나", "진에어", "제주항공", "티웨이", "에어부산", "면세"}},
	{"문화·여가", []string{"CGV", "메가박스", "롯데시네마", "영화", "노래", "코인노래", "PC방", "피씨방", "공연", "인터파크",
		"티켓링크", "예스24", "YES24", "교보문고", "알라딘", "서점", "볼링", "전시"}},
	{"생활·통신", []string{"SKT", "KT", "LGU+", "LG U+", "엘지유플러스", "알뜰폰", "통신", "관리비", "한국전력", "도시가스",
		"수도", "세탁", "다이소"}},
	{"쇼핑", []string{"쿠팡", "COUPANG", "네이버페이", "NAVERPAY", "11번가", "G마켓", "지마켓", "옥션", "무신사", "29CM",
		"SSG", "컬리", "알리익스프레스", "ALIEXPRESS", "AMAZON", "아마존", "테무", "TEMU", "유니클로", "ZARA",
		"백화점", "아울렛", "에이블리", "지그재그"}},
	{"식비", []string{"맥도날드", "버거킹", "롯데리아", "맘스터치", "KFC", "서브웨이", "김밥", "식당", "치킨", "피자", "국밥",
		"분식", "고기", "횟집", "아웃백", "한식", "중식", "일식", "초밥", "라멘", "냉면", "곱창", "족발", "보쌈", "떡볶이", "푸드"}},
}

// Classify is the keyword guess. A short ASCII word (KT, CU, PT) only counts
// as a whole token - "KT" inside "KTX" or "CU" inside "CUCINA" is not it.
func Classify(merchant string) string {
	up := strings.ToUpper(merchant)
	tokens := tokenSet(up)
	for _, r := range rules {
		for _, w := range r.words {
			if isShortASCII(w) {
				if tokens[w] {
					return r.category
				}
				continue
			}
			if strings.Contains(up, w) {
				return r.category
			}
		}
	}
	return Other
}

// Key is how one merchant is recognised across alerts: "스타벅스 강남역점" and
// "스타벅스강남역점" are the same place.
func Key(merchant string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(merchant) {
		if unicode.IsSpace(r) || r == '(' || r == ')' || r == '.' || r == '-' || r == '_' || r == '*' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isShortASCII(w string) bool {
	if len(w) > 3 {
		return false
	}
	for _, r := range w {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+')
	}) {
		out[f] = true
		// "CU역삼점" is one field; its leading ASCII run is the brand.
		i := 0
		for i < len(f) && f[i] < 0x80 {
			i++
		}
		if i > 0 && i < len(f) {
			out[f[:i]] = true
		}
	}
	return out
}
