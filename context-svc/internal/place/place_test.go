package place

import (
	"slices"
	"testing"
	"time"
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

func TestTrips(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	at := func(d, hm string) time.Time {
		x, _ := time.ParseInLocation("2006-01-02 15:04", d+" "+hm, seoul)
		return x
	}
	out := func(d, hm string) *time.Time { x := at(d, hm); return &x }
	stays := []Stay{
		{Region: "회사", Area: "강남", In: at("2026-10-13", "09:00"), Out: out("2026-10-13", "18:20")},
		{Region: "집", Area: "망원동", In: at("2026-10-13", "19:05"), Out: out("2026-10-14", "08:20")},
		{Region: "회사", Area: "강남", In: at("2026-10-14", "09:10"), Out: out("2026-10-14", "18:30")},
		{Region: "강남역 헬스장", Area: "강남", In: at("2026-10-14", "18:40"), Out: out("2026-10-14", "20:00")}, // same area: no trip
		{Region: "집", Area: "망원동", In: at("2026-10-14", "20:45"), Out: out("2026-10-15", "08:15")},
		{Region: "회사", Area: "강남", In: at("2026-10-15", "09:05"), Out: out("2026-10-15", "18:00")},
		{Region: "편의점", In: at("2026-10-15", "10:00"), Out: out("2026-10-15", "10:05")}, // inside 회사's stay
		{Region: "집", Area: "망원동", In: at("2026-10-15", "23:30")},                       // 5h30 later: not a trip
	}
	trips := Trips(stays)
	got := map[string]int{}
	for _, tr := range trips {
		got[tr.From+"→"+tr.To]++
	}
	if got["강남→망원동"] != 2 || got["망원동→강남"] != 2 || len(trips) != 4 {
		t.Fatalf("trips: %+v", trips)
	}
	routes := Routes(trips, seoul)
	for _, r := range routes {
		if r.From == "강남" && (r.Minutes != 45 || r.Trips != 2) {
			t.Errorf("강남→망원동: %+v", r)
		}
		if r.From == "망원동" && (r.Minutes != 50 || r.Leave != "08:17") {
			t.Errorf("망원동→강남: %+v", r)
		}
	}
}
