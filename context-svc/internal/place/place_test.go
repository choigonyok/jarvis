package place

import (
	"slices"
	"testing"
)

func TestInsideAndMode(t *testing.T) {
	home := Region{Name: "집", Kind: "home", Lat: 37.5559, Lon: 126.9104, RadiusM: 120}
	work := Region{Name: "회사", Kind: "work", Lat: 37.3947, Lon: 127.1112, RadiusM: 200}
	if got := Inside([]Region{home, work}, 37.5561, 126.9106, 20); !slices.Equal(got, []string{"집"}) {
		t.Fatalf("집 안: %v", got)
	}
	if got := Inside([]Region{home, work}, 37.5700, 126.9800, 20); len(got) != 0 {
		t.Fatalf("어디에도 없음: %v", got)
	}
	if d := Meters(37.5559, 126.9104, 37.3947, 127.1112); d < 24000 || d > 27000 {
		t.Fatalf("망원↔판교 약 25km: %.0f", d)
	}
	if m := Mode(Significant, "leave", "home", 80, false); m != Move {
		t.Fatalf("집을 나서면 자세히: %d", m)
	}
	if m := Mode(Move, "enter", "shop", 80, false); m != Significant {
		t.Fatalf("어딘가 도착하면 아끼기: %d", m)
	}
	if m := Mode(Significant, "leave", "work", 15, false); m != Significant {
		t.Fatalf("배터리가 적으면 자세히 하지 않음: %d", m)
	}
	if m := Mode(Move, "", "", 5, false); m != Quiet {
		t.Fatalf("배터리가 거의 없으면 조용히: %d", m)
	}
}
