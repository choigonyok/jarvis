package thread

import (
	"testing"
)

// stepsOf is the list a steps frame carries.
func stepsOf(t *testing.T, ev any) []Step {
	t.Helper()
	list, ok := ev.([]Step)
	if !ok {
		t.Fatalf("steps 가 목록이 아닙니다: %#v", ev)
	}
	return list
}

// Each step goes out with the whole stretch, the newest last, so a client
// that missed a frame is set right by the next one.
func TestStepSendsTheWholeStretch(t *testing.T) {
	s, events := draftStore(t)
	s.Step("일정 보기")
	s.Step("")
	s.Step("웹 검색: 양평 캠핑장")

	first := next(t, events)
	if first.Type != "steps" || len(stepsOf(t, first.Steps)) != 1 {
		t.Fatalf("첫 단계: %+v", first)
	}
	second := stepsOf(t, next(t, events).Steps)
	if len(second) != 2 || second[0].Text != "일정 보기" || second[1].Text != "웹 검색: 양평 캠핑장" || second[1].At == "" {
		t.Fatalf("두 번째 프레임(빈 줄은 건너뜀): %+v", second)
	}
	if got := s.Steps(); len(got) != 2 {
		t.Fatalf("막 연결한 화면이 받을 목록: %+v", got)
	}
}

// What the conversation says ends the stretch before it; a job's news laid
// in beside the turn does not.
func TestSayingSomethingEndsTheStretch(t *testing.T) {
	s, events := draftStore(t)
	s.Step("일정 보기")
	next(t, events)

	s.JobNews(Origin{Kind: "job", Run: 7}, []string{"생수를 장바구니에 담았어요"}, nil)
	if ev := next(t, events); ev.Type != "turn" {
		t.Fatalf("작업 소식: %+v", ev)
	}
	if got := s.Steps(); len(got) != 1 {
		t.Fatalf("작업 소식이 진행 중인 단계를 지웠습니다: %+v", got)
	}

	s.AppendAgent([]string{"토요일 일정은 없어요."})
	if ev := next(t, events); ev.Type != "steps" || len(stepsOf(t, ev.Steps)) != 0 {
		t.Fatalf("말 앞에 빈 목록이 와야 합니다: %+v", ev)
	}
	if ev := next(t, events); ev.Type != "turn" {
		t.Fatalf("그다음 말: %+v", ev)
	}
	if got := s.Steps(); len(got) != 0 {
		t.Fatalf("말한 뒤에도 단계가 남았습니다: %+v", got)
	}
}

// A run starts with nothing done and leaves nothing behind; with nothing to
// clear, nothing is sent.
func TestThinkingEndsTheStretch(t *testing.T) {
	s, events := draftStore(t)
	s.SetThinking(true)
	if ev := next(t, events); ev.Type != "status" || !ev.Thinking {
		t.Fatalf("빈 목록은 보내지 않습니다: %+v", ev)
	}
	s.Step("기억 찾기: 캠핑")
	next(t, events)
	s.SetThinking(false)
	if ev := next(t, events); ev.Type != "steps" || len(stepsOf(t, ev.Steps)) != 0 {
		t.Fatalf("끝날 때 빈 목록: %+v", ev)
	}
	if ev := next(t, events); ev.Type != "status" || ev.Thinking {
		t.Fatalf("그다음 상태: %+v", ev)
	}
}

// A long stretch keeps its end.
func TestStepsKeepTheEnd(t *testing.T) {
	s, _ := draftStore(t)
	for i := range maxSteps + 3 {
		s.Step(string(rune('가' + i)))
	}
	got := s.Steps()
	if len(got) != maxSteps || got[len(got)-1].Text != string(rune('가'+maxSteps+2)) {
		t.Fatalf("끝의 %d개: %+v", maxSteps, got)
	}
}
