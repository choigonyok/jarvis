package prefs

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// One noisy campsite is the campsite's; two make an estimate; a high score
// at a known-noisy place counts against it; a rejected estimate is not
// brought back; a stated preference is confirmed at once.
func TestRules(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	p := &Prefs{St: st, Now: func() time.Time { return time.Date(2026, 10, 20, 21, 0, 0, 0, time.UTC) }}
	review := func(src, subject, score, bad string) []string {
		moved, err := p.Record(ctx, src, Review{SubjectType: "캠핑장", Subject: subject, Context: map[string]string{"with": "가족"},
			Answers: Answers{"score": score, "bad": bad}})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range moved {
			out = append(out, m.Dimension+"/"+m.Status+"/"+m.Scope)
		}
		return out
	}
	if got := review("int_1", "솔숲캠핑장", "2", "소음·매너,사이트 간격"); len(got) != 0 {
		t.Fatalf("한 곳으로는 선호가 아님: %v", got)
	}
	got := review("int_2", "강변캠핑장", "2", "소음·매너")
	if len(got) != 1 || got[0] != "소음/estimated/가족과 갈 때" {
		t.Fatalf("두 곳이면 추정, 늘 가족과였으면 범위가 붙음: %v", got)
	}
	attrs, _ := st.Attributes(ctx, "캠핑장", "솔숲캠핑장")
	if len(attrs) != 2 {
		t.Fatalf("대상의 속성: %+v", attrs)
	}
	if err := p.Decide(ctx, "소음", -1, "가족과 갈 때", "이번만 그랬어"); err != nil {
		t.Fatal(err)
	}
	if got := review("int_3", "숲속캠핑장", "1", "소음·매너"); len(got) != 0 {
		t.Fatalf("거절한 추정은 다시 나오지 않음: %v", got)
	}

	st2 := store.NewMemory()
	p2 := &Prefs{St: st2, Now: p.Now}
	_, _ = p2.Record(ctx, "a", Review{SubjectType: "식당", Subject: "A", Answers: Answers{"score": "2", "bad": "소음·매너"}})
	_, _ = p2.Record(ctx, "b", Review{SubjectType: "식당", Subject: "B", Answers: Answers{"score": "2", "bad": "소음·매너"}})
	// C is known for noise (someone noted it), and rated 5 anyway: against.
	_, _ = p2.Record(ctx, "c1", Review{SubjectType: "식당", Subject: "C", Answers: Answers{"score": "2", "bad": "소음·매너"}})
	_, _ = p2.Record(ctx, "c2", Review{SubjectType: "식당", Subject: "C", Answers: Answers{"score": "5"}})
	ps, _ := st2.Preferences(ctx)
	if len(ps) != 1 || len(ps[0].Counter) != 1 || ps[0].Confidence >= 0.75 {
		t.Fatalf("반대 근거가 신뢰도를 낮춤: %+v", ps)
	}

	if d, pol, ok := Stated("DISLIKES", "나는 원래 시끄러운 거 싫어"); !ok || d != "소음" || pol != -1 {
		t.Fatalf("직접 한 말: %s %d %v", d, pol, ok)
	}
	if d, pol, ok := Stated("LIKES", "조용한 곳이 좋아"); !ok || d != "소음" || pol != -1 {
		t.Fatalf("조용한 곳을 좋아함 = 소음에 민감: %s %d %v", d, pol, ok)
	}
}

// The same "기타" at three places becomes a dimension, read from the
// observations as they were.
func TestPromote(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	p := &Prefs{St: st, Now: func() time.Time { return time.Date(2026, 10, 20, 21, 0, 0, 0, time.UTC) }}
	for i, place := range []string{"A", "B", "C"} {
		_ = st.AddObservations(ctx, []model.Observation{{SubjectType: "캠핑장", Subject: place, Dimension: Other, Polarity: -1,
			Detail: []string{"벌레가 많음", "벌레 많음", "벌레가 많다"}[i], Source: place}})
	}
	if ok, _ := p.Promoted(ctx, "벌레 많음"); !ok {
		t.Fatal("세 곳이면 새 차원")
	}
	ps, err := p.Recompute(ctx, Key("벌레 많음"))
	if err != nil || len(ps) != 1 || len(ps[0].Subjects) != 3 {
		t.Fatalf("승격한 차원으로 선호 추정: %+v %v", ps, err)
	}
}

// The card offers what grew out of "기타" (its words as said, its way), and
// stops offering what went unchosen for months or says nothing about the
// score; the observations stay.
func TestOptions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 20, 21, 0, 0, 0, time.UTC)
	st := store.NewMemory()
	p := &Prefs{St: st, Now: func() time.Time { return now }}
	add := func(src, subject string, at time.Time, score int, dims ...model.Observation) {
		obs := []model.Observation{{SubjectType: "캠핑장", Subject: subject, Score: score, Source: src, At: at}}
		for _, d := range dims {
			d.SubjectType, d.Subject, d.Score, d.Source, d.At = "캠핑장", subject, score, src, at
			obs = append(obs, d)
		}
		if err := st.AddObservations(ctx, obs); err != nil {
			t.Fatal(err)
		}
	}
	old := now.AddDate(0, -6, 0)
	// "벌레 많음" at three places: offered among the bad.
	for i, s := range []string{"A", "B", "C"} {
		add("o"+s, s, old, []int{3, 4, 3}[i], model.Observation{Dimension: Other, Polarity: -1, Detail: []string{"벌레 많음", "벌레가 많음", "벌레 많음"}[i]})
	}
	bad, _, retired, err := p.Options(ctx, "캠핑장")
	if err != nil || !slices.Contains(bad, "벌레 많음") || len(retired) != 0 {
		t.Fatalf("기타에서 자란 차원: %v %v %v", bad, retired, err)
	}
	// Ten more reviews in the last months: 접근성 never chosen since the
	// start; 청결 chosen at both high and low scores alike.
	for i := range 10 {
		score := 2 + 3*(i%2) // 2, 5, 2, 5 …
		add(fmt.Sprint("r", i), fmt.Sprint("R", i), now.AddDate(0, 0, -10*i), score,
			model.Observation{Dimension: "청결", Polarity: -1}, model.Observation{Dimension: "소음", Polarity: -1 + 2*(i%2)})
	}
	bad, good, retired, err := p.Options(ctx, "캠핑장")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(bad, "접근성") || slices.Contains(good, "가까움") || retired["접근성"] == "" {
		t.Errorf("넉 달 동안 안 고른 차원은 정리: %v %v %v", bad, good, retired)
	}
	if slices.Contains(bad, "청결") || retired["청결"] == "" {
		t.Errorf("점수와 상관없는 차원은 정리: %v %v", bad, retired)
	}
	if !slices.Contains(bad, "소음·매너") {
		t.Errorf("점수와 함께 움직이는 소음은 남음: %v %v", bad, retired)
	}
	if obs, _ := st.ObservationsOn(ctx, "청결"); len(obs) != 10 {
		t.Errorf("관찰은 지우지 않음: %d", len(obs))
	}
	// Choosing a grown dimension records it under its words.
	if _, err := p.Record(ctx, "x", Review{SubjectType: "캠핑장", Subject: "D", Answers: Answers{"score": "1", "bad": "벌레 많음"}}); err != nil {
		t.Fatal(err)
	}
	if obs, _ := st.ObservationsOn(ctx, "벌레 많음"); len(obs) != 1 || obs[0].Polarity != -1 {
		t.Errorf("자란 차원으로 기록: %+v", obs)
	}
}

// A purchase, a workout, a week: answers kept under their own names, no
// preference about places.
func TestOthers(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	p := &Prefs{St: st, Now: func() time.Time { return time.Date(2026, 10, 20, 21, 0, 0, 0, time.UTC) }}
	moved, err := p.Record(ctx, "w", Review{SubjectType: "물건", Subject: "캠핑 의자", Answers: Answers{"score": "2", "again": "안 살래요", "note": "허리가 아픔"}})
	if err != nil || len(moved) != 0 {
		t.Fatalf("%v %v", moved, err)
	}
	attrs, _ := st.Attributes(ctx, "물건", "캠핑 의자")
	if len(attrs) != 1 || attrs[0].Dimension != "재구매" || attrs[0].Value >= 0 {
		t.Fatalf("다시 안 산다: %+v", attrs)
	}
	obs := Others(model.Observation{SubjectType: "주", Subject: "2026-W42"}, Answers{"score": "4", "good": "캠핑", "bad": "야근", "next": "운동 세 번"})
	if len(obs) != 4 || obs[3].Dimension != "다음 주" || obs[3].Detail != "운동 세 번" {
		t.Fatalf("주간 회고: %+v", obs)
	}
}
