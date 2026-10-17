package model

import "testing"

func TestSameEvent(t *testing.T) {
	plan := func(title, date, start string, people ...string) State {
		return State{Title: title, Date: date, Start: start, People: people}
	}
	for _, c := range []struct {
		name string
		a, b State
		want bool
	}{
		{"대화의 합주와 캘린더의 합주", plan("은주와 합주", "2026-10-17", "15:00", "은주"), plan("기타 합주 (은주)", "2026-10-17", "15:00"), true},
		{"사람만 같음", plan("저녁", "2026-10-17", "19:00", "민수"), plan("민수 생일 파티", "2026-10-17", "19:20"), true},
		{"낱말만 같음", plan("치과 예약", "2026-10-17", "10:00"), plan("치과", "2026-10-17", "10:00"), true},
		{"영어 낱말", plan("Team sync", "2026-10-17", "10:00"), plan("weekly sync", "2026-10-17", "10:15"), true},
		{"같은 때 다른 일", plan("치과", "2026-10-17", "15:00"), plan("팀 회의", "2026-10-17", "15:00"), false},
		{"다른 날", plan("은주와 합주", "2026-10-17", "15:00", "은주"), plan("기타 합주 (은주)", "2026-10-18", "15:00"), false},
		{"한 시간 넘게 차이", plan("은주와 합주", "2026-10-17", "15:00", "은주"), plan("기타 합주 (은주)", "2026-10-17", "16:00"), false},
		{"시각 없음", plan("은주와 합주", "2026-10-17", "", "은주"), plan("기타 합주 (은주)", "2026-10-17", "15:00"), false},
		{"짧은 영어 조각은 낱말이 아님", plan("PT at gym", "2026-10-17", "09:00"), plan("at office", "2026-10-17", "09:00"), false},
	} {
		if got := SameEvent(c.a, c.b); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}
