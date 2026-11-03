package link

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// TurnedSystem is whether a no the operator gave on a calendar card speaks for
// an entry the assistant would ask for now (gate.TurnedDown).
const TurnedSystem = `운영자가 캘린더 카드를 반려하며 남긴 말 목록과, 비서가 지금 캘린더에 넣자고 카드를 올리려는 일정이 주어진다.
목록의 말 중 하나가 이런 일정은 캘린더에 넣지 말라는 뜻이면 그 번호를 고른다.
- 그 말이 가리킨 종류의 일이면 고른다. 제목이 달라도 같은 종류다: "차 예약은 일정 카드 필요없어"는 쏘카·그린카처럼 차를 빌리는 일 전부를 말한다.
- 그 카드 하나의 사정(시각이 틀림, 이미 넣음, 날짜가 바뀜)을 말한 것이면 다른 일정에는 고르지 않는다.
- 새 일정이 그 말이 가리킨 것과 다른 일이면 고르지 않는다: 차를 빌려서 가는 캠핑은 차 빌리기가 아니라 캠핑이다.
- 운영자가 방금 한 말에서 이 일정을 캘린더에 넣어 달라고 직접 말했으면 0이다. 그 말이 지금의 뜻이다.
모르겠으면 0이다.
JSON 하나만: {"turned":0}`

const turnedTimeout = 20 * time.Second

// TurnedJudge settles it. No answer is no no: the card goes up as before.
type TurnedJudge struct {
	LLM llm.Client
	Log *slog.Logger
}

// Of is the number of the no in past that speaks for ev, 0 for none.
func (j *TurnedJudge) Of(ctx context.Context, past []model.Turned, ev model.CalendarEvent, said string) int {
	if j.LLM == nil || !j.LLM.Ready() || len(past) == 0 {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, turnedTimeout)
	defer cancel()
	answer, err := j.LLM.Complete(llm.WithPurpose(ctx, llm.Judge), TurnedSystem, TurnedPrompt(past, ev, said))
	n := 0
	if err == nil {
		n, err = parseTurned(answer, len(past))
	}
	if err != nil {
		j.Log.Warn("반려한 일정과 같은 종류인지 판단하지 못했습니다", "err", err)
		return 0
	}
	return n
}

// TurnedPrompt is what the judge reads: the noes, the entry, and what the
// operator said just now.
func TurnedPrompt(past []model.Turned, ev model.CalendarEvent, said string) string {
	var b strings.Builder
	b.WriteString("반려하며 남긴 말:\n")
	for i, t := range past {
		fmt.Fprintf(&b, "%d. '%s' 카드 (%d/%d): %s\n", i+1, t.Title, int(t.At.Month()), t.At.Day(), strings.Join(strings.Fields(t.Reason), " "))
	}
	when := strings.TrimSpace(strings.Join([]string{ev.Date, dash(ev.EndDate), ev.Start, dash(ev.End)}, " "))
	fmt.Fprintf(&b, "올리려는 일정: '%s' %s", ev.Title, strings.Join(strings.Fields(when), " "))
	if memo := strings.Join(strings.Fields(ev.Memo), " "); memo != "" {
		fmt.Fprintf(&b, " 메모: %s", memo)
	}
	said = strings.Join(strings.Fields(said), " ")
	if said == "" {
		said = "없음"
	} else {
		said = "“" + said + "”"
	}
	b.WriteString("\n운영자가 방금 한 말: " + said)
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return ""
	}
	return "~" + s
}

func parseTurned(answer string, n int) (int, error) {
	start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if start < 0 || end < start {
		return 0, fmt.Errorf("판정을 읽지 못했습니다: %q", answer)
	}
	var j struct {
		Turned int `json:"turned"`
	}
	if err := json.Unmarshal([]byte(answer[start:end+1]), &j); err != nil {
		return 0, err
	}
	if j.Turned < 0 || j.Turned > n {
		return 0, fmt.Errorf("없는 번호입니다: %d", j.Turned)
	}
	return j.Turned, nil
}
