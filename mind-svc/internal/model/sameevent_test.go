package model

import "testing"

func TestSameEvent(t *testing.T) {
	plan := func(title, date, start string, people ...string) State {
		return State{Title: title, Date: date, Start: start, People: people}
	}
	trip := func(title, from, to string) State {
		return State{Title: title, Date: from, EndDate: to}
	}
	for _, c := range []struct {
		name string
		a, b State
		want bool
	}{
		// 2026-11-03: one stay, named by a conversation and by the calendar.
		{"며칠 일정의 첫날·끝날이 같음", trip("은주와 캠핑 2박3일", "2027-01-01", "2027-01-03"), trip("편월재 캠핑 2박3일 (4번 사이트)", "2027-01-01", "2027-01-03"), true},
		{"며칠 일정의 끝날이 다름", trip("은주와 캠핑", "2027-01-01", "2027-01-03"), trip("편월재 캠핑", "2027-01-01", "2027-01-02"), false},
		{"같은 날짜 겹치는 말 없음", trip("부산 출장", "2027-01-01", "2027-01-03"), trip("제주 여행", "2027-01-01", "2027-01-03"), false},
		{"대화의 합주와 캘린더의 합주", plan("은주와 합주", "2026-10-17", "15:00", "은주"), plan("기타 합주 (은주)", "2026-10-17", "15:00"), true},
		{"사람만 같음", plan("저녁", "2026-10-17", "19:00", "민수"), plan("민수 생일 파티", "2026-10-17", "19:20"), true},
		{"낱말만 같음", plan("치과 예약", "2026-10-17", "10:00"), plan("치과", "2026-10-17", "10:00"), true},
		{"영어 낱말", plan("Team sync", "2026-10-17", "10:00"), plan("weekly sync", "2026-10-17", "10:15"), true},
		{"같은 때 다른 일", plan("치과", "2026-10-17", "15:00"), plan("팀 회의", "2026-10-17", "15:00"), false},
		{"다른 날", plan("은주와 합주", "2026-10-17", "15:00", "은주"), plan("기타 합주 (은주)", "2026-10-18", "15:00"), false},
		{"한 시간 넘게 차이", plan("은주와 합주", "2026-10-17", "15:00", "은주"), plan("기타 합주 (은주)", "2026-10-17", "16:00"), false},
		{"시각 없음", plan("은주와 합주", "2026-10-17", "", "은주"), plan("기타 합주 (은주)", "2026-10-17", "15:00"), false},
		// 2026-10-26: read without a time, put on the calendar at 20:00.
		{"시각 없이 같은 제목", plan("Netflix 플랜 변경·해지", "2026-11-15", ""), plan("Netflix 플랜 변경·해지", "2026-11-15", "20:00"), true},
		{"시각 없이 띄어쓰기·기호만 다름", plan("netflix 플랜 변경/해지", "2026-11-15", ""), plan("Netflix 플랜 변경·해지", "2026-11-15", "20:00"), true},
		{"시각 없이 한쪽이 다른 쪽을 품음", plan("재택근무", "2026-10-20", ""), plan("재택", "2026-10-20", ""), true},
		{"시각 없이 다른 일", plan("치과", "2026-10-17", ""), plan("팀 회의", "2026-10-17", "15:00"), false},
		{"시각 없이 같은 제목 다른 날", plan("치과", "2026-10-17", ""), plan("치과", "2026-10-18", "15:00"), false},
		{"한 글자는 제목이 아님", plan("회", "2026-10-17", ""), plan("회식", "2026-10-17", "19:00"), false},
		{"짧은 영어 조각은 낱말이 아님", plan("PT at gym", "2026-10-17", "09:00"), plan("at office", "2026-10-17", "09:00"), false},
	} {
		if got := SameEvent(c.a, c.b); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestSameOuting(t *testing.T) {
	plan := func(title, start, end string, people ...string) State {
		return State{Title: title, Date: "2026-11-28", Start: start, End: end, People: people}
	}
	for _, c := range []struct {
		name string
		a, b State
		want bool
	}{
		{"겹치는 두 부분", plan("지수와 톤 세팅", "10:00", "14:00", "지수"), plan("지수와 점심", "11:00", "", "지수"), true},
		{"끝나고 바로 이어짐", plan("지수와 톤 세팅", "10:00", "14:00", "지수"), plan("지수와 합주", "15:00", "", "지수"), true},
		{"시각 없는 부분", plan("지수와 톤 세팅", "10:00", "", "지수"), plan("지수와 점심", "", "", "지수"), true},
		{"제목에만 있는 사람", plan("지수와 점심", "11:00", "", "지수"), plan("지수 톤 세팅", "10:00", ""), true},
		{"같은 사람도 오래 떨어지면 따로", plan("지수와 점심", "12:00", "", "지수"), plan("지수와 저녁", "19:00", "", "지수"), false},
		{"다른 사람", plan("지수와 점심", "12:00", "", "지수"), plan("민수와 커피", "12:30", "", "민수"), false},
		{"사람이 없으면 알 수 없음", plan("점심", "12:00", ""), plan("커피", "12:30", ""), false},
		{"다른 날", State{Title: "지수와 점심", Date: "2026-11-27", Start: "12:00", People: []string{"지수"}}, plan("지수와 커피", "12:30", "", "지수"), false},
	} {
		if got := SameOuting(c.a, c.b); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
		if got := SameOuting(c.b, c.a); got != c.want {
			t.Errorf("%s (뒤집어서): %v", c.name, got)
		}
	}
}
