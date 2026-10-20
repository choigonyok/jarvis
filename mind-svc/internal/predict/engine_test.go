package predict

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

var kst = time.FixedZone("KST", 9*3600)

func at(y int, m time.Month, d, h, mi int) time.Time { return time.Date(y, m, d, h, mi, 0, 0, kst) }

func pay(id, merchant, class string, t time.Time) model.Occurrence {
	return model.Occurrence{ID: id, Kind: "purchase", At: t, Title: merchant, Class: class, Amount: 10000, Ref: id}
}

func engine(now *time.Time) (*Predictor, *store.Memory) {
	st := store.NewMemory()
	return &Predictor{St: st, Loc: kst, Now: func() time.Time { return *now }}, st
}

func find(notes []model.Note, kind, key string) (model.Note, bool) {
	for _, n := range notes {
		if n.Rule != nil && n.Rule.Kind == kind && n.Rule.Key == key {
			return n, true
		}
	}
	return model.Note{}, false
}

// Any source's occurrence is a few keys, general to particular; a delivery
// app's payment is also food delivered. The calendar is never a thing to
// predict, and the assistant's own errands are nothing at all.
func TestKeys(t *testing.T) {
	o := pay("s1", "주식회사 우아한형제", "식비", at(2026, 10, 18, 12, 0))
	if got := Keys(o, nil); !slices.Equal(got, []string{"purchase", "purchase/식비", "purchase/식비@주식회사 우아한형제", Delivery}) {
		t.Fatalf("배민 결제의 키: %v", got)
	}
	item := model.Occurrence{Kind: "purchase.item", Title: "삼다수 2L 12병", Class: "식품"}
	if got := Keys(item, func(string) string { return "생수" }); !slices.Equal(got, []string{"purchase.item", "purchase.item/생수"}) {
		t.Fatalf("상품은 무엇인지로: %v", got)
	}
	if Keys(model.Occurrence{Kind: "job", Title: "가격 조사"}, nil) != nil || Keys(model.Occurrence{Kind: "asset", Class: "보유", Title: "BTC"}, nil) != nil {
		t.Fatal("작업과 보유 상태는 일어난 일이 아닙니다")
	}
	visit := model.Occurrence{Kind: "visit", Title: "회사", Class: "work"}
	if got := Keys(visit, nil); !slices.Equal(got, []string{"visit", "visit/work", "visit/work@회사"}) {
		t.Fatalf("체류: %v", got)
	}
	for key, want := range map[string]string{Delivery: "배달 주문", "purchase/식비@주식회사 우아한형제": "우아한형제 결제", "purchase/교통": "교통 결제",
		"visit/work": "회사 도착", "visit/work@회사": "회사 도착", "purchase.item/생수": "생수 구매", "workout@하체": "하체 운동", "deposit/급여": "급여"} {
		if got := Label(key); got != want {
			t.Errorf("Label(%q) = %q, want %q", key, got, want)
		}
	}
	if Targetable("calendar/데이트") || !Targetable(Delivery) || !Targetable("visit/work@회사") {
		t.Fatal("캘린더는 다른 일이 따르는 기준일 뿐")
	}
}

// A thing that happens on Sundays far more than on other days is a daily
// rule, tried on the weeks before it was found - and the same payments
// under a wider or narrower name do not make a second rule.
func TestMineDaily(t *testing.T) {
	now := at(2026, 11, 23, 3, 0) // a Monday
	p, st := engine(&now)
	ctx := context.Background()
	n := 0
	put := func(o model.Occurrence) {
		n++
		o.ID = o.ID + "-" + string(rune('a'+n%26)) + string(rune('a'+n/26))
		o.Ref = o.ID
		_ = st.PutOccurrence(ctx, o)
	}
	for d := at(2026, 10, 5, 0, 0); d.Before(at(2026, 11, 23, 0, 0)); d = d.AddDate(0, 0, 1) {
		switch d.Weekday() {
		case time.Sunday:
			put(pay("baemin", "주식회사 우아한형제", "식비", d.Add(12*time.Hour+30*time.Minute)))
		case time.Saturday:
		default:
			put(pay("bus", "버스", "교통", d.Add(8*time.Hour+30*time.Minute)))
			if d.Weekday() == time.Wednesday {
				put(pay("kimbap", "김밥천국", "식비", d.Add(12*time.Hour)))
			}
		}
	}
	notes, err := p.Mine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		t.Logf("%s · %s · %v", n.Rule.Kind, n.Text, n.Evidence)
	}
	got, ok := find(notes, Daily, Delivery)
	if !ok {
		t.Fatalf("일요일 배달 규칙이 있어야 합니다: %v", texts(notes))
	}
	r := got.Rule
	if !strings.HasPrefix(got.Text, "일요일엔 배달 주문이 있는 편이다") || !strings.Contains(got.Text, "주로 12시 반쯤") {
		t.Fatalf("문장: %q", got.Text)
	}
	if r.Rates[0] < 0.7 || r.Rates[3] > 0.2 || r.Test == nil || r.Test.N == 0 || r.Test.Skill() <= 0 {
		t.Fatalf("일요일 높고 수요일 낮게, 지난 날로 시험한 기록과 함께: %+v %+v", r.Rates, r.Test)
	}
	if _, ok := find(notes, Daily, "purchase/식비@주식회사 우아한형제"); ok {
		t.Fatalf("같은 결제를 가게 이름으로 또 말하지 않습니다: %v", texts(notes))
	}
	if _, ok := find(notes, Daily, "purchase/교통"); ok {
		t.Fatalf("교통 결제가 모두 버스면 버스 하나로: %v", texts(notes))
	}
	if bus, ok := find(notes, Daily, "purchase/교통@버스"); !ok || !strings.HasPrefix(bus.Text, "주말엔 버스 결제가 거의 없다") {
		t.Fatalf("주말엔 버스를 안 탐: %v", texts(notes))
	}
}

func texts(notes []model.Note) []string {
	var out []string
	for _, n := range notes {
		out = append(out, n.Text)
	}
	return out
}

// What comes round every three weeks is a cycle: once its window opens it
// is expected, an item bought in the window meets it, and the prediction
// comes true when its time is up.
func TestCycle(t *testing.T) {
	now := at(2026, 11, 14, 3, 0)
	p, st := engine(&now)
	ctx := context.Background()
	_ = st.PutThingClass(ctx, "삼다수 2L 12병", "생수")
	item := func(id string, t time.Time) model.Occurrence {
		return model.Occurrence{ID: id, Kind: "purchase.item", At: t, Title: "삼다수 2L 12병", Class: "식품", Amount: 7000, Ref: id}
	}
	for i, d := range []time.Time{at(2026, 8, 3, 20, 0), at(2026, 8, 24, 20, 0), at(2026, 9, 14, 21, 0), at(2026, 10, 5, 20, 0), at(2026, 10, 26, 20, 0)} {
		_ = st.PutOccurrence(ctx, item("w"+string(rune('0'+i)), d))
	}
	notes, err := p.Mine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := find(notes, Cycle, "purchase.item/생수")
	if !ok || got.Rule.Gap != 21 || got.Rule.Tol != 1 || got.Rule.Last != "2026-10-26" || !strings.HasPrefix(got.Text, "생수 구매, 보통 21일마다") {
		t.Fatalf("3주 주기: %+v %v", got.Rule, texts(notes))
	}
	if got.Rule.Test == nil || got.Rule.Test.N != 1 || got.Rule.Test.Hits != 1 {
		t.Fatalf("네 번째 간격부터 시험: %+v", got.Rule.Test)
	}
	if err := p.Keep(ctx, notes); err != nil {
		t.Fatal(err)
	}
	kept, _ := st.Note(ctx, got.ID)
	if kept.Status != model.Candidate || kept.Rule.Since == nil {
		t.Fatalf("새 규칙은 검증 중: %+v", kept)
	}
	if prs, _ := p.Expect(ctx); len(prs) != 0 {
		t.Fatalf("창이 열리기 전엔 예측 없음: %+v", prs)
	}
	now = at(2026, 11, 15, 7, 0)
	prs, err := p.Expect(ctx)
	if err != nil || len(prs) != 1 {
		t.Fatalf("창이 열리면 예측: %+v %v", prs, err)
	}
	pr := prs[0]
	if pr.Target != "cycle:purchase.item/생수:2026-10-26" || !pr.From.Equal(at(2026, 11, 15, 0, 0)) || !pr.Due.Equal(at(2026, 11, 17, 23, 59)) || pr.Base <= 0 || pr.Base >= pr.Prob {
		t.Fatalf("예측: %+v", pr)
	}
	now = at(2026, 11, 16, 19, 0)
	o := item("w5", now)
	_ = st.PutOccurrence(ctx, o)
	if met, _ := p.Match(ctx, o); !slices.Contains(met, pr.ID) {
		t.Fatalf("창 안의 구매가 맞힘: %v", met)
	}
	now = at(2026, 11, 18, 0, 0)
	scored, _ := p.Score(ctx)
	if len(scored) != 1 || scored[0].Outcome == nil || !*scored[0].Outcome || scored[0].Surprise {
		t.Fatalf("맞은 예측: %+v", scored)
	}
	tracks, _ := p.Tracks(ctx)
	if tr := tracks[got.ID]; tr.N != 1 || tr.Hits != 1 || tr.Skill() <= 0 {
		t.Fatalf("규칙의 기록: %+v", tr)
	}
}

// One coffee after three arrivals counts for the latest only: a thing never
// stands behind every anchor before it.
func TestAfterCountsEachFollowerOnce(t *testing.T) {
	now := at(2026, 10, 20, 12, 0)
	a := &stream{key: "visit/work@회사", since: at(2026, 10, 1, 0, 0)}
	b := &stream{key: "purchase/카페·간식@스타벅스", since: at(2026, 10, 1, 0, 0)}
	for d := 1; d <= 12; d++ {
		day := at(2026, 10, d, 9, 0)
		a.events = append(a.events, event{at: day, id: "v"})
		if d <= 10 {
			b.events = append(b.events, event{at: day.Add(20 * time.Minute), id: "c"})
		}
	}
	c, ok := tryAfter(a, b, 3, now)
	if !ok || c.n != 12 || c.k != 10 || c.q > 0.2 || c.test.N == 0 {
		t.Fatalf("도착 뒤 커피: %+v %v", c, ok)
	}
	// Three arrivals each morning and one coffee after the last: it counts
	// for the last only - six of eighteen, not eighteen.
	a2 := &stream{key: a.key, since: a.since}
	b2 := &stream{key: b.key, since: b.since}
	for d := 1; d <= 6; d++ {
		for _, h := range []int{9, 10, 11} {
			a2.events = append(a2.events, event{at: at(2026, 10, d, h, 0)})
		}
		b2.events = append(b2.events, event{at: at(2026, 10, d, 11, 30)})
	}
	anchors, hit, nb, _ := followed(a2, b2, 3)
	k := 0
	for i, h := range hit {
		if h {
			k++
			if anchors[i].Hour() != 11 {
				t.Fatalf("마지막 도착에만: %v", anchors[i])
			}
		}
	}
	if len(anchors) != 18 || k != 6 || nb != 6 {
		t.Fatalf("커피 한 잔은 한 번만: %d개 중 %d개 (커피 %d)", len(anchors), k, nb)
	}
	if _, ok := tryAfter(a2, b2, 3, now); ok {
		t.Fatal("18번 중 6번은 규칙이 아님")
	}
}

// A candidate whose own predictions came true - better than the plain rate
// - is shown; one whose predictions keep missing stops; one the operator
// confirmed only shows its record.
func TestGrade(t *testing.T) {
	now := at(2026, 11, 1, 3, 0)
	p, st := engine(&now)
	ctx := context.Background()
	since := at(2026, 10, 1, 0, 0)
	rule := func(id, status string) model.Note {
		test := model.Track{}
		for range 4 {
			test = test.Add(0.8, 0.3, true)
		}
		n := model.Note{ID: id, Text: id, Status: status, Version: 1, Confidence: 0.5, CreatedAt: since, UpdatedAt: since,
			Rule: &model.Rule{Kind: Daily, Key: "delivery", Rates: make([]float64, 7), Base: 0.3, Since: &since, Test: &test}}
		_ = st.PutNote(ctx, n)
		return n
	}
	good, bad, sure := rule("good", model.Candidate), rule("bad", model.Candidate), rule("sure", model.Confirmed)
	scored := func(n model.Note, i int, prob float64, happened bool) {
		at := since.AddDate(0, 0, i+1)
		_ = st.PutPrediction(ctx, model.Prediction{ID: n.ID + string(rune('a'+i)), Target: "day:x:" + n.ID + string(rune('a'+i)), Prob: prob, Base: 0.3,
			Due: at, CreatedAt: at.Add(-time.Hour), ScoredAt: &at, Outcome: &happened, Basis: []string{n.ID}})
	}
	for i := range 3 {
		scored(good, i, 0.8, true)
	}
	for i := range 12 {
		scored(bad, i, 0.9, false)
		scored(sure, i, 0.9, false)
	}
	if err := p.Grade(ctx); err != nil {
		t.Fatal(err)
	}
	g, _ := st.Note(ctx, "good")
	if g.Status != model.Hypothesis || !strings.HasPrefix(g.Reason, "예측이 맞아 와서 보여 드림") || g.Version != 2 || g.Confidence < 0.8 {
		t.Fatalf("맞아 온 규칙은 보여 줌: %+v", g)
	}
	if !slices.ContainsFunc(g.Evidence, func(e string) bool { return strings.HasPrefix(e, "실제 예측: 예측 3번 중 3번 맞음") }) {
		t.Fatalf("근거에 기록: %v", g.Evidence)
	}
	b, _ := st.Note(ctx, "bad")
	if b.Status != model.Candidate || !b.Rule.Off || !strings.HasPrefix(b.Reason, reasonFailing) {
		t.Fatalf("계속 빗나가면 예측을 멈춤: %+v", b)
	}
	s, _ := st.Note(ctx, "sure")
	if s.Status != model.Confirmed || s.Rule.Off || s.Confidence > 0.5 {
		t.Fatalf("확정한 규칙은 기록만 보임: %+v", s)
	}
	// What stopped is not asked to predict.
	now = at(2026, 11, 2, 7, 0)
	prs, _ := p.Expect(ctx)
	for _, pr := range prs {
		if pr.Basis[0] == "bad" {
			t.Fatalf("멈춘 규칙의 예측: %+v", pr)
		}
	}
	// A rule found again after it was gone is taken up again; after it
	// kept missing, only once it has rested.
	_ = p.Keep(ctx, []model.Note{{ID: "bad", Text: "bad", Rule: &model.Rule{Kind: Daily, Key: "delivery", Rates: make([]float64, 7), Test: &model.Track{}}}})
	if b, _ := st.Note(ctx, "bad"); !b.Rule.Off {
		t.Fatalf("쉬기 전엔 그대로 멈춤: %+v", b.Rule)
	}
	now = now.Add(offRest)
	_ = p.Keep(ctx, []model.Note{{ID: "bad", Text: "bad", Rule: &model.Rule{Kind: Daily, Key: "delivery", Rates: make([]float64, 7), Test: &model.Track{}}}})
	if b, _ := st.Note(ctx, "bad"); b.Rule.Off || b.Rule.Since == nil || !b.Rule.Since.Equal(now) {
		t.Fatalf("쉰 뒤엔 새로 시험: %+v", b.Rule)
	}
}

// A prediction made at seven for the whole day is met by what happened at
// one in the morning, at once.
func TestCatchUp(t *testing.T) {
	now := at(2026, 11, 8, 7, 0)
	p, st := engine(&now)
	ctx := context.Background()
	_ = st.PutOccurrence(ctx, pay("s1", "주식회사 우아한형제", "식비", at(2026, 11, 8, 1, 0)))
	day := at(2026, 11, 8, 0, 0)
	pr, err := p.Put(ctx, model.Prediction{Target: "day:delivery:2026-11-08", Prob: 0.8, Base: 0.4, Key: Delivery, From: &day, Due: at(2026, 11, 8, 23, 59)}, Delivery)
	if err != nil {
		t.Fatal(err)
	}
	es, _ := st.Expectations(ctx, pr.ID)
	if len(es) != 1 || es[0].State != "met" || es[0].MetBy != "s1" {
		t.Fatalf("이미 있었던 일: %+v", es)
	}
	// Before its window, nothing meets it.
	early := model.Occurrence{ID: "s0", Kind: "purchase", At: at(2026, 11, 7, 23, 0), Title: "주식회사 우아한형제", Class: "식비"}
	pr2, _ := p.Put(ctx, model.Prediction{Target: "day:delivery:x", Prob: 0.8, Key: Delivery, From: &now, Due: at(2026, 11, 8, 23, 59)}, Delivery)
	if met, _ := p.Match(ctx, early); slices.Contains(met, pr2.ID) {
		t.Fatal("창 앞의 일은 맞힌 것이 아님")
	}
}

// Track: the share of calls right and how much smaller its errors were
// than the plain rate's, pulled toward nothing while there are few.
func TestTrack(t *testing.T) {
	var tr model.Track
	if tr.Skill() != 0 || tr.Accuracy() != 0.5 {
		t.Fatalf("빈 기록: %v %v", tr.Skill(), tr.Accuracy())
	}
	for range 9 {
		tr = tr.Add(0.9, 0.5, true)
	}
	tr = tr.Add(0.9, 0.5, false)
	if tr.N != 10 || tr.Hits != 9 || tr.Right != 9 || tr.Skill() < 0.4 || tr.Accuracy() < 0.8 {
		t.Fatalf("잘 맞힌 기록: %+v skill %.2f acc %.2f", tr, tr.Skill(), tr.Accuracy())
	}
	var worse model.Track
	for range 10 {
		worse = worse.Add(0.9, 0.3, false)
	}
	if worse.Skill() >= 0 {
		t.Fatalf("평소 빈도보다 못함: %.2f", worse.Skill())
	}
	if !strings.Contains(Words(tr), "예측 10번 중 9번 맞음") || !strings.Contains(Words(tr), "적음") {
		t.Fatalf("말로: %s", Words(tr))
	}
}

// An anchor starts what usually follows it: arriving at work expects the
// coffee within three hours - not the arrival itself, which is no coffee -
// and the coffee meets it.
func TestFollow(t *testing.T) {
	now := at(2026, 11, 2, 9, 0)
	p, st := engine(&now)
	ctx := context.Background()
	since := at(2026, 10, 1, 0, 0)
	_ = st.PutNote(ctx, model.Note{ID: "after1", Text: "회사 도착 뒤 3시간 안에 스타벅스 결제가 있다", Status: model.Candidate, Version: 1,
		Rule: &model.Rule{Kind: After, Anchor: "visit/work@회사", Key: "purchase/카페·간식@스타벅스", Hours: 3, Support: 9, Total: 10, Base: 0.1, Since: &since}})
	arrive := model.Occurrence{ID: "visit:1", Kind: "visit", At: now, Title: "회사", Class: "work", Ref: "visit:1"}
	_ = st.PutOccurrence(ctx, arrive)
	prs, err := p.Follow(ctx, arrive)
	if err != nil || len(prs) != 1 {
		t.Fatalf("도착 뒤 예측: %+v %v", prs, err)
	}
	pr := prs[0]
	if !pr.From.Equal(now.Add(afterGap)) || !pr.Due.Equal(now.Add(3*time.Hour)) || pr.Prob < 0.7 || pr.Base != 0.1 {
		t.Fatalf("예측: %+v", pr)
	}
	old := model.Occurrence{ID: "visit:0", Kind: "visit", At: now.Add(-5 * time.Hour), Title: "회사", Class: "work"}
	if got, _ := p.Follow(ctx, old); len(got) != 0 {
		t.Fatalf("창이 지난 도착은 예측하지 않음: %+v", got)
	}
	now = now.Add(25 * time.Minute)
	coffee := model.Occurrence{ID: "s9", Kind: "purchase", At: now, Title: "스타벅스", Class: "카페·간식", Ref: "s9"}
	if met, _ := p.Match(ctx, coffee); !slices.Contains(met, pr.ID) {
		t.Fatalf("커피가 맞힘: %v", met)
	}
}
