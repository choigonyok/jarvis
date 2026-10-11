// Package act is what the assistant can do, and how each thing is undone.
//
// Every actuator previews (what it will do, in words), executes, and hands
// back how to take it back. An action that cannot be undone is never done
// without asking - the gate makes sure of that.
package act

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

type Actuator interface {
	Kinds() []string
	Preview(ctx context.Context, kind string, payload json.RawMessage) (string, error)
	// Execute returns the result (fed back as an observation) and how to undo it.
	Execute(ctx context.Context, kind string, payload json.RawMessage) (result, undo json.RawMessage, err error)
	// Undo takes an action back, and says what that produced.
	Undo(ctx context.Context, kind string, undo json.RawMessage) (json.RawMessage, error)
}

type Registry struct{ byKind map[string]Actuator }

func NewRegistry(as ...Actuator) *Registry {
	r := &Registry{byKind: map[string]Actuator{}}
	for _, a := range as {
		for _, k := range a.Kinds() {
			r.byKind[k] = a
		}
	}
	return r
}

func (r *Registry) For(kind string) (Actuator, bool) {
	a, ok := r.byKind[kind]
	return a, ok
}

// Calendar is where entries live: calendar-svc in the stack, a map on a
// laptop.
type Calendar interface {
	Get(ctx context.Context, id string) (model.CalendarEvent, error)
	Put(ctx context.Context, e model.CalendarEvent) (model.CalendarEvent, error)
	Delete(ctx context.Context, id string) (model.CalendarEvent, error)
}

// CalendarActuator does calendar.create, .update and .delete.
type CalendarActuator struct{ Cal Calendar }

func (CalendarActuator) Kinds() []string {
	return []string{"calendar.create", "calendar.update", "calendar.delete"}
}

type calendarUndo struct {
	Op    string              `json:"op"` // delete | restore | recreate
	ID    string              `json:"id,omitempty"`
	Event model.CalendarEvent `json:"event"`
}

func (c CalendarActuator) Preview(_ context.Context, kind string, payload json.RawMessage) (string, error) {
	var a model.CalendarAction
	if err := json.Unmarshal(payload, &a); err != nil {
		return "", err
	}
	if kind != "calendar.delete" {
		if _, err := time.Parse("2006-01-02", a.Event.Date); err != nil || strings.TrimSpace(a.Event.Title) == "" {
			return "", errors.New("날짜(YYYY-MM-DD)와 제목이 필요합니다")
		}
	}
	if kind != "calendar.create" && a.EventID == "" {
		return "", errors.New("어느 일정인지(eventId)가 필요합니다")
	}
	line := a.Event.Date
	if a.Event.Start != "" {
		line += " " + a.Event.Start
	}
	line += " " + a.Event.Title
	if a.Event.Place != "" {
		line += " @" + a.Event.Place
	}
	where := "내 일정"
	if a.Event.Owner == "shared" {
		where = "공유 일정"
	}
	switch kind {
	case "calendar.create":
		return where + "에 추가: " + line, nil
	case "calendar.update":
		return "일정 수정: " + line, nil
	default:
		return "일정 삭제: " + line, nil
	}
}

func (c CalendarActuator) Execute(ctx context.Context, kind string, payload json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	var a model.CalendarAction
	if err := json.Unmarshal(payload, &a); err != nil {
		return nil, nil, err
	}
	switch kind {
	case "calendar.create":
		saved, err := c.Cal.Put(ctx, a.Event)
		if err != nil {
			return nil, nil, err
		}
		return result(saved.ID), mustJSON(calendarUndo{Op: "delete", ID: saved.ID}), nil
	case "calendar.update":
		prev, err := c.Cal.Get(ctx, a.EventID)
		if err != nil {
			return nil, nil, err
		}
		next := a.Event
		next.ID = a.EventID
		saved, err := c.Cal.Put(ctx, next)
		if err != nil {
			return nil, nil, err
		}
		return result(saved.ID), mustJSON(calendarUndo{Op: "restore", Event: prev}), nil
	case "calendar.delete":
		gone, err := c.Cal.Delete(ctx, a.EventID)
		if err != nil {
			return nil, nil, err
		}
		gone.ID = ""
		return result(""), mustJSON(calendarUndo{Op: "recreate", Event: gone}), nil
	}
	return nil, nil, fmt.Errorf("모르는 행동 %s", kind)
}

func (c CalendarActuator) Undo(ctx context.Context, _ string, undo json.RawMessage) (json.RawMessage, error) {
	var u calendarUndo
	if err := json.Unmarshal(undo, &u); err != nil {
		return nil, err
	}
	switch u.Op {
	case "delete":
		_, err := c.Cal.Delete(ctx, u.ID)
		return result(""), err
	case "restore":
		saved, err := c.Cal.Put(ctx, u.Event)
		return result(saved.ID), err
	case "recreate":
		saved, err := c.Cal.Put(ctx, u.Event)
		return result(saved.ID), err
	}
	return nil, fmt.Errorf("되돌릴 수 없는 기록 %q", u.Op)
}

func result(id string) json.RawMessage { return mustJSON(map[string]string{"calendarId": id}) }

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// MemoryCalendar is a calendar in this process: a laptop run touches no
// real calendar.
type MemoryCalendar struct {
	mu  sync.Mutex
	seq int
	evs map[string]model.CalendarEvent
}

func NewMemoryCalendar() *MemoryCalendar {
	return &MemoryCalendar{evs: map[string]model.CalendarEvent{}}
}

func (m *MemoryCalendar) Get(_ context.Context, id string) (model.CalendarEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.evs[id]
	if !ok {
		return model.CalendarEvent{}, model.ErrNotFound
	}
	return e, nil
}

func (m *MemoryCalendar) Put(_ context.Context, e model.CalendarEvent) (model.CalendarEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ID == "" {
		m.seq++
		e.ID = fmt.Sprintf("cal-%d", m.seq)
	}
	m.evs[e.ID] = e
	return e, nil
}

func (m *MemoryCalendar) Delete(_ context.Context, id string) (model.CalendarEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.evs[id]
	if !ok {
		return model.CalendarEvent{}, model.ErrNotFound
	}
	delete(m.evs, id)
	return e, nil
}

// All is the calendar's entries, for the console.
func (m *MemoryCalendar) All() []model.CalendarEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.CalendarEvent, 0, len(m.evs))
	for _, e := range m.evs {
		out = append(out, e)
	}
	return out
}

// HTTPCalendar is calendar-svc (PUT /events, GET and DELETE /events/{id}).
type HTTPCalendar struct {
	URL   string
	Token string
	HTTP  *http.Client
}

func (h HTTPCalendar) Get(ctx context.Context, id string) (model.CalendarEvent, error) {
	var e model.CalendarEvent
	return e, h.do(ctx, http.MethodGet, "/events/"+id, nil, &e)
}

func (h HTTPCalendar) Put(ctx context.Context, e model.CalendarEvent) (model.CalendarEvent, error) {
	e.Source = "jarvis"
	var saved model.CalendarEvent
	return saved, h.do(ctx, http.MethodPut, "/events", e, &saved)
}

func (h HTTPCalendar) Delete(ctx context.Context, id string) (model.CalendarEvent, error) {
	var gone model.CalendarEvent
	return gone, h.do(ctx, http.MethodDelete, "/events/"+id, nil, &gone)
}

func (h HTTPCalendar) do(ctx context.Context, method, path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		_ = json.NewEncoder(&body).Encode(in)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.URL, "/")+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	client := h.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return model.ErrNotFound
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("calendar-svc %d: %s", resp.StatusCode, e.Error)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Notice is what reaches a person: a push now, or a line in the evening's
// summary.
type Notice struct {
	Title string    `json:"title"`
	Body  string    `json:"body,omitempty"`
	URL   string    `json:"url,omitempty"`
	Tier  string    `json:"tier"` // now | digest
	Level string    `json:"level,omitempty"`
	Key   string    `json:"key,omitempty"`
	At    time.Time `json:"at"`
}

type Notifier interface {
	Notify(ctx context.Context, n Notice)
}

// MemoryNotifier keeps the last notices for the console.
type MemoryNotifier struct {
	mu  sync.Mutex
	all []Notice
}

func (m *MemoryNotifier) Notify(_ context.Context, n Notice) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n.At.IsZero() {
		n.At = time.Now()
	}
	m.all = append(m.all, n)
	if len(m.all) > 100 {
		m.all = m.all[len(m.all)-100:]
	}
}

func (m *MemoryNotifier) All() []Notice {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Notice, len(m.all))
	copy(out, m.all)
	return out
}

// Appender is the observation log's append, which is how a notice reaches
// notify-svc in the stack: an event with wording, kind mind.*.
type Appender interface {
	AppendNotice(ctx context.Context, kind string, n Notice) error
}

// EventsNotifier sends notices as events through the log.
type EventsNotifier struct {
	Log Appender
	Out *slog.Logger
}

func (e EventsNotifier) Notify(ctx context.Context, n Notice) {
	if err := e.Log.AppendNotice(ctx, "mind.notice", n); err != nil {
		e.Out.Warn("알림을 보내지 못했습니다", "err", err)
	}
}
