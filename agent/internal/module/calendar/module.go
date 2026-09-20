package calendar

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
)

// The registry sorts modules by type assertion, so a method set that drifts
// would be skipped in silence rather than failing to build. These two lines
// are what make that impossible.
var (
	_ module.Actuator      = (*Module)(nil)
	_ module.ContextSource = (*Module)(nil)
)

const Name = "calendar"

const (
	KindCreate = "calendar.create_event"
	KindUpdate = "calendar.update_event"
	KindDelete = "calendar.delete_event"
)

// Module is the calendar as the registry sees it: an Actuator (it changes
// things) and a ContextSource (it knows what is already booked).
type Module struct {
	store *Store
}

func New(store *Store) *Module { return &Module{store: store} }

func (m *Module) Name() string { return Name }

func (m *Module) Start(context.Context) error { return m.store.Load() }

func (m *Module) Stop(context.Context) error { return nil }

// Specs declares what this module accepts. Reversible is the honest answer to
// "may this ever run without a person looking", and delete's answer is no.
func (m *Module) Specs() []action.Spec {
	return []action.Spec{
		{
			Kind:       KindCreate,
			Summary:    "캘린더에 일정을 추가합니다. 날짜는 YYYY-MM-DD, 시각은 HH:MM 절대값으로만 보냅니다.",
			Reversible: true,
		},
		{
			Kind:       KindUpdate,
			Summary:    "기존 일정을 수정합니다. 바꿀 필드만 보냅니다.",
			Reversible: true,
		},
		{
			Kind:       KindDelete,
			Summary:    "일정을 삭제합니다.",
			Reversible: false,
		},
	}
}

// --- 도구 입력 -------------------------------------------------------------
//
// These structs are the schema: the MCP server infers the tool's JSON Schema
// from them, so a field described here is a field the model is told about,
// and a field absent here is one it cannot send.

type CreateInput struct {
	Date  string `json:"date" jsonschema:"일정 날짜. YYYY-MM-DD 형식의 절대 날짜만 보낸다. '다음 주 화요일' 같은 상대 표현은 직접 계산해서 변환한다."`
	Title string `json:"title" jsonschema:"일정 제목. 사람이 캘린더에서 읽을 한 줄."`
	Start string `json:"start,omitempty" jsonschema:"시작 시각. HH:MM 24시간제. 종일 일정이면 비운다."`
	End   string `json:"end,omitempty" jsonschema:"끝나는 시각. HH:MM 24시간제."`
	Place string `json:"place,omitempty" jsonschema:"장소."`
	Memo  string `json:"memo,omitempty" jsonschema:"짧은 메모."`
}

type UpdateInput struct {
	ID    string `json:"id" jsonschema:"수정할 일정의 id. list_events 로 먼저 확인한다."`
	Date  string `json:"date,omitempty" jsonschema:"바꿀 날짜. YYYY-MM-DD."`
	Title string `json:"title,omitempty" jsonschema:"바꿀 제목."`
	Start string `json:"start,omitempty" jsonschema:"바꿀 시작 시각. HH:MM."`
	End   string `json:"end,omitempty" jsonschema:"바꿀 종료 시각. HH:MM."`
	Place string `json:"place,omitempty" jsonschema:"바꿀 장소."`
	Memo  string `json:"memo,omitempty" jsonschema:"바꿀 메모."`
}

type DeleteInput struct {
	ID string `json:"id" jsonschema:"삭제할 일정의 id."`
}

type ListInput struct {
	From string `json:"from,omitempty" jsonschema:"조회 시작일. YYYY-MM-DD. 비우면 처음부터."`
	To   string `json:"to,omitempty" jsonschema:"조회 종료일. YYYY-MM-DD. 비우면 끝까지."`
}

type ListOutput struct {
	Events []Event `json:"events"`
}

// --- Actuator --------------------------------------------------------------

// Preview renders the card the operator decides on. An update or a delete
// reads current state first, so the card shows what is there now rather than
// only what is being asked for.
func (m *Module) Preview(_ context.Context, a action.Action) (action.Card, error) {
	switch a.Kind {
	case KindCreate:
		var in CreateInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Card{}, err
		}
		return action.Card{
			Title:       "일정을 추가합니다",
			Body:        line(eventOf(in)),
			Consequence: "캘린더에 추가됩니다.",
		}, nil

	case KindUpdate:
		var in UpdateInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Card{}, err
		}
		before, ok := m.store.Get(in.ID)
		if !ok {
			return action.Card{}, ErrNotFound
		}
		after := merge(before, in)
		return action.Card{
			Title:       "일정을 수정합니다",
			Body:        "- " + line(before) + "\n+ " + line(after),
			Consequence: "기존 내용이 바뀝니다.",
		}, nil

	case KindDelete:
		var in DeleteInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Card{}, err
		}
		before, ok := m.store.Get(in.ID)
		if !ok {
			return action.Card{}, ErrNotFound
		}
		return action.Card{
			Title:       "일정을 삭제합니다",
			Body:        line(before),
			Consequence: "되돌릴 수 없습니다.",
		}, nil
	}
	return action.Card{}, fmt.Errorf("모르는 동작입니다: %s", a.Kind)
}

func (m *Module) Execute(_ context.Context, a action.Action) (action.Result, error) {
	switch a.Kind {
	case KindCreate:
		var in CreateInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Result{}, err
		}
		e := eventOf(in)
		e.Source = SourceAgent
		saved, err := m.store.Put(e)
		if err != nil {
			return action.Result{}, err
		}
		return action.Result{Note: "추가함 · " + line(saved)}, nil

	case KindUpdate:
		var in UpdateInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Result{}, err
		}
		before, ok := m.store.Get(in.ID)
		if !ok {
			return action.Result{}, ErrNotFound
		}
		saved, err := m.store.Put(merge(before, in))
		if err != nil {
			return action.Result{}, err
		}
		return action.Result{Note: "수정함 · " + line(saved)}, nil

	case KindDelete:
		var in DeleteInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Result{}, err
		}
		gone, err := m.store.Delete(in.ID)
		if err != nil {
			return action.Result{}, err
		}
		return action.Result{Note: "삭제함 · " + line(gone)}, nil
	}
	return action.Result{}, fmt.Errorf("모르는 동작입니다: %s", a.Kind)
}

// --- ContextSource ---------------------------------------------------------

// Facts answers "what is already on the calendar" for the days around now.
// Not wired into a reasoning loop yet - the interface is what matters, so the
// module that will answer that question is already the one holding the data.
func (m *Module) Facts(_ context.Context, _ string) ([]module.Fact, error) {
	now := time.Now()
	from := now.AddDate(0, 0, -7).Format("2006-01-02")
	to := now.AddDate(0, 0, 30).Format("2006-01-02")

	var out []module.Fact
	for _, e := range m.store.Range(from, to) {
		out = append(out, module.Fact{Source: Name, Text: line(e)})
	}
	return out, nil
}

// --- 렌더링 ----------------------------------------------------------------

var weekdays = [...]string{"일", "월", "화", "수", "목", "금", "토"}

// line is the one-line form of an event, used on cards, in results, and in
// facts. One renderer means the operator reads the same sentence everywhere.
func line(e Event) string {
	var b strings.Builder
	b.WriteString(korDate(e.Date))
	if e.Start != "" {
		b.WriteString(" " + e.Start)
		if e.End != "" {
			b.WriteString("–" + e.End)
		}
	} else {
		b.WriteString(" 종일")
	}
	b.WriteString("  " + e.Title)
	if e.Place != "" {
		b.WriteString(" · " + e.Place)
	}
	return b.String()
}

func korDate(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return fmt.Sprintf("%d월 %d일 (%s)", int(t.Month()), t.Day(), weekdays[int(t.Weekday())])
}

func eventOf(in CreateInput) Event {
	return Event{
		Date:  in.Date,
		Start: in.Start,
		End:   in.End,
		Title: in.Title,
		Place: in.Place,
		Memo:  in.Memo,
	}
}

// merge applies a patch. An empty string means "leave it alone", which is why
// the update tool's schema marks every field but id as optional.
func merge(base Event, in UpdateInput) Event {
	out := base
	if in.Date != "" {
		out.Date = in.Date
	}
	if in.Title != "" {
		out.Title = in.Title
	}
	if in.Start != "" {
		out.Start = in.Start
	}
	if in.End != "" {
		out.End = in.End
	}
	if in.Place != "" {
		out.Place = in.Place
	}
	if in.Memo != "" {
		out.Memo = in.Memo
	}
	return out
}
