// Package mind assembles the loop: observations come in from the log, the
// interpreters turn them into changes to the world model, attention hands
// the changes to skills, skills hand intents to the gate, the gate acts -
// and what it did comes back in as an observation.
//
// One process holds all the layers for now, each behind its own package and
// interface, so a layer can move out on its own when it needs to.
package mind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/attention"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/skill"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

type Options struct {
	Store     store.Store
	Log       obs.Log
	LLM       llm.Client
	Calendar  act.Calendar
	Notifier  act.Notifier // nil: notices go through the log to notify-svc
	Loc       *time.Location
	Now       func() time.Time
	Logger    *slog.Logger
	Interpret interpret.Config
	Settle    time.Duration
	Policy    gate.Policy
	URL       string // where a notice links to
	Mode      string // memory | stack
}

type Mind struct {
	Opts    Options
	World   *world.World
	Conv    *interpret.Conversation
	Actions *interpret.Actions
	Attn    *attention.Attention
	Gate    *gate.Gate
	Skills  []skill.Skill
}

func New(o Options) *Mind {
	if o.Now == nil {
		o.Now = time.Now
	}
	m := &Mind{Opts: o}
	m.World = world.New(o.Store, o.Now)
	m.Conv = interpret.NewConversation(o.Interpret, o.Store, m.World, o.LLM, o.Loc, o.Now, o.Logger)
	m.Actions = &interpret.Actions{World: m.World}
	notifier := o.Notifier
	if notifier == nil {
		notifier = act.EventsNotifier{Log: noticeLog{o.Log}, Out: o.Logger}
	}
	acts := act.NewRegistry(act.CalendarActuator{Cal: o.Calendar})
	m.Gate = gate.New(o.Store, acts, notifier, o.Log, o.Policy, o.Loc, o.Now, o.URL, o.Logger)
	m.Skills = []skill.Skill{skill.Schedule{}, skill.Loops{}, skill.Brief{}}
	m.Attn = attention.New(m.Skills, m.World, o.Store, m.Gate, o.Settle, o.Loc, o.Now, o.Logger)
	return m
}

// Handle is the loop's entry: one observation from the log.
func (m *Mind) Handle(ctx context.Context, o obs.Observation) error {
	switch o.Type {
	case obs.MessageObserved:
		var ref obs.MessageRef
		if err := json.Unmarshal(o.Data, &ref); err != nil {
			return nil // not ours to fix; skip it
		}
		msg, err := m.Opts.Store.Message(ctx, ref.Ref)
		if errors.Is(err, model.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		m.Conv.Observe(msg)
	case obs.ActionExecuted, obs.ActionUndone:
		return m.Actions.Handle(ctx, o)
	}
	return nil
}

// Run keeps the interpreters and attention going until ctx ends.
func (m *Mind) Run(ctx context.Context, every time.Duration) {
	if err := m.Conv.Resume(ctx); err != nil {
		m.Opts.Logger.Warn("남은 대화를 다시 잡지 못했습니다", "err", err)
	}
	go m.Conv.Run(ctx, every)
	go m.Attn.Run(ctx, every)
}

// Simulate is a message from the console's simulator: stored and logged
// exactly as a sensor would.
func (m *Mind) Simulate(ctx context.Context, room, roomName, sender string, mine bool, text string, at time.Time) (model.Message, error) {
	n, err := m.Opts.Store.Cursor(ctx, "sim")
	if err != nil {
		return model.Message{}, err
	}
	n++
	if err := m.Opts.Store.SetCursor(ctx, "sim", n); err != nil {
		return model.Message{}, err
	}
	if at.IsZero() {
		at = m.Opts.Now()
	}
	msg := model.Message{
		Ref: fmt.Sprintf("sim:%d", n), Source: "sim", Room: room, RoomName: roomName,
		Sender: sender, IsMine: mine, SentAt: at, Text: text,
	}
	return msg, Observe(ctx, m.Opts.Store, m.Opts.Log, msg)
}

// Observe stores a message's text and logs that it was seen - what every
// sensor does.
func Observe(ctx context.Context, st store.Store, log obs.Log, msg model.Message) error {
	if err := st.PutMessage(ctx, msg); err != nil {
		return err
	}
	data, _ := json.Marshal(obs.MessageRef{Ref: msg.Ref, Room: msg.Room})
	return log.Append(ctx, obs.Observation{
		Source: msg.Source, Type: obs.MessageObserved, Subject: msg.Room,
		Data: data, Key: msg.Ref, OccurredAt: msg.SentAt,
	})
}

// ReadAll interprets every room with something new, now.
func (m *Mind) ReadAll(ctx context.Context) error {
	rooms, err := m.Opts.Store.Rooms(ctx)
	if err != nil {
		return err
	}
	for _, r := range rooms {
		if r.Uninterpreted > 0 {
			if err := m.Conv.Read(ctx, r.Room); err != nil {
				return err
			}
		}
	}
	return nil
}

// Snapshot is everything the console shows.
type Snapshot struct {
	Mode     string                `json:"mode"`
	Now      time.Time             `json:"now"`
	LLM      LLMState              `json:"llm"`
	Rooms    []RoomView            `json:"rooms"`
	Entities []model.Entity        `json:"entities"`
	Changes  []model.Change        `json:"changes"`
	Intents  []model.Intent        `json:"intents"`
	Timers   []model.Timer         `json:"timers"`
	Levels   map[string][2]string  `json:"levels"`
	Settling int                   `json:"settling"`
	Notices  []act.Notice          `json:"notices,omitempty"`
	Calendar []model.CalendarEvent `json:"calendar,omitempty"`
}

type LLMState struct {
	Ready bool    `json:"ready"`
	Model string  `json:"model"`
	Spent float64 `json:"spentUsd"`
}

type RoomView struct {
	store.Room
	ReadAt   *time.Time      `json:"readAt,omitempty"` // when it will be read
	Messages []model.Message `json:"messages"`
}

func (m *Mind) Snapshot(ctx context.Context) (Snapshot, error) {
	st := m.Opts.Store
	s := Snapshot{Mode: m.Opts.Mode, Now: m.Opts.Now(), LLM: LLMState{m.Opts.LLM.Ready(), m.Opts.LLM.Model(), m.Opts.LLM.Spent()}, Settling: m.Attn.Waiting()}
	rooms, err := st.Rooms(ctx)
	if err != nil {
		return s, err
	}
	due := m.Conv.Pending()
	for _, r := range rooms {
		msgs, err := st.RoomMessages(ctx, r.Room, time.Time{}, time.Time{}, 40)
		if err != nil {
			return s, err
		}
		v := RoomView{Room: r, Messages: msgs}
		if at, ok := due[r.Room]; ok {
			v.ReadAt = &at
		}
		s.Rooms = append(s.Rooms, v)
	}
	if s.Entities, err = st.Entities(ctx, "", "", false); err != nil {
		return s, err
	}
	sort.SliceStable(s.Entities, func(i, j int) bool { return s.Entities[i].UpdatedAt.After(s.Entities[j].UpdatedAt) })
	if s.Changes, err = st.Changes(ctx, "", 80); err != nil {
		return s, err
	}
	if s.Intents, err = st.Intents(ctx, 60); err != nil {
		return s, err
	}
	if s.Timers, err = st.Timers(ctx); err != nil {
		return s, err
	}
	if s.Levels, err = m.Gate.Levels(ctx); err != nil {
		return s, err
	}
	if n, ok := m.Opts.Notifier.(*act.MemoryNotifier); ok {
		s.Notices = n.All()
	}
	if c, ok := m.Opts.Calendar.(*act.MemoryCalendar); ok {
		s.Calendar = c.All()
		sort.Slice(s.Calendar, func(i, j int) bool {
			return s.Calendar[i].Date+s.Calendar[i].Start < s.Calendar[j].Date+s.Calendar[j].Start
		})
	}
	return s, nil
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// noticeLog lets a notice travel through the log as an event with wording,
// which notify-svc turns into a push or a digest line.
type noticeLog struct{ log obs.Log }

func (n noticeLog) AppendNotice(ctx context.Context, kind string, nt act.Notice) error {
	wording, _ := json.Marshal(map[string]string{"tier": nt.Tier, "level": nt.Level, "title": nt.Title, "body": nt.Body, "url": nt.URL})
	return n.log.Append(ctx, obs.Observation{
		Source: "mind", Type: kind, Subject: nt.Key, Data: json.RawMessage(`{}`),
		Key: nt.Key, OccurredAt: nt.At, Notify: wording,
	})
}
