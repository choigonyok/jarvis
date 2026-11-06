package notify

import "testing"

// A report written for the conversation, said as a notification: one line,
// no document marks, no opening name, clipped.
func TestBrief(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"고뇩님, 12개 브랜드를 모두 확인했습니다.\n\n**확인 방법**\n- 필터를 걸었습니다.\n1. 추천순", 200, "12개 브랜드를 모두 확인했습니다. · 확인 방법 · 필터를 걸었습니다. · 추천순"},
		{"## 결과\n`npm run build` 가 [문서](https://example.com)대로 됐어요", 200, "결과 · npm run build 가 문서대로 됐어요"},
		{"제목: 알람시계\n가격: 5만 원", 200, "제목: 알람시계 · 가격: 5만 원"},
		{"가나다라마바사", 3, "가나다…"},
		{"", 10, ""},
	} {
		if got := Brief(c.in, c.n); got != c.want {
			t.Errorf("Brief(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
