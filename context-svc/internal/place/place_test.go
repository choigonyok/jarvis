package place

import (
	"slices"
	"testing"
	"time"
)

func TestActivity(t *testing.T) {
	for _, c := range []struct {
		says []string
		want string
	}{
		{[]string{"stationary", "automotive"}, "automotive"},
		{[]string{"walking"}, "walking"},
		{[]string{"stationary"}, "stationary"},
		{[]string{"unknown"}, ""},
		{nil, ""},
	} {
		if got := Activity(c.says); got != c.want {
			t.Errorf("%v → %q, 기대 %q", c.says, got, c.want)
		}
	}
}

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

func TestKindOf(t *testing.T) {
	for name, want := range map[string]string{"집": "home", "우리 집": "home", "지수네 집": "other", "회사": "work", "판교 사무실": "work", "솔숲캠핑장": "camp", "이마트": "shop"} {
		if got := KindOf(name); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", name, got, want)
		}
	}
}

// The usual is one's own: the median of the days before, one reading a day;
// too few days and nothing is said.
func TestBaseline(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	day0 := time.Date(2026, 10, 1, 8, 0, 0, 0, seoul)
	var rs []Reading
	for i, v := range []float64{44, 46, 45, 47, 43, 45, 46, 99} { // 99 the same day as 46, superseded
		at := day0.AddDate(0, 0, i)
		if i == 7 {
			at = day0.AddDate(0, 0, 6).Add(time.Hour)
		}
		rs = append(rs, Reading{at, v})
	}
	if _, ok := Baseline("health.hrv", rs, seoul); ok {
		t.Fatal("엿새로는 평소를 말하지 않음")
	}
	rs = append(rs, Reading{day0.AddDate(0, 0, 8), 38})
	h, ok := Baseline("health.hrv", rs, seoul)
	if !ok || h.Baseline != 45 || h.Value != 38 || h.Days != 7 || !h.Notable() || h.Line() != "심박 변이(HRV) 38 (평소 45보다 16% 낮음)" {
		t.Fatalf("%+v %v %q", h, ok, h.Line())
	}
}

func TestWatch(t *testing.T) {
	for _, c := range []struct {
		settled  bool
		batt     int
		charging bool
		want     int
	}{
		{true, 80, false, Significant},
		{false, 80, false, Move},
		{false, 15, false, Significant},
		{false, 15, true, Move},
		{true, 5, false, Quiet},
		{false, 0, false, Move}, // 배터리를 모르면 막지 않음
	} {
		if got := Watch(c.settled, c.batt, c.charging); got != c.want {
			t.Errorf("Watch(%v, %d, %v) = %d, 기대 %d", c.settled, c.batt, c.charging, got, c.want)
		}
	}
}

func TestStill(t *testing.T) {
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	here := Spot{Lat: 37.5559, Lon: 126.9104, At: at}
	near := func(ago time.Duration) Spot { return Spot{Lat: 37.5563, Lon: 126.9108, At: at.Add(-ago)} }
	far := func(ago time.Duration) Spot { return Spot{Lat: 37.5600, Lon: 126.9200, At: at.Add(-ago)} }
	if !Still([]Spot{near(20 * time.Minute), near(5 * time.Minute), here}, here.Lat, here.Lon, at, 150, 15*time.Minute) {
		t.Fatal("20분 전부터 150m 안: 제자리")
	}
	if Still([]Spot{near(10 * time.Minute), here}, here.Lat, here.Lon, at, 150, 15*time.Minute) {
		t.Fatal("10분밖에 안 됨")
	}
	if Still([]Spot{near(30 * time.Minute), far(8 * time.Minute), here}, here.Lat, here.Lon, at, 150, 15*time.Minute) {
		t.Fatal("8분 전에 다른 곳: 막 도착함")
	}
	if !Still([]Spot{near(3 * time.Hour), here}, here.Lat, here.Lon, at, 150, 15*time.Minute) {
		t.Fatal("조용한 폰의 세 시간 전 위치도 같은 자리면 제자리")
	}
	if Still([]Spot{here}, here.Lat, here.Lon, at, 150, 15*time.Minute) {
		t.Fatal("처음 보고만으로는 모름")
	}
}
