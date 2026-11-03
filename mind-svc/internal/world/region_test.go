package world

import "testing"

func TestSamePlace(t *testing.T) {
	cases := []struct {
		areaA, placeA, areaB, placeB string
		want                         bool
	}{
		{"망원동", "", "", "GS25 망원점", true},
		{"가평", "", "Gapyeong-gun", "Gapyeong-gun, Gyeonggi-do", true},
		{"홍대", "CGV 홍대", "", "CGV 홍대", true},
		{"강남", "", "Gangnam-gu", "", true},
		{"서울특별시", "", "Seoul", "", true},
		{"망원동", "지수네 집", "역삼동", "스타벅스 강남역점", false},
		{"", "", "망원동", "", false},
		{"중구", "", "Jung-gu", "", true},
	}
	for _, c := range cases {
		if got := SamePlace(c.areaA, c.placeA, c.areaB, c.placeB); got != c.want {
			t.Errorf("%q/%q vs %q/%q: %v (%q %q)", c.areaA, c.placeA, c.areaB, c.placeB, got, Region(c.areaA+c.placeA), Region(c.areaB+c.placeB))
		}
	}
}

// Immich's names come with or without the suffix that says what kind of region
// they are, and both are the same place; a name many cities share (jung-gu) is
// not guessed.
func TestRomanizedWithItsSuffix(t *testing.T) {
	for in, want := range map[string]string{
		"Hongcheon-gun": "홍천", "Pocheon-si": "포천", "Yongsan-dong": "용산", "Seocho": "서초", "Seongnam": "성남",
		"Gapyeong-gun": "가평", "Bundang-gu": "분당", "Mapo-gu, Seoul": "마포", "Suwon": "수원",
	} {
		if got := Region(in); got != want {
			t.Errorf("%s: %q, %q 여야 합니다", in, got, want)
		}
	}
	if got := Region("Jung-dong"); got == "중구" {
		t.Fatal("여러 도시에 있는 이름은 짐작하지 않습니다")
	}
	if !City("Hongcheon-gun") || !City("Yongsan-dong") {
		t.Fatal("도시 단위의 이름입니다")
	}
}
