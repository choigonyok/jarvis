package predict

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
)

// The least likely of eight looks is not as rare as its own p, and of many
// streams some stand out by chance: Benjamini-Hochberg lets stand only what
// stands out among them all.
func TestSidakAndDiscoveries(t *testing.T) {
	if got := sidak(0.01, 8); math.Abs(got-(1-math.Pow(0.99, 8))) > 1e-12 || sidak(0.01, 1) != 0.01 {
		t.Fatalf("%v", got)
	}
	if cut := discoveries([]float64{0.001, 0.02, 0.04}, 3, 0.1); cut != 0.04 {
		t.Fatalf("셋 다: %v", cut)
	}
	if cut := discoveries([]float64{0.001, 0.02, 0.04}, 100, 0.1); cut != 0.001 {
		t.Fatalf("백 개를 시험했으면 가장 드문 것만: %v", cut)
	}
	if cut := discoveries([]float64{0.05}, 100, 0.1); cut != -1 {
		t.Fatalf("아무것도: %v", cut)
	}
}

// Forty merchants bought from at random, and one every Sunday: only the
// Sunday one is a rule - and a rule already known stays known, held to its
// record and not found again among the many.
func TestADailyRuleMustStandOutAmongAllTried(t *testing.T) {
	now := at(2026, 11, 23, 3, 0) // a Monday
	p, st := engine(&now)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(7, 11))
	n := 0
	for d := at(2026, 9, 28, 0, 0); d.Before(at(2026, 11, 23, 0, 0)); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Sunday {
			n++
			_ = st.PutOccurrence(ctx, pay(fmt.Sprint("b", n), "주식회사 우아한형제", "식비", d.Add(12*time.Hour)))
		}
		for m := range 40 {
			if rng.Float64() < 0.3 {
				n++
				_ = st.PutOccurrence(ctx, pay(fmt.Sprint("r", n), fmt.Sprintf("가게%02d", m), "기타", d.Add(time.Duration(9+m%10)*time.Hour)))
			}
		}
	}
	notes, err := p.Mine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := find(notes, Daily, Delivery); !ok {
		t.Fatalf("일요일 배달은 남음: %v", texts(notes))
	}
	chance := 0
	var lucky model.Note
	for _, n := range notes {
		if n.Rule.Kind == Daily && n.Rule.Key != Delivery {
			chance++
			lucky = n
		}
	}
	if chance > 0 {
		t.Fatalf("우연히 튄 가게는 규칙이 아님: %d개 (%s)", chance, lucky.Text)
	}
	// A shop whose weekday stood out on its own, with a note already: kept.
	for _, k := range []string{"purchase/기타@가게03", "purchase/기타@가게17", "purchase/기타@가게29"} {
		_ = st.PutNote(ctx, model.Note{ID: self.ID(Daily, k), Status: model.Hypothesis, Rule: &model.Rule{Kind: Daily, Key: k}})
	}
	again, _ := p.Mine(ctx)
	for _, n := range again {
		if n.Rule.Kind == Daily && n.Rule.Key != Delivery {
			if _, err := st.Note(ctx, n.ID); err != nil {
				t.Fatalf("새로 찾은 우연이 들어옴: %s", n.Text)
			}
		}
	}
}

// A weekday's share is said by how much it differs, not only which way.
func TestDailyTextSaysHowMuch(t *testing.T) {
	fit := func(day time.Weekday, on, off float64) dailyFit {
		var f dailyFit
		f.day, f.base = day, off
		for w := range 7 {
			f.n[w], f.k[w], f.rates[w] = 10, int(math.Round(off*10)), off
			if time.Weekday(w) == day {
				f.k[w], f.rates[w] = int(math.Round(on*10)), on
			}
			f.N += f.n[w]
			f.K += f.k[w]
		}
		return f
	}
	for _, c := range []struct {
		on, off float64
		want    string
	}{
		{0.9, 0.2, "있는 편이다"},
		{0.35, 0.05, "다른 날보다 잦다"},
		{0.1, 0.9, "거의 없다"},
		{0.5, 0.95, "다른 날보다 드물다"},
	} {
		text, _ := dailyText("배달 주문", fit(time.Tuesday, c.on, c.off))
		if !strings.Contains(text, c.want) {
			t.Errorf("%.0f%% vs %.0f%%: %q", c.on*100, c.off*100, text)
		}
	}
}
