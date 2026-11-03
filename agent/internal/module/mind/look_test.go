package mind

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

// What mind knows beyond the situation reads as lines the model can answer
// from: who owes what and from which words, the plans with what became of
// their cards, the notes with how sure, what mind asked and why.
func TestTheWorldReadsAsLines(t *testing.T) {
	answers := map[string]string{
		"/api/loops": `{"loops":[{"id":"loop_1","title":"지수에게 숙소 링크 보내기","owner":"me","due":"2026-11-01","status":"open","plan":"지수와 저녁","where":"카카오톡 '지수' 대화","said":"링크 보내줄게"}]}`,
		"/api/plans": `{"from":"2026-11-02","to":"2027-01-03","plans":[{"title":"렌터카 대여","status":"confirmed","date":"2026-11-08","calendar":false,"declined":"대여는 일정이 아니야","where":"카카오톡 '지수' 대화","said":"차 빌리자"},{"title":"지수와 저녁","status":"confirmed","date":"2026-11-07","start":"19:00","calendar":true,"where":"캘린더"}]}`,
		"/api/notes": `{"notes":[{"id":"note_1","text":"캠핑 며칠 전에 장작을 산다","scope":"캠핑","status":"hypothesis","confidence":0.6,"reason":"지난 캠핑 3번 중 2번","track":{"n":4,"right":3}},{"id":"note_r","text":"렌터카 대여는 일정 카드로 올리지 않는다","scope":"규칙","status":"confirmed","confidence":1}]}`,
		"/api/asked": `{"days":7,"asked":[{"at":"2026-11-03T18:00:00+09:00","skill":"schedule","kind":"calendar.create","title":"11/8 렌터카 대여 넣을까요?","reason":"카톡에서 확정","why":"확신도 66%라 저녁 요약으로","decision":"digest","status":"rejected","evidence":["차 빌리자"],"answer":"대여는 일정이 아니야","card":true}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(answers[r.URL.Path]))
	}))
	defer srv.Close()
	look := tool.New(tool.Look, "읽는다.")
	New(Client{URL: srv.URL}).Look(look)
	ctx := context.Background()
	for _, c := range []struct {
		row  string
		want []string
		not  []string
	}{
		{"loops", []string{"[내가 할 것] 지수에게 숙소 링크 보내기", "기한 11/1(일) 지남", "'지수와 저녁' 일정", "카카오톡 '지수' 대화: \"링크 보내줄게\""}, nil},
		{"plans", []string{"11/7(토) 19:00 지수와 저녁 [확정 · 캘린더]", "11/8(일) 렌터카 대여 [확정 · 캘린더 카드 반려: 대여는 일정이 아니야]", "\"차 빌리자\""}, nil},
		{"notes", []string{"캠핑 며칠 전에 장작을 산다 [추정 60% · 캠핑] - 지난 캠핑 3번 중 2번 (예측 4번 중 3번 맞음)"}, []string{"렌터카"}},
		{"asked", []string{"[schedule · 저녁 요약 · 반려] 11/8 렌터카 대여 넣을까요?", "이유: 카톡에서 확정", "관문: 확신도 66%라 저녁 요약으로", "근거: \"차 빌리자\"", "운영자: 대여는 일정이 아니야"}, nil},
	} {
		out, err := look.Do(ctx, c.row, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.row, err)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: %q 가 없습니다:\n%s", c.row, w, out)
			}
		}
		for _, n := range c.not {
			if strings.Contains(out, n) {
				t.Errorf("%s: %q 는 없어야 합니다(규칙은 rules):\n%s", c.row, n, out)
			}
		}
	}
}
