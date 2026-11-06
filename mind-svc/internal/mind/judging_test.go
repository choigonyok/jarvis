package mind

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/checks"
	"github.com/choigonyok/jarvis/mind-svc/internal/engine"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// The night's trial of the judgement engine is a row of the status tab: a
// correction that does worse than what was said is a warning, and too few
// answers say nothing yet.
func TestHowJudgingStands(t *testing.T) {
	at := time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC)
	if v := judging(engine.Trial{}, time.Time{}); v.State != checks.Unknown {
		t.Fatalf("장부를 읽기 전: %+v", v)
	}
	rows := func(n, approved int, p float64) []model.Outcome {
		var out []model.Outcome
		for i := range n {
			result := model.ResultRejected
			if i%(n/approved) == 0 {
				result = model.ResultApproved
			}
			out = append(out, model.Outcome{ID: fmt.Sprint("o", i), Source: "card", Kind: "cart.add", Skill: "predict", Context: "prep:캠핑",
				P: p, Result: result, At: at.Add(time.Duration(i) * time.Hour)})
		}
		return out
	}
	if v := judging(engine.Walk(rows(5, 1, 0.9)), at); v.State != checks.OK || !strings.Contains(v.Summary, "5건이라 아직") {
		t.Fatalf("답이 적으면 아직: %+v", v)
	}
	// Said 90% and approved one in ten: the correction comes closer.
	v := judging(engine.Walk(rows(40, 4, 0.9)), at)
	if v.State != checks.OK || !strings.Contains(v.Summary, "작아요") || v.Figure != "40건" || len(v.Facts) != 2 {
		t.Fatalf("보정이 나으면 정상: %+v", v)
	}
	if v := judging(engine.Trial{Cases: 30, Stated: 0.1, Corrected: 0.2}, at); v.State != checks.Warn {
		t.Fatalf("보정이 더 나쁘면 주의: %+v", v)
	}
}
