package weather

import (
	"context"
	"testing"
)

func TestServiceAt(t *testing.T) {
	s := &Service{Source: Static{Days: map[string][]Day{
		"":   {{Date: "2026-10-18", Code: 0, Max: 20, Min: 10}},
		"가평": {{Date: "2026-10-18", Code: 63, Prob: 90, Precip: 20, Max: 15, Min: 9}},
	}}, Home: StaticHome}
	ctx := context.Background()
	if d, _, ok := s.At(ctx, "", "", "2026-10-18"); !ok || d.Wet() {
		t.Errorf("home %+v %v", d, ok)
	}
	if d, _, ok := s.At(ctx, "", "가평 캠핑장", "2026-10-18"); !ok || !d.Wet() {
		t.Errorf("a campsite is found by its town: %+v %v", d, ok)
	}
	if _, _, ok := s.At(ctx, "", "어딘가 캠핑장", "2026-10-18"); ok {
		t.Error("a place no map knows has no weather")
	}
	if _, _, ok := s.At(ctx, "", "가평", "2026-10-25"); ok {
		t.Error("a day the forecast does not reach has no weather")
	}
	days, err := s.HomeDays(ctx, "2026-10-01", "2026-10-31")
	if err != nil || len(days) != 1 {
		t.Errorf("HomeDays = %v %v", days, err)
	}
}
