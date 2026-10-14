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
