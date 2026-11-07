// Package goal is the conversation's goals - "매일 물 2L 마시기", "주 3회 운동",
// "하루 물 4번" - kept by mind-svc with each day's 했다 or 안 했다 (or how many
// times, of a goal of more than one a day), and shown on the 목표 tab.
// Here the conversation makes one when asked and writes down what the person
// says of a day, so that saying it is as good as tapping it. None of it takes a
// card: it changes the person's own goals, and they asked for it.
package goal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

// Name is the module.
const Name = "goal"

// Goal is one as mind answers it.
type Goal struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Line   string `json:"line"`
}

// Sender sends what would change mind through the stream (mindcmd).
type Sender interface {
	Do(ctx context.Context, method, path string, in, out any) error
}

type Module struct {
	url, token string
	http       *http.Client
	send       Sender
}

// New reads mind-svc at url with the services' token and changes it through send.
func New(url, token string, send Sender) *Module {
	return &Module{url: strings.TrimRight(url, "/"), token: token, http: &http.Client{Timeout: 15 * time.Second}, send: send}
}

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) do(ctx context.Context, method, path string, in, out any) error {
	if method != http.MethodGet && m.send != nil {
		return m.send.Do(ctx, method, path, in, out)
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.url+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	res, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("mind 에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("mind 가 %d 로 응답했습니다", res.StatusCode)
	}
	return json.Unmarshal(raw, out)
}

// one is a call that answers with a goal.
func (m *Module) one(ctx context.Context, method, path string, in any) (Goal, error) {
	var out struct {
		Goal Goal `json:"goal"`
	}
	err := m.do(ctx, method, path, in, &out)
	return out.Goal, err
}

type ListInput struct {
	All bool `json:"all,omitempty" jsonschema:"true 면 끝낸 목표까지"`
}

// Look puts the list on the board.
func (m *Module) Look(b *tool.Board) {
	tool.Add(b, "goals", "운영자가 세운 목표들: 주기, 이번 주·이번 달이 어떤지, 연속, 달성률, 오늘 기록.", func(ctx context.Context, in ListInput) (string, error) {
		path := "/api/goals"
		if in.All {
			path += "?all=1"
		}
		var out struct {
			Goals []Goal `json:"goals"`
		}
		if err := m.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return "", err
		}
		if len(out.Goals) == 0 {
			return "세운 목표가 없습니다.", nil
		}
		lines := make([]string, len(out.Goals))
		for i, g := range out.Goals {
			lines[i] = g.Line
		}
		return strings.Join(lines, "\n"), nil
	})
}

type MakeInput struct {
	Title    string `json:"title" jsonschema:"목표 이름, 짧게. 예: '물 2L 마시기', '운동'"`
	Cadence  string `json:"cadence" jsonschema:"daily(매일), days(정한 요일마다), weekly(주 n회), monthly(월 n회)"`
	Times    int    `json:"times,omitempty" jsonschema:"weekly·monthly 의 횟수"`
	PerDay   int    `json:"perDay,omitempty" jsonschema:"하루에 몇 번(daily·days 만). '하루 물 4번' → 4. 하루 한 번이면 비운다"`
	Weekdays []int  `json:"weekdays,omitempty" jsonschema:"days 의 요일. 0 일요일, 1 월요일 … 6 토요일"`
	Note     string `json:"note,omitempty" jsonschema:"왜 하는지 한 줄(선택)"`
	Start    string `json:"start,omitempty" jsonschema:"시작일 YYYY-MM-DD(선택, 기본 오늘)"`
	End      string `json:"end,omitempty" jsonschema:"끝나는 날 YYYY-MM-DD(선택)"`
	Remind   string `json:"remind,omitempty" jsonschema:"매일 물어볼 시각 HH:MM(선택). 운영자가 시각을 말했을 때만"`
}

type CheckInput struct {
	ID   string `json:"id" jsonschema:"목표 id(goal_…). 지금 상황의 '목표'나 look 의 goals 에 있다"`
	Mark string `json:"mark,omitempty" jsonschema:"done(했다), missed(안 했다), clear(기록 지우기). 하루 여러 번인 목표에서 done 은 그날 다 했다는 뜻"`
	// Of a goal of more than one a day: how many times that day, or how many more.
	Count *int   `json:"count,omitempty" jsonschema:"하루 여러 번인 목표에서 그날 모두 몇 번 했는지. '오늘 물 3번 마셨어' → 3"`
	Add   int    `json:"add,omitempty" jsonschema:"하루 여러 번인 목표에서 몇 번 더 했는지. '한 잔 더 마셨어' → 1, 잘못 셌으면 -1"`
	Day   string `json:"day,omitempty" jsonschema:"어느 날인지 YYYY-MM-DD(기본 오늘). '어제 했어'면 어제 날짜"`
	Note  string `json:"note,omitempty" jsonschema:"그날에 대한 한 줄(선택)"`
}

type ChangeInput struct {
	ID       string  `json:"id" jsonschema:"목표 id(goal_…)"`
	Title    *string `json:"title,omitempty" jsonschema:"새 이름"`
	Note     *string `json:"note,omitempty" jsonschema:"새 한 줄"`
	Cadence  *string `json:"cadence,omitempty" jsonschema:"새 주기: daily, days, weekly, monthly"`
	Times    *int    `json:"times,omitempty" jsonschema:"새 횟수"`
	PerDay   *int    `json:"perDay,omitempty" jsonschema:"하루에 몇 번. 1 이면 하루 한 번"`
	Weekdays *[]int  `json:"weekdays,omitempty" jsonschema:"새 요일. 0 일요일 … 6 토요일"`
	End      *string `json:"end,omitempty" jsonschema:"끝나는 날 YYYY-MM-DD. 빈 값이면 없앤다"`
	Remind   *string `json:"remind,omitempty" jsonschema:"물어볼 시각 HH:MM. 빈 값이면 없앤다"`
	Pause    *bool   `json:"pause,omitempty" jsonschema:"true 면 오늘부터 쉬고(쉬는 날은 세지 않는다) false 면 다시 시작"`
	Finish   *bool   `json:"finish,omitempty" jsonschema:"true 면 오늘로 끝낸다. 기록은 남는다"`
}

// Change puts making, marking and changing one on the board.
func (m *Module) Change(b *tool.Board) {
	tool.Add(b, "goal.make", "목표를 만든다 - 되풀이해서 하려는 일. 목표 탭에서 날마다 했다·안 했다로 체크하고 달성률을 본다. "+
		"한 번 하고 끝나는 일은 목표가 아니라 체크리스트나 리마인드다. 운영자가 세운 것만 만든다.",
		func(ctx context.Context, in MakeInput) (string, error) {
			g, err := m.one(ctx, http.MethodPost, "/api/goals", in)
			if err != nil {
				return "", fmt.Errorf("만들지 못했습니다: %w", err)
			}
			return "만들었습니다: " + g.Line, nil
		})
	tool.Add(b, "goal.check", "목표의 하루를 기록한다. 운영자가 했다, 안 했다고 말한 것만 적는다. 짐작해서 적지 않는다. "+
		"하루 여러 번인 목표는 한 번 했다고 하면 add 1, 몇 번 했다고 하면 count 로 적는다.",
		func(ctx context.Context, in CheckInput) (string, error) {
			body := map[string]any{"day": in.Day, "note": in.Note, "by": "chat"}
			switch {
			case in.Mark == "clear":
				body["mark"] = ""
			case in.Mark != "":
				// Missed with a count: done so many times, and that was the day.
				body["mark"] = in.Mark
				if in.Count != nil {
					body["count"] = *in.Count
				}
			case in.Count != nil:
				body["count"] = *in.Count
			case in.Add != 0:
				body["add"] = in.Add
			default:
				// Nothing said is no mark to make: told as made, it was not.
				return "", fmt.Errorf("goal.check 에 mark, count, add 중 하나가 필요합니다")
			}
			g, err := m.one(ctx, http.MethodPost, "/api/goals/"+url.PathEscape(in.ID)+"/checks", body)
			if err != nil {
				return "", fmt.Errorf("기록하지 못했습니다: %w", err)
			}
			return "기록했습니다: " + g.Line, nil
		})
	tool.Add(b, "goal.change", "목표를 고친다: 이름, 주기, 끝나는 날, 물어볼 시각. pause 로 쉬거나 다시 시작하고, finish 로 끝낸다.",
		func(ctx context.Context, in ChangeInput) (string, error) {
			g, err := m.one(ctx, http.MethodPatch, "/api/goals/"+url.PathEscape(in.ID), in)
			if err != nil {
				return "", fmt.Errorf("고치지 못했습니다: %w", err)
			}
			return "고쳤습니다: " + g.Line, nil
		})
}
