package kakao

import "testing"

func TestFullText(t *testing.T) {
	cases := []struct{ name, msg, att, want string }{
		{"no attachment", "hi", "", "hi"},
		{"alimtalk full body in TD.T", "preview", `{"P":{"ME":"preview"},"C":{"TI":{"TD":{"T":"preview and the rest"}}}}`, "preview and the rest"},
		{"short title in TD.T keeps message", "longer message body", `{"P":{"ME":"longer message body"},"C":{"TI":{"TD":{"T":"title"}}}}`, "longer message body"},
		{"bad json", "m", `{"ME":`, "m"},
	}
	for _, c := range cases {
		if got := fullText(c.msg, c.att); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
