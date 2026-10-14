package world

import "strings"

// Region is a place name brought to one form so places from different
// sources can be compared: Immich names cities in English ("Gapyeong",
// "Mapo-gu"), cards and conversations in Korean ("가평", "망원점"). The
// suffixes that say what kind of region it is (-si, 시, 군, 구, 동, 점) go.
func Region(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if k, ok := romanized[strings.ToLower(strings.SplitN(s, ",", 2)[0])]; ok {
		s = k
	}
	s = strings.ToLower(strings.Join(strings.Fields(s), ""))
	for _, suf := range []string{"-si", "-gun", "-gu", "-dong", "-eup", "-myeon"} {
		s = strings.TrimSuffix(s, suf)
	}
	for _, suf := range []string{"특별시", "광역시", "시", "군", "구", "동", "읍", "면", "점", "역"} {
		if r := []rune(s); len(r) > 2 || (len(r) == 2 && suf != "시" && suf != "구") {
			s = strings.TrimSuffix(s, suf)
		}
	}
	return s
}

// romanized is Immich's English names for the regions most photos are taken
// in, by their Korean names.
var romanized = map[string]string{
	"seoul": "서울", "busan": "부산", "incheon": "인천", "daegu": "대구", "daejeon": "대전", "gwangju": "광주", "ulsan": "울산", "sejong": "세종",
	"suwon": "수원", "seongnam": "성남", "seongnam-si": "성남", "goyang": "고양", "goyang-si": "고양", "yongin": "용인", "yongin-si": "용인",
	"bucheon": "부천", "bucheon-si": "부천", "ansan": "안산", "anyang": "안양", "namyangju": "남양주", "hwaseong": "화성", "hwaseong-si": "화성",
	"pyeongtaek": "평택", "uijeongbu": "의정부", "siheung": "시흥", "paju": "파주", "gimpo": "김포", "gwangmyeong": "광명", "hanam": "하남",
	"gunpo": "군포", "osan": "오산", "icheon": "이천", "anseong": "안성", "guri": "구리", "pocheon": "포천", "yangju": "양주",
	"gapyeong": "가평", "gapyeong-gun": "가평", "yangpyeong": "양평", "yangpyeong-gun": "양평", "yeoju": "여주", "dongducheon": "동두천",
	"gwacheon": "과천", "uiwang": "의왕", "chuncheon": "춘천", "gangneung": "강릉", "sokcho": "속초", "wonju": "원주", "donghae": "동해",
	"samcheok": "삼척", "hongcheon": "홍천", "pyeongchang": "평창", "jeongseon": "정선", "yangyang": "양양", "goseong": "고성", "inje": "인제",
	"jeju": "제주", "jeju-si": "제주", "seogwipo": "서귀포", "jeonju": "전주", "yeosu": "여수", "suncheon": "순천", "mokpo": "목포",
	"gyeongju": "경주", "pohang": "포항", "andong": "안동", "tongyeong": "통영", "geoje": "거제", "changwon": "창원", "gimhae": "김해",
	"cheonan": "천안", "asan": "아산", "cheongju": "청주", "chungju": "충주", "taean": "태안", "boryeong": "보령", "danyang": "단양",
	"jongno-gu": "종로", "jung-gu": "중구", "yongsan-gu": "용산", "seongdong-gu": "성동", "gwangjin-gu": "광진", "dongdaemun-gu": "동대문",
	"jungnang-gu": "중랑", "seongbuk-gu": "성북", "gangbuk-gu": "강북", "dobong-gu": "도봉", "nowon-gu": "노원", "eunpyeong-gu": "은평",
	"seodaemun-gu": "서대문", "mapo-gu": "마포", "yangcheon-gu": "양천", "gangseo-gu": "강서", "guro-gu": "구로", "geumcheon-gu": "금천",
	"yeongdeungpo-gu": "영등포", "dongjak-gu": "동작", "gwanak-gu": "관악", "seocho-gu": "서초", "gangnam-gu": "강남", "songpa-gu": "송파",
	"gangdong-gu": "강동",
}
