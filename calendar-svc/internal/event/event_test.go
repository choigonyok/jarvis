package event

import "testing"

// Saving keeps no place apart from the memo: whoever hands one over, it goes
// on the memo's first line, once.
func TestPlaceGoesInTheMemo(t *testing.T) {
	for _, c := range []struct{ place, memo, want string }{
		{"연습실", "", "연습실"},
		{"연습실", "7시 반까지", "연습실\n7시 반까지"},
		{" 연습실 ", " 7시 반까지 ", "연습실\n7시 반까지"},
		{"연습실", "연습실 앞에서 7시 반", "연습실 앞에서 7시 반"},
		{"", "7시 반까지", "7시 반까지"},
		{"", "", ""},
	} {
		e := Event{Date: "2026-11-02", Title: "합주", Place: c.place, Memo: c.memo}
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
		if e.Place != "" || e.Memo != c.want {
			t.Errorf("장소 %q 메모 %q: %+v", c.place, c.memo, e)
		}
	}
}
