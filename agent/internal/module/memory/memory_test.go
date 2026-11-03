package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// What goes in front of a message on its own is what answers it: no job's
// report, nothing that only shares a word with it, a few at most. Asked
// outright (look 의 memory), a job's record says it is one.
func TestRecallKeepsWhatAnswers(t *testing.T) {
	score := func(f float64) *float64 { return &f }
	facts := []Fact{
		{Fact: "11/1에 맡긴 작업 '예약 확인': 접수 확인 - 지금 진행 중", Relation: JobRelation, Score: score(0.86)},
		{Fact: "소나무숲 캠핑장 예약이 확정됐다", Relation: "CONFIRMED_BOOKING_FOR", Score: score(0.74)},
		{Fact: "운영자는 토요일에 캠핑을 간다", Relation: "일정", Score: score(0.61)},
		{Fact: "운영자는 커피를 좋아한다", Relation: "LIKES", Score: score(0.47)},
		{Fact: "점수 없는 사실", Relation: "KNOWS"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "12" {
			t.Errorf("limit %s", r.URL.Query().Get("limit"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"facts": facts})
	}))
	defer srv.Close()
	m := New(NewStore(srv.URL, "", "owner"))
	got := m.Recall(context.Background(), "예약 확정됐어?")
	for _, want := range []string{"소나무숲 캠핑장 예약이 확정됐다", "토요일에 캠핑", "점수 없는 사실"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q 가 없습니다:\n%s", want, got)
		}
	}
	for _, not := range []string{"맡긴 작업", "커피"} {
		if strings.Contains(got, not) {
			t.Fatalf("%q 는 빼야 합니다:\n%s", not, got)
		}
	}
	many := make([]Fact, 20)
	for i := range many {
		many[i] = Fact{Fact: "사실", Relation: "KNOWS", Score: score(0.9)}
	}
	if n := len(pick(many)); n != recallMax {
		t.Fatalf("%d개", n)
	}
	if out := render(facts[:1]); !strings.Contains(out, "작업 기록") {
		t.Fatalf("작업 기록이라고 밝혀야 합니다: %s", out)
	}
}
