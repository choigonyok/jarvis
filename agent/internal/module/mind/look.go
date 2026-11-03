package mind

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

// What mind knows that the situation in front of every turn does not say: the
// loops still open, the plans around now (the calendar's and those only said),
// the notes it holds about the operator, and what it asked and told lately with
// why. Read only; asked of mind's read API, the operator's.

type loopsIn struct {
	All bool `json:"all,omitempty" jsonschema:"true 면 2주 안에 닫힌 것도"`
}

type plansIn struct {
	From string `json:"from,omitempty" jsonschema:"YYYY-MM-DD. 비우면 어제"`
	To   string `json:"to,omitempty" jsonschema:"YYYY-MM-DD. 비우면 두 달 뒤"`
}

type notesIn struct {
	All bool `json:"all,omitempty" jsonschema:"true 면 아직 조용히 시험 중인 규칙까지"`
}

type askedIn struct {
	Days int `json:"days,omitempty" jsonschema:"며칠 치(1~30). 비우면 7"`
}

// Look puts mind's world model and its own word on the board.
func (m *Module) Look(b *tool.Board) {
	tool.Add(b, "loops", "열린 고리: 운영자가 하기로 한 것(me)과 남이 해 주기로 한 것(them). 카톡·iMessage·이 대화에서 읽은 약속·부탁을 기한이 가까운 것부터, 어느 대화의 어떤 말에서 읽었는지와 함께 준다.",
		func(ctx context.Context, in loopsIn) (string, error) {
			q := ""
			if in.All {
				q = "?all=1"
			}
			var out struct {
				Loops []loopView `json:"loops"`
			}
			if err := m.c.Do(ctx, http.MethodGet, "/api/loops"+q, nil, &out); err != nil {
				return "", err
			}
			return renderLoops(out.Loops, time.Now()), nil
		})
	tool.Add(b, "plans", "mind 가 아는 일정: 캘린더의 것과 대화에서만 정한 것(제안·확정·취소, 캘린더 카드를 반려한 것은 그 이유)을 날짜순으로, 읽은 대화와 말과 함께 준다.",
		func(ctx context.Context, in plansIn) (string, error) {
			q := url.Values{}
			if in.From != "" {
				q.Set("from", in.From)
			}
			if in.To != "" {
				q.Set("to", in.To)
			}
			path := "/api/plans"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			var out struct {
				From  string     `json:"from"`
				To    string     `json:"to"`
				Plans []planView `json:"plans"`
			}
			if err := m.c.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
				return "", err
			}
			return renderPlans(out.From, out.To, out.Plans), nil
		})
	tool.Add(b, "notes", "mind 가 운영자에 대해 세운 노트(습관·패턴의 추정과 운영자가 확인한 것)를 신뢰도와 근거, 예측이 맞은 정도와 함께 준다.",
		func(ctx context.Context, in notesIn) (string, error) {
			q := ""
			if in.All {
				q = "?all=1"
			}
			var out struct {
				Notes []noteView `json:"notes"`
			}
			if err := m.c.Do(ctx, http.MethodGet, "/api/notes"+q, nil, &out); err != nil {
				return "", err
			}
			return renderNotes(out.Notes), nil
		})
	tool.Add(b, "asked", "mind 가 최근에 묻거나 알린 것(카드·알림·저녁 요약·묻지 않고 넘긴 것)을 그 이유·근거·관문의 판단·운영자의 답과 함께 준다. '왜 그 카드 올렸어?', '어제 뭐 알려줬지?'에 답할 때.",
		func(ctx context.Context, in askedIn) (string, error) {
			path := "/api/asked"
			if in.Days > 0 {
				path += fmt.Sprintf("?days=%d", in.Days)
			}
			var out struct {
				Days  int         `json:"days"`
				Asked []askedView `json:"asked"`
			}
			if err := m.c.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
				return "", err
			}
			return renderAsked(out.Days, out.Asked), nil
		})
}

type loopView struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Owner  string    `json:"owner"`
	Due    string    `json:"due"`
	Status string    `json:"status"`
	Plan   string    `json:"plan"`
	Where  string    `json:"where"`
	Said   string    `json:"said"`
	Since  time.Time `json:"since"`
}

type planView struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Date     string   `json:"date"`
	EndDate  string   `json:"endDate"`
	Start    string   `json:"start"`
	End      string   `json:"end"`
	Place    string   `json:"place"`
	People   []string `json:"people"`
	Type     string   `json:"type"`
	Calendar bool     `json:"calendar"`
	Declined string   `json:"declined"`
	Where    string   `json:"where"`
	Said     string   `json:"said"`
}

type noteView struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	Scope      string  `json:"scope"`
	Status     string  `json:"status"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
	Track      *struct {
		N     int `json:"n"`
		Right int `json:"right"`
	} `json:"track"`
}

type askedView struct {
	ID         string    `json:"id"`
	At         time.Time `json:"at"`
	Skill      string    `json:"skill"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	Reason     string    `json:"reason"`
	Why        string    `json:"why"`
	Decision   string    `json:"decision"`
	Status     string    `json:"status"`
	Confidence float64   `json:"confidence"`
	Evidence   []string  `json:"evidence"`
	Answer     string    `json:"answer"`
	Card       bool      `json:"card"`
	Chat       bool      `json:"chat"`
}

var planStatus = map[string]string{"proposed": "제안", "confirmed": "확정", "cancelled": "취소", "done": "지남"}

var noteStatus = map[string]string{"hypothesis": "추정", "confirmed": "확정", "rejected": "기각", "candidate": "시험 중"}

var intentStatus = map[string]string{"pending": "답 기다림", "executed": "함", "rejected": "반려", "undone": "되돌림",
	"dropped": "묻지 않고 넘김", "superseded": "다른 카드로 대신", "failed": "실패"}

var decisions = map[string]string{"execute": "바로 함", "notify": "하고 알림", "ask": "카드", "digest": "저녁 요약", "drop": "넘김"}

var weekdays = [...]string{"일", "월", "화", "수", "목", "금", "토"}

// day is "11/7(토)" for a YYYY-MM-DD.
func day(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return fmt.Sprintf("%d/%d(%s)", int(t.Month()), t.Day(), weekdays[t.Weekday()])
}

func renderLoops(loops []loopView, now time.Time) string {
	if len(loops) == 0 {
		return "열린 고리가 없습니다."
	}
	today := now.Format("2006-01-02")
	var b strings.Builder
	for _, l := range loops {
		who := map[string]string{"me": "내가 할 것", "them": "받을 것"}[l.Owner]
		if who == "" {
			who = l.Owner
		}
		parts := []string{fmt.Sprintf("[%s] %s", who, l.Title)}
		if l.Due != "" {
			due := "기한 " + day(l.Due[:min(len(l.Due), 10)])
			if l.Status == "open" && l.Due[:min(len(l.Due), 10)] < today {
				due += " 지남"
			}
			parts = append(parts, due)
		}
		if l.Status != "open" {
			parts = append(parts, "닫힘")
		}
		if l.Plan != "" {
			parts = append(parts, "'"+l.Plan+"' 일정")
		}
		src := l.Where
		if l.Said != "" {
			src += ": \"" + l.Said + "\""
		}
		parts = append(parts, src)
		b.WriteString("- " + strings.Join(parts, " · ") + "\n")
	}
	return b.String()
}

func renderPlans(from, to string, plans []planView) string {
	if len(plans) == 0 {
		return fmt.Sprintf("%s~%s 에 아는 일정이 없습니다.", from, to)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s~%s\n", from, to)
	for _, p := range plans {
		when := day(p.Date)
		if p.EndDate != "" && p.EndDate != p.Date {
			when += "~" + day(p.EndDate)
		}
		if p.Start != "" {
			when += " " + p.Start
			if p.End != "" {
				when += "~" + p.End
			}
		}
		status := planStatus[p.Status]
		if status == "" {
			status = p.Status
		}
		tags := []string{status}
		if p.Calendar {
			tags = append(tags, "캘린더")
		}
		if p.Declined != "" {
			tags = append(tags, "캘린더 카드 반려: "+p.Declined)
		}
		line := fmt.Sprintf("- %s %s [%s]", when, p.Title, strings.Join(tags, " · "))
		if p.Place != "" {
			line += " · 장소 " + p.Place
		}
		if len(p.People) > 0 {
			line += " · 함께 " + strings.Join(p.People, ", ")
		}
		if p.Where != "캘린더" {
			line += " · " + p.Where
			if p.Said != "" {
				line += ": \"" + p.Said + "\""
			}
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func renderNotes(notes []noteView) string {
	var b strings.Builder
	for _, n := range notes {
		if n.Scope == RuleScope {
			continue // the operator's rules: look 의 rules
		}
		status := noteStatus[n.Status]
		if status == "" {
			status = n.Status
		}
		line := fmt.Sprintf("- %s [%s", n.Text, status)
		if n.Status != "confirmed" && n.Status != "rejected" {
			line += fmt.Sprintf(" %.0f%%", n.Confidence*100)
		}
		if n.Scope != "" {
			line += " · " + n.Scope
		}
		line += "]"
		if n.Reason != "" {
			line += " - " + n.Reason
		}
		if n.Track != nil && n.Track.N > 0 {
			line += fmt.Sprintf(" (예측 %d번 중 %d번 맞음)", n.Track.N, n.Track.Right)
		}
		b.WriteString(line + "\n")
	}
	if b.Len() == 0 {
		return "세운 노트가 없습니다."
	}
	return b.String()
}

func renderAsked(days int, asked []askedView) string {
	if len(asked) == 0 {
		return fmt.Sprintf("최근 %d일 동안 mind 가 묻거나 알린 것이 없습니다.", days)
	}
	var b strings.Builder
	for _, a := range asked {
		at := a.At.In(time.Local).Format("1/2 15:04")
		how := decisions[a.Decision]
		if a.Chat {
			how = "대화의 카드(장부만)"
		} else if how == "" {
			how = a.Decision
		}
		status := intentStatus[a.Status]
		if status == "" {
			status = a.Status
		}
		line := fmt.Sprintf("- %s [%s · %s · %s] %s", at, a.Skill, how, status, a.Title)
		if a.Reason != "" {
			line += " - 이유: " + a.Reason
		}
		if a.Why != "" {
			line += " / 관문: " + a.Why
		}
		if len(a.Evidence) > 0 {
			line += " / 근거: \"" + strings.Join(a.Evidence, "\", \"") + "\""
		}
		if a.Answer != "" {
			line += " / 운영자: " + a.Answer
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
