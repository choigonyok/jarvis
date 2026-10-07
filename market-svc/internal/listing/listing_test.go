package listing

import "testing"

func TestNormalize(t *testing.T) {
	ok := New{Title: " 에어팟 프로 2 ", PriceKrw: 150000, Photos: []string{"0123456789abcdef0123456789abcdef.jpg"}}
	if err := ok.Normalize(); err != nil {
		t.Fatal(err)
	}
	if ok.Title != "에어팟 프로 2" || ok.Shipping != "included" {
		t.Fatalf("got %+v", ok)
	}
	bad := []New{
		{Title: "", PriceKrw: 1, Photos: ok.Photos},
		{Title: "a", PriceKrw: 0, Photos: ok.Photos},
		{Title: "a", PriceKrw: 1},
		{Title: "a", PriceKrw: 1, Photos: []string{"../../profile/Cookies"}},
		{Title: "a", PriceKrw: 1, Photos: ok.Photos, Shipping: "free"},
	}
	for i, n := range bad {
		if err := n.Normalize(); err == nil {
			t.Errorf("%d: want error for %+v", i, n)
		}
	}
}

func TestCheckTask(t *testing.T) {
	cases := []struct {
		status string
		posted bool
		in     TaskInput
		ok     bool
	}{
		{Active, true, TaskInput{Kind: KindPrice, PriceKrw: 9000}, true},
		{Active, true, TaskInput{Kind: KindPrice}, false},
		{Sold, true, TaskInput{Kind: KindPrice, PriceKrw: 9000}, false},
		{Active, true, TaskInput{Kind: KindStatus, ToStatus: Reserved}, true},
		{Active, true, TaskInput{Kind: KindStatus, ToStatus: Active}, false},
		{Active, true, TaskInput{Kind: KindStatus, ToStatus: Deleted}, false},
		{Reserved, true, TaskInput{Kind: KindBump}, false},
		{Active, true, TaskInput{Kind: KindBump}, true},
		{Sold, true, TaskInput{Kind: KindDelete}, true},
		{Deleted, true, TaskInput{Kind: KindDelete}, false},
		{Queued, false, TaskInput{Kind: KindDelete}, false},
		{Failed, false, TaskInput{Kind: KindPost}, true},
		{Active, true, TaskInput{Kind: KindPost}, false},
	}
	for i, c := range cases {
		err := CheckTask(c.status, c.posted, c.in)
		if (err == nil) != c.ok {
			t.Errorf("%d: %s %+v: err=%v", i, c.status, c.in, err)
		}
	}
}

func TestFromJoongna(t *testing.T) {
	for in, want := range map[string]string{
		"판매중": Active, "예약중": Reserved, "판매완료": Sold, "거래 완료": Sold, "sold": Sold, "숨김": "",
	} {
		if got := FromJoongna(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
