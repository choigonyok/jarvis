package moments

import (
	"context"
	"testing"
)

// A square city, and a station by a circle: places on the map, not regions.
var (
	square  = &Area{Name: "가상시", Rings: [][][2]float64{{{128.00, 36.00}, {128.20, 36.00}, {128.20, 36.15}, {128.00, 36.15}}}}
	station = &Area{Name: "가상역", Lat: 36.5000, Lon: 128.5000, Radius: 300}
)

func (f *fixture) fix(t *testing.T, h, m int, lat, lon, acc float64) {
	t.Helper()
	f.now = *f.at(h, m)
	if err := f.s.Fixed(context.Background(), Owner, Fix{Lat: lat, Lon: lon, Acc: acc, At: f.now}); err != nil {
		t.Fatal(err)
	}
}

func TestAreaContains(t *testing.T) {
	if !square.Contains(36.10, 128.07) || square.Contains(36.20, 128.07) || square.Contains(36.10, 128.30) {
		t.Fatal("사각형 안팎")
	}
	if !station.Contains(36.5020, 128.5000) || station.Contains(36.5050, 128.5000) {
		t.Fatal("원 안팎: 220m 는 안, 550m 는 밖")
	}
	if err := (Area{Name: "x"}).valid(); err == nil {
		t.Fatal("경계도 반경도 없는 지역은 받지 않는다")
	}
}

// Without a start, an arrival is a fix inside after one outside: being inside
// already when it was set is not one, and a rough or late fix says nothing.
func TestAreaArrivalNeedsAFixOutsideFirst(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.s.Set(ctx, Reminder{Text: "빵", Place: "가상", Area: square}); err != nil {
		t.Fatal(err)
	}
	f.fix(t, 9, 10, 36.10, 128.07, 10) // inside: where it was set, not an arrival
	f.fix(t, 9, 20, 36.25, 128.07, 10) // out
	f.fix(t, 9, 30, 36.10, 128.07, 3000)
	if len(f.said) != 0 {
		t.Fatalf("먼저 안, 그다음 밖, 거친 좌표는 도착이 아니다: %+v", f.said)
	}
	late := Fix{Lat: 36.10, Lon: 128.07, Acc: 10, At: *f.at(9, 15)}
	if err := f.s.Fixed(ctx, Owner, late); err != nil || len(f.said) != 0 {
		t.Fatalf("늦게 온 좌표는 지금을 말하지 않는다: %+v %v", f.said, err)
	}
	f.fix(t, 9, 40, 36.10, 128.07, 20)
	if len(f.said) != 1 || f.said[0].SentBy != ByArrive {
		t.Fatalf("밖에서 안으로: %+v", f.said)
	}
}

// With a start, being there then counts: the last fix, if new enough, says so
// when it starts; without one, the first fix inside after it does.
func TestAreaStartFindsThemThere(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.fix(t, 9, 0, 36.10, 128.07, 10)
	if _, err := f.s.Set(ctx, Reminder{Text: "빵", Place: "가상", Area: square, After: f.at(10, 0)}); err != nil {
		t.Fatal(err)
	}
	f.now = *f.at(10, 0)
	f.tick(t)
	if len(f.said) != 1 || f.said[0].SentBy != ByThere {
		t.Fatalf("시작할 때 이미 안: %+v", f.said)
	}

	g := newFixture(t)
	if _, err := g.s.Set(ctx, Reminder{Text: "빵", Place: "가상", Area: square, After: g.at(10, 0)}); err != nil {
		t.Fatal(err)
	}
	g.now = *g.at(10, 0)
	g.tick(t)
	if len(g.said) != 0 {
		t.Fatalf("좌표가 없으면 기다린다: %+v", g.said)
	}
	g.fix(t, 10, 5, 36.10, 128.07, 10)
	if len(g.said) != 1 || g.said[0].SentBy != ByThere {
		t.Fatalf("시작 뒤 첫 안쪽 좌표에: %+v", g.said)
	}
}

// Leaving is a fix outside after one inside; a station's circle takes only a
// fix about as fine as itself.
func TestAreaLeaveAndAStation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.s.Set(ctx, Reminder{Text: "우산", Place: "가상역", Area: station, Move: Leave}); err != nil {
		t.Fatal(err)
	}
	f.fix(t, 9, 10, 36.5001, 128.5001, 400) // too rough for a 300m circle
	f.fix(t, 9, 20, 36.6000, 128.5000, 10)  // outside, never inside
	if len(f.said) != 0 {
		t.Fatalf("안에 있던 적이 없으면 나간 것도 아니다: %+v", f.said)
	}
	f.fix(t, 9, 30, 36.5001, 128.5001, 15)
	f.fix(t, 9, 40, 36.5100, 128.5000, 15)
	if len(f.said) != 1 || f.said[0].SentBy != ByLeave {
		t.Fatalf("안에서 밖으로: %+v", f.said)
	}
}

// A reminder for an area is not one for a region of the same name, and the
// line says the place as the operator did.
func TestAreaIsNoRegion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r, err := f.s.Set(ctx, Reminder{Text: "빵", Place: "가상", Area: square})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.Moved(ctx, Arrive, "가상", "", f.now); err != nil {
		t.Fatal(err)
	}
	if len(f.said) != 0 {
		t.Fatalf("같은 이름의 영역 도착은 이 리마인드의 것이 아니다: %+v", f.said)
	}
	if got := r.Line(seoul); got != "가상에 도착할 때 · 빵 (id "+r.ID+")" {
		t.Fatalf("%q", got)
	}
}
