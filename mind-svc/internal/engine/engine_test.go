package engine

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

var key = Key{Source: "card", Kind: "calendar.create", Skill: "schedule", Context: "calendar.create"}

func cards(n, approved int, p float64) []model.Outcome {
	var out []model.Outcome
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		result := model.ResultRejected
		if i < approved {
			result = model.ResultApproved
		}
		out = append(out, model.Outcome{ID: fmt.Sprintf("intent:%d", i), Source: "card", Kind: key.Kind, Skill: key.Skill, Context: key.Context,
			P: p, Result: result, At: at.Add(time.Duration(i) * time.Hour)})
	}
	return out
}

// The one the ledger found: says 66%, approved 21% of the time.
func TestAnOverconfidentKindIsCorrected(t *testing.T) {
	r := NewBook(cards(24, 5, 0.66)).Read(key, 0.66)
	if r.N != 24 || r.P > 0.40 || r.P < 0.20 {
		t.Fatalf("%+v", r)
	}
}

func TestAFewAnswersMoveItALittle(t *testing.T) {
	few := NewBook(cards(2, 0, 0.8)).Read(key, 0.8)
	many := NewBook(cards(30, 0, 0.8)).Read(key, 0.8)
	if few.P >= 0.8 || few.P < 0.5 {
		t.Fatalf("두 번으로 크게 움직이면 안 됩니다: %+v", few)
	}
	if many.P >= few.P {
		t.Fatalf("많을수록 더 움직입니다: %+v %+v", few, many)
	}
}

func TestAWellCalibratedKindStaysPut(t *testing.T) {
	r := NewBook(cards(20, 16, 0.8)).Read(key, 0.8)
	if math.Abs(r.P-0.8) > 0.05 {
		t.Fatalf("%+v", r)
	}
}

// A kind with no record says what it said, and nothing it pools with leaks in
// from another kind: 일정 카드의 기록이 새 종류를 끌어내리지 않는다.
func TestANewKindKeepsItsWord(t *testing.T) {
	b := NewBook(cards(24, 5, 0.66))
	other := Key{Source: "card", Kind: "assets.rebalance", Skill: "assets", Context: "assets.rebalance"}
	if r := b.Read(other, 0.8); r.P != 0.8 || r.N != 0 {
		t.Fatalf("%+v", r)
	}
	var none *Book
	if r := none.Read(key, 0.7); r.P != 0.7 || r.N != 0 {
		t.Fatalf("%+v", r)
	}
}

// Ignored is not refused, and neither it nor what never got an answer moves
// the record.
func TestOnlyAnswersCount(t *testing.T) {
	rows := []model.Outcome{
		{Source: "card", Kind: key.Kind, Skill: key.Skill, Context: key.Context, P: 0.8, Result: model.ResultIgnored},
		{Source: "card", Kind: key.Kind, Skill: key.Skill, Context: key.Context, P: 0.8, Result: model.ResultSuperseded},
		{Source: "card", Kind: key.Kind, Skill: key.Skill, Context: key.Context, P: 0.8, Result: model.ResultOpen},
		{Source: "prediction", Kind: key.Kind, Skill: key.Skill, Context: key.Context, P: 0.8, Result: model.ResultMiss},
	}
	if r := NewBook(rows).Read(key, 0.8); r.N != 0 {
		t.Fatalf("%+v", r)
	}
}

// Silence can be wrong: what was held back and done by hand lifts the kind.
func TestWhatWasMissedLiftsAKind(t *testing.T) {
	rows := cards(10, 0, 0.5)
	before := NewBook(rows).Read(key, 0.5)
	for i := 0; i < 6; i++ {
		rows = append(rows, model.Outcome{Source: "quiet", Kind: key.Kind, Skill: key.Skill, Context: key.Context, P: 0.5, Result: model.ResultDropped, Manual: true})
	}
	rows = append(rows, model.Outcome{Source: "quiet", Kind: key.Kind, Skill: key.Skill, Context: key.Context, P: 0.5, Result: model.ResultDropped})
	after := NewBook(rows).Read(key, 0.5)
	if after.P <= before.P || after.N != 16 {
		t.Fatalf("%+v → %+v", before, after)
	}
}

func TestGrades(t *testing.T) {
	rev := Spec{Reversible: true}
	cases := []struct {
		name string
		spec Spec
		r    Reading
		want Grade
	}{
		{"새 종류는 아무리 확신해도 묻는다", rev, Reading{P: 0.99, N: 0}, AskFilled},
		{"k건 전에는 승격 금지", rev, Reading{P: 0.95, N: MinCases - 1}, AskFilled},
		{"k건을 넘고 확실하면 한다", rev, Reading{P: 0.95, N: MinCases}, Act},
		{"되돌릴 수 없으면 영원히 묻는다", Spec{}, Reading{P: 0.99, N: 500}, AskFilled},
		{"상한이 있으면 넘지 않는다", Spec{Reversible: true, Cap: AskFilled}, Reading{P: 0.99, N: 500}, AskFilled},
		{"반반이면 채워서 묻는다", rev, Reading{P: 0.6, N: 20}, AskFilled},
		{"낮으면 비워서 묻는다", rev, Reading{P: 0.3, N: 20}, AskBlank},
		{"쌓여서 아주 낮으면 침묵한다", rev, Reading{P: 0.05, N: 20}, Silent},
		{"기록이 없으면 침묵하지 않는다", rev, Reading{P: 0.05, N: 2}, AskBlank},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.spec, c.r, false); got != c.want {
				t.Fatalf("%s", got)
			}
		})
	}
}

// A kind gone silent keeps a way back: one in exploreEvery is asked, the same
// ones every time, and the asking lets its record recover.
func TestSilenceKeepsAWayBack(t *testing.T) {
	if got := Decide(Spec{Reversible: true}, Reading{P: 0.05, N: 20}, true); got != AskBlank {
		t.Fatalf("%s", got)
	}
	asked := 0
	for i := 0; i < 1000; i++ {
		if Explored(fmt.Sprintf("int_%d", i)) {
			asked++
		}
	}
	if asked < 60 || asked > 140 {
		t.Fatalf("천 번에 %d번", asked)
	}
	if Explored("int_7") != Explored("int_7") {
		t.Fatal("같은 id 는 같은 답입니다")
	}
}

// The trial corrects each case only by what came before it: the first answer
// of a kind is still taken at its word.
func TestWalkDoesNotLookAhead(t *testing.T) {
	rows := cards(12, 2, 0.8)
	tr := Walk(rows)
	if tr.Cases != 12 {
		t.Fatalf("%+v", tr)
	}
	if tr.Corrected >= tr.Stated {
		t.Fatalf("계속 틀리는 확신은 보정이 나아야 합니다: %+v", tr)
	}
	one := Walk(rows[:1])
	if one.Stated != one.Corrected {
		t.Fatalf("첫 건은 앞의 기록이 없습니다: %+v", one)
	}
}

func TestWalkLeavesPredictionsOut(t *testing.T) {
	tr := Walk([]model.Outcome{{Source: "prediction", P: 0.9, Result: model.ResultMiss}, {Source: "card", P: 0.5, Result: model.ResultIgnored}})
	if tr.Cases != 0 {
		t.Fatalf("%+v", tr)
	}
}
