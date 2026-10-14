// Package mind assembles the loop: observations come in from the log, the
// interpreters turn them into changes to the world model (and facts for
// long-term memory), attention hands the changes to skills, skills hand
// intents to the gate, the gate acts - and what it did comes back in as an
// observation.
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
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/attention"
	"github.com/choigonyok/jarvis/mind-svc/internal/draft"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/link"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/predict"
	"github.com/choigonyok/jarvis/mind-svc/internal/prefs"
	"github.com/choigonyok/jarvis/mind-svc/internal/reflect"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
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
	// Memory is long-term memory (memory-svc); nil keeps facts in the
	// ledger, pending.
	Memory memory.Client
	// PlanEvents announces confirmed plans as memory.plan events, the shape
	// the agent's suggestion engine already turns into calendar cards.
	PlanEvents bool
	// Photos is where albums are made (Immich); nil keeps them in memory.
	Photos act.Photos
	// Drafter writes the messages the operator may send; nil: a template.
	Drafter act.Drafter
	// Jobs is where browser jobs are made (jobs-svc); nil keeps them in memory.
	Jobs act.Jobs
	// Assets is the allocation, read only; nil: not known.
	Assets Assets
	// DailyUSD is the model's daily budget, for the report.
	DailyUSD float64
}

type Mind struct {
	Opts     Options
	World    *world.World
	Conv     *interpret.Conversation
	Actions  *interpret.Actions
	Attn     *attention.Attention
	Gate     *gate.Gate
	Skills   []skill.Skill
	Remember *Remember // nil without long-term memory
	Link     *link.Linker
	Self     *self.Self
	Predict  *predict.Predictor
	Reflect  *reflect.Reflector
	Prefs    *prefs.Prefs
	// nightly and mornings are further steps of the night and the morning,
	// in order.
	nightly  []func(ctx context.Context) error
	mornings []func(ctx context.Context) error
}

func New(o Options) *Mind {
	if o.Now == nil {
		o.Now = time.Now
	}
	m := &Mind{Opts: o}
	m.World = world.New(o.Store, o.Now)
	cfg := o.Interpret
	if o.Memory != nil {
		m.Remember = NewRemember(o.Store, o.Memory, o.Now, o.Logger)
		cfg.Recall, cfg.OnFact = m.recallRoom, m.Remember.Nudge
	}
	m.Conv = interpret.NewConversation(cfg, o.Store, m.World, o.LLM, o.Loc, o.Now, o.Logger)
	m.Actions = &interpret.Actions{World: m.World}
	judge := &link.Judge{St: o.Store, LLM: o.LLM}
	m.Link = &link.Linker{St: o.Store, World: m.World, Loc: o.Loc, Now: o.Now, Log: o.Logger, Judge: judge}
	m.Self = &self.Self{St: o.Store, Now: o.Now}
	m.Predict = &predict.Predictor{St: o.Store, World: m.World, Judge: judge, Loc: o.Loc, Now: o.Now}
	m.Reflect = &reflect.Reflector{St: o.Store, LLM: o.LLM, Predict: m.Predict, Self: m.Self, Loc: o.Loc, Now: o.Now}
	m.Link.OnPlan = func(ctx context.Context, e model.Entity) {
		if _, err := m.Predict.Prep(ctx, e); err != nil {
			o.Logger.Warn("준비물 예측을 하지 못했습니다", "plan", e.ID, "err", err)
		}
		if err := m.ceremony(ctx, e); err != nil {
			o.Logger.Warn("경조사 장부를 보지 못했습니다", "plan", e.ID, "err", err)
		}
	}
	m.World.Listen(m.Link.Watch)
	notifier := o.Notifier
	if notifier == nil {
		notifier = act.EventsNotifier{Log: noticeLog{o.Log}, Out: o.Logger}
	}
	photos := o.Photos
	if photos == nil {
		photos = &act.MemoryPhotos{}
	}
	drafter := o.Drafter
	if drafter == nil {
		drafter = &draft.Drafter{St: o.Store, LLM: o.LLM, Owner: o.Interpret.Owner}
	}
	jobs := o.Jobs
	if jobs == nil {
		jobs = &act.MemoryJobs{}
	}
	acts := act.NewRegistry(act.CalendarActuator{Cal: o.Calendar}, act.Note{},
		act.SettleActuator{Drafts: drafter}, act.PhotosActuator{Photos: photos}, act.CartActuator{Jobs: jobs},
		act.DraftActuator{Drafts: drafter}, act.ReviewActuator{})
	m.Gate = gate.New(o.Store, acts, notifier, o.Log, o.Policy, o.Loc, o.Now, o.URL, o.Logger)
	m.Prefs = &prefs.Prefs{St: o.Store, LLM: o.LLM, Now: o.Now}
	m.Gate.OnDecide = func(ctx context.Context, in model.Intent) {
		m.learn(ctx, in)
		m.answered(ctx, in)
	}
	m.mornings = append(m.mornings, m.people, m.gift, m.weekend, m.restock)
	m.nightly = append(m.nightly, m.expireQuestions, m.statedPreferences, m.deliveries)
	m.Skills = []skill.Skill{skill.Schedule{}, skill.Loops{}, skill.Brief{}, skill.After{}}
	m.Attn = attention.New(m.Skills, m.World, o.Store, m.Gate, o.Settle, o.Loc, o.Now, o.Logger)
	m.Link.OnLink = m.Attn.OnLink
	if o.Memory != nil {
		m.Attn.Recall = m.recallPeople
	}
	if o.PlanEvents {
		m.World.Listen(m.announcePlan)
	}
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
		if err := m.reply(ctx, msg); err != nil {
			m.Opts.Logger.Warn("답장 상황을 기록하지 못했습니다", "err", err)
		}
	case obs.ActionExecuted, obs.ActionUndone:
		return m.Actions.Handle(ctx, o)
	case obs.OccurrenceObserved:
		var ref obs.OccurrenceRef
		if err := json.Unmarshal(o.Data, &ref); err != nil {
			return nil
		}
		if err := m.Link.Occurrence(ctx, ref.ID); err != nil {
			return err
		}
		if occ, err := m.Opts.Store.Occurrence(ctx, ref.ID); err == nil {
			if _, err := m.Predict.Match(ctx, occ); err != nil {
				return err
			}
			if err := m.salary(ctx, occ); err != nil {
				return err
			}
			if err := m.late(ctx, occ); err != nil {
				return err
			}
			if err := m.nearWish(ctx, occ); err != nil {
				return err
			}
		}
		return nil
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
	go m.Link.Run(ctx, every)
	go m.nights(ctx, 30*time.Second)
	go m.daily(ctx, 30*time.Second, "tick:predict", morningAt, m.Morning)
	go m.daily(ctx, 30*time.Second, "tick:review", reviewAt, m.reviews)
	if m.Remember != nil {
		go m.Remember.Run(ctx, 15*time.Second)
	}
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

// Observe records an occurrence read from a structured source and logs that
// it was seen, as a sensor does. Seeing the same one again (a payment's
// items filled in later) updates it.
func (m *Mind) Observe(ctx context.Context, o model.Occurrence) error {
	if err := m.Opts.Store.PutOccurrence(ctx, o); err != nil {
		return err
	}
	data, _ := json.Marshal(obs.OccurrenceRef{ID: o.ID, Kind: o.Kind})
	return m.Opts.Log.Append(ctx, obs.Observation{
		Source: strings.SplitN(o.ID, ":", 2)[0], Type: obs.OccurrenceObserved, Subject: o.ID,
		Data: data, Key: "occurrence:" + o.ID + ":" + o.At.UTC().Format(time.RFC3339), OccurredAt: o.At,
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

// Status is the loop's health in a few numbers, for the status screen: is it
// reading, is anything left unread for long, are facts reaching memory.
type Status struct {
	LLM           LLMState       `json:"llm"`
	Rooms         int            `json:"rooms"`
	Unread        int            `json:"unread"`                  // messages not yet interpreted
	OldestUnread  *time.Time     `json:"oldestUnread,omitempty"`  // the oldest of them
	LastRead      *time.Time     `json:"lastRead,omitempty"`      // the last read that went through
	ReadError     string         `json:"readError,omitempty"`     // why the latest read failed
	Memory        bool           `json:"memory"`                  // long-term memory is connected
	Facts         map[string]int `json:"facts"`                   // by status
	OldestPending *time.Time     `json:"oldestPending,omitempty"` // the oldest fact still waiting for memory
	FactError     string         `json:"factError,omitempty"`     // why the latest waiting fact was not taken
	Usage         *Usage         `json:"usage,omitempty"`         // what the model cost
}

// Usage is the model's cost: today against the budget, and this month by
// what it was for.
type Usage struct {
	Model    string  `json:"model"`
	DailyUSD float64 `json:"dailyUsd"`
	TodayUSD float64 `json:"todayUsd"`
	Month    struct {
		USD   float64 `json:"usd"`
		Calls int     `json:"calls"`
	} `json:"month"`
	Purposes []model.Usage `json:"purposes"` // this month, by purpose
}

// Cost is the model's usage report.
func (m *Mind) Cost(ctx context.Context) (*Usage, error) {
	now := m.Opts.Now().In(m.Opts.Loc)
	us, err := m.Opts.Store.Usages(ctx, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, m.Opts.Loc).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	out := &Usage{Model: m.Opts.LLM.Model(), DailyUSD: m.Opts.DailyUSD, Purposes: []model.Usage{}}
	by := map[string]*model.Usage{}
	today := now.Format("2006-01-02")
	for _, u := range us {
		if u.Day == today {
			out.TodayUSD += u.USD
		}
		out.Month.USD += u.USD
		out.Month.Calls += u.Calls
		p := by[u.Purpose]
		if p == nil {
			p = &model.Usage{Purpose: u.Purpose}
			by[u.Purpose] = p
		}
		p.Calls += u.Calls
		p.USD += u.USD
		p.In += u.In
		p.Out += u.Out
	}
	for _, k := range []string{"interpret", "judge", "reflect", "draft"} {
		if p := by[k]; p != nil {
			out.Purposes = append(out.Purposes, *p)
			delete(by, k)
		}
	}
	for _, p := range by {
		out.Purposes = append(out.Purposes, *p)
	}
	return out, nil
}

func (m *Mind) Status(ctx context.Context) (Status, error) {
	st := m.Opts.Store
	s := Status{LLM: LLMState{m.Opts.LLM.Ready(), m.Opts.LLM.Model(), m.Opts.LLM.Spent()}, Memory: m.Opts.Memory != nil, Facts: map[string]int{}}
	rooms, err := st.Rooms(ctx)
	if err != nil {
		return s, err
	}
	s.Rooms = len(rooms)
	for _, r := range rooms {
		if r.Uninterpreted == 0 {
			continue
		}
		s.Unread += r.Uninterpreted
		// Oldest first, as many as are unread: the first is the oldest.
		unread, err := st.RoomMessages(ctx, r.Room, r.Interpreted, time.Time{}, r.Uninterpreted)
		if err != nil {
			return s, err
		}
		if len(unread) > 0 && (s.OldestUnread == nil || unread[0].SentAt.Before(*s.OldestUnread)) {
			at := unread[0].SentAt
			s.OldestUnread = &at
		}
	}
	if at, why := m.Conv.LastRead(); !at.IsZero() || why != "" {
		if !at.IsZero() {
			s.LastRead = &at
		}
		s.ReadError = why
	}
	if s.Usage, err = m.Cost(ctx); err != nil {
		return s, err
	}
	facts, err := st.Facts(ctx, 1000)
	if err != nil {
		return s, err
	}
	for _, f := range facts {
		s.Facts[f.Status]++
		if f.Status == model.FactPending && (s.OldestPending == nil || f.CreatedAt.Before(*s.OldestPending)) {
			at := f.CreatedAt
			s.OldestPending, s.FactError = &at, f.Error
		}
	}
	return s, nil
}

// Snapshot is everything the console shows.
type Snapshot struct {
	Mode      string                `json:"mode"`
	Now       time.Time             `json:"now"`
	LLM       LLMState              `json:"llm"`
	Rooms     []RoomView            `json:"rooms"`
	Entities  []model.Entity        `json:"entities"`
	Changes   []model.Change        `json:"changes"`
	Intents   []model.Intent        `json:"intents"`
	Timers    []model.Timer         `json:"timers"`
	Levels    map[string][2]string  `json:"levels"`
	Settling  int                   `json:"settling"`
	Notices   []act.Notice          `json:"notices,omitempty"`
	Calendar  []model.CalendarEvent `json:"calendar,omitempty"`
	Facts     []model.Fact          `json:"facts"`
	Aliases   []model.Alias         `json:"aliases"`
	Memory    bool                  `json:"memory"` // long-term memory is connected
	Situation world.Situation       `json:"situation"`
	// What is believed about the operator, and how well it predicts.
	Notes       []model.Note       `json:"notes"`
	Links       []LinkView         `json:"links"`
	Predictions []model.Prediction `json:"predictions"`
	Brier       *Brier             `json:"brier,omitempty"`
	Patterns    []model.Pattern    `json:"patterns"`
}

// LinkView is a link with the names of its ends, for the console.
type LinkView struct {
	model.Link
	FromTitle string `json:"fromTitle"`
	ToTitle   string `json:"toTitle"`
}

// Brier is how well the last fortnight's predictions did.
type Brier struct {
	Score float64 `json:"score"`
	N     int     `json:"n"`
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
	if s.Facts, err = st.Facts(ctx, 80); err != nil {
		return s, err
	}
	if s.Aliases, err = st.Aliases(ctx); err != nil {
		return s, err
	}
	s.Memory = m.Opts.Memory != nil
	if s.Situation, err = m.Situation(ctx); err != nil {
		return s, err
	}
	if s.Notes, err = st.Notes(ctx); err != nil {
		return s, err
	}
	now := m.Opts.Now()
	if s.Predictions, err = st.Predictions(ctx, now.Add(-14*24*time.Hour), now.Add(14*24*time.Hour)); err != nil {
		return s, err
	}
	if score, n, err := m.Predict.Brier(ctx, now.Add(-14*24*time.Hour), now); err == nil && n > 0 {
		s.Brier = &Brier{Score: score, N: n}
	}
	if s.Patterns, err = st.Patterns(ctx, ""); err != nil {
		return s, err
	}
	links, err := st.LinksSince(ctx, now.Add(-60*24*time.Hour))
	if err != nil {
		return s, err
	}
	for i := len(links) - 1; i >= 0 && len(s.Links) < 60; i-- {
		l := links[i]
		if l.Rel == model.With {
			continue
		}
		v := LinkView{Link: l}
		if o, err := st.Occurrence(ctx, l.From); err == nil {
			v.FromTitle = o.Title
			if v.FromTitle == "" {
				v.FromTitle = o.Kind + " " + o.At.In(m.Opts.Loc).Format("01/02 15:04")
			}
		}
		if e, err := st.Entity(ctx, l.To); err == nil {
			v.ToTitle = e.State.Title
		}
		s.Links = append(s.Links, v)
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

// DecideLink is the operator's word on a link: it is so (confirmed) or it
// is not (rejected). Neither the rules nor the judge overwrite it after.
func (m *Mind) DecideLink(ctx context.Context, from, to, rel, state string) (model.Link, error) {
	if state != model.Confirmed && state != model.Rejected {
		return model.Link{}, fmt.Errorf("모르는 상태 %q", state)
	}
	links, err := m.Opts.Store.LinksOf(ctx, from)
	if err != nil {
		return model.Link{}, err
	}
	for _, l := range links {
		if l.To == to && l.Rel == rel {
			l.State, l.By, l.UpdatedAt = state, "user", m.Opts.Now()
			if state == model.Confirmed {
				l.Confidence = 1
			}
			return l, m.Opts.Store.PutLink(ctx, l)
		}
	}
	return model.Link{}, model.ErrNotFound
}

// Situation is the world model's "right now", which the conversation is
// shown every turn.
func (m *Mind) Situation(ctx context.Context) (world.Situation, error) {
	return m.World.Situation(ctx, m.Opts.Now(), m.Opts.Loc)
}

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
