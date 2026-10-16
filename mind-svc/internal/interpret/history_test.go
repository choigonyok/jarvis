package interpret

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// The past is read for facts only: a plan in the answer changes nothing, a
// fact without a date takes its evidence's day, and it ends nothing.
func TestHistoryIsReadForFactsOnly(t *testing.T) {
	ctx := context.Background()
	c, st, script := setup(t, `{"changes":[
		{"op":"create","kind":"plan","set":{"title":"지수와 강릉 여행","status":"confirmed","date":"2026-10-03"},"evidence":[{"line":1,"quote":"강릉 가자"}],"confidence":0.9},
		{"op":"remember","kind":"fact","set":{"subject":"지수","subjectKind":"Person","relation":"LIVES_IN","object":"부산","objectKind":"Place","text":"지수는 부산에 산다"},"evidence":[{"line":2,"quote":"부산으로 이사했어"}],"confidence":0.9,"ends":["M1"]}
	]}`)
	c.cfg.Recall = func(context.Context, string, string, []model.Message) []Memory {
		return []Memory{{ID: "edge_1", Fact: "지수는 서울에 산다"}}
	}
	say(t, st, "sim:1", "강릉 가자", false, time.Date(2026, 10, 1, 20, 0, 0, 0, seoul))
	say(t, st, "sim:2", "나 부산으로 이사했어", false, time.Date(2026, 10, 2, 9, 0, 0, 0, seoul))
	say(t, st, "sim:3", "지금 이야기", false, time.Date(2026, 10, 12, 19, 0, 0, 0, seoul))
	line := time.Date(2026, 10, 11, 21, 0, 0, 0, seoul)

	through, read, facts, err := c.ReadHistory(ctx, "r", "지수", time.Time{}, line, 40)
	if err != nil || read != 2 || facts != 1 || !through.Equal(time.Date(2026, 10, 2, 9, 0, 0, 0, seoul)) {
		t.Fatalf("선 앞의 두 건만, 사실 하나: through=%v read=%d facts=%d err=%v", through, read, facts, err)
	}
	if !strings.Contains(script.Prompts[0], "## 지난 대화 읽기") || strings.Contains(script.Prompts[0], "지금 이야기") {
		t.Fatalf("지난 대화 안내가 붙고, 선 뒤의 메시지는 없어야 합니다:\n%s", script.Prompts[0])
	}
	if plans, _ := st.Entities(ctx, "", "", false); len(plans) != 0 {
		t.Fatalf("지난 대화로 일정을 만들면 안 됩니다: %+v", plans)
	}
	fs, _ := st.Facts(ctx, 10)
	if len(fs) != 1 || fs[0].ValidAt != "2026-10-02" || len(fs[0].Ends) != 0 {
		t.Fatalf("근거의 날짜로, 아무것도 끝내지 않고: %+v", fs)
	}
	if _, read, _, _ := c.ReadHistory(ctx, "r", "지수", through, line, 40); read != 0 {
		t.Fatalf("다 읽은 뒤에는 읽을 것이 없어야 합니다: %d", read)
	}
}

// A batch is not cut between two messages of the same second: the next read
// starts after the last time read and would skip the second one.
func TestHistoryBatchKeepsTheSameSecondTogether(t *testing.T) {
	ctx := context.Background()
	c, st, _ := setup(t, `{"changes":[]}`)
	at := time.Date(2026, 10, 1, 20, 0, 0, 0, seoul)
	say(t, st, "sim:1", "하나", false, at)
	say(t, st, "sim:2", "둘", false, at.Add(time.Second))
	say(t, st, "sim:3", "셋", false, at.Add(time.Second))
	through, read, _, err := c.ReadHistory(ctx, "r", "지수", time.Time{}, time.Time{}, 2)
	if err != nil || read != 1 || !through.Equal(at) {
		t.Fatalf("같은 초의 둘은 다음 읽기로: read=%d through=%v err=%v", read, through, err)
	}
	if _, read, _, _ = c.ReadHistory(ctx, "r", "지수", through, time.Time{}, 2); read != 2 {
		t.Fatalf("같은 초의 둘을 함께 읽어야 합니다: %d", read)
	}
}
