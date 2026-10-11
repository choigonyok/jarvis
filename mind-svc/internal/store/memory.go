package store

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Memory keeps everything in this process: the loop runs on a laptop with
// nothing else, and tests start from nothing.
type Memory struct {
	mu       sync.Mutex
	entities map[string]model.Entity
	changes  []model.Change
	messages map[string]model.Message
	marks    map[string]time.Time
	timers   []model.Timer
	intents  map[string]model.Intent
	cursors  map[string]int64
	seq      int64
}

func NewMemory() *Memory {
	return &Memory{
		entities: map[string]model.Entity{},
		messages: map[string]model.Message{},
		marks:    map[string]time.Time{},
		intents:  map[string]model.Intent{},
		cursors:  map[string]int64{},
	}
}

var _ Store = (*Memory)(nil)

func (m *Memory) next() int64 { m.seq++; return m.seq }

func (m *Memory) Entity(_ context.Context, id string) (model.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entities[id]
	if !ok {
		return model.Entity{}, model.ErrNotFound
	}
	return clone(e), nil
}

func (m *Memory) Entities(_ context.Context, kind, room string, liveOnly bool) ([]model.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Entity{}
	for _, e := range m.entities {
		if (kind == "" || e.Kind == kind) && (room == "" || e.Room == room) && (!liveOnly || e.Live()) {
			out = append(out, clone(e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) Put(_ context.Context, e model.Entity, c model.Change) (model.Entity, model.Change, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, exists := m.entities[e.ID]
	switch {
	case !exists && e.Version != 0, exists && cur.Version != e.Version:
		return model.Entity{}, model.Change{}, model.ErrConflict
	}
	if exists {
		before := clone(cur).State
		c.Before = &before
		e.CreatedAt = cur.CreatedAt
	}
	e.Version++
	e.UpdatedAt = c.At
	if !exists {
		e.CreatedAt = c.At
	}
	c.ID = m.next()
	c.EntityID = e.ID
	c.Version = e.Version
	c.After = clone(e).State
	m.entities[e.ID] = clone(e)
	m.changes = append(m.changes, c)
	return clone(e), c, nil
}

func (m *Memory) Changes(_ context.Context, entityID string, limit int) ([]model.Change, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Change{}
	for i := len(m.changes) - 1; i >= 0 && len(out) < limit; i-- {
		if entityID == "" || m.changes[i].EntityID == entityID {
			out = append(out, m.changes[i])
		}
	}
	return out, nil
}

func (m *Memory) PutMessage(_ context.Context, msg model.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.messages[msg.Ref]; !ok {
		m.messages[msg.Ref] = msg
	}
	return nil
}

func (m *Memory) Message(_ context.Context, ref string) (model.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.messages[ref]
	if !ok {
		return model.Message{}, model.ErrNotFound
	}
	return msg, nil
}

func (m *Memory) RoomMessages(_ context.Context, room string, after, before time.Time, limit int) ([]model.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Message{}
	for _, msg := range m.messages {
		if msg.Room == room && msg.SentAt.After(after) && (before.IsZero() || msg.SentAt.Before(before)) {
			out = append(out, msg)
		}
	}
	sortMessages(out)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (m *Memory) Rooms(_ context.Context) ([]Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	by := map[string]*Room{}
	for _, msg := range m.messages {
		r := by[msg.Room]
		if r == nil {
			r = &Room{Room: msg.Room, Name: msg.RoomName, Source: msg.Source, OnlyMine: true, Interpreted: m.marks[msg.Room]}
			by[msg.Room] = r
		}
		r.Messages++
		r.OnlyMine = r.OnlyMine && msg.IsMine
		if msg.SentAt.After(r.LastAt) {
			r.LastAt = msg.SentAt
		}
		if msg.SentAt.After(r.Interpreted) {
			r.Uninterpreted++
		}
	}
	out := make([]Room, 0, len(by))
	for _, r := range by {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out, nil
}

func (m *Memory) MarkRoom(_ context.Context, room string, through time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if through.After(m.marks[room]) {
		m.marks[room] = through
	}
	return nil
}

func (m *Memory) SetTimer(_ context.Context, t model.Timer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.timers {
		if x.Skill == t.Skill && x.EntityID == t.EntityID && x.Name == t.Name {
			m.timers[i].FireAt, m.timers[i].FiredAt = t.FireAt, nil
			return nil
		}
	}
	t.ID = m.next()
	t.FiredAt = nil
	m.timers = append(m.timers, t)
	return nil
}

func (m *Memory) CancelTimers(_ context.Context, skill, entityID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timers = slices.DeleteFunc(m.timers, func(t model.Timer) bool { return t.Skill == skill && t.EntityID == entityID })
	return nil
}

func (m *Memory) DueTimers(_ context.Context, now time.Time) ([]model.Timer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Timer{}
	for _, t := range m.timers {
		if t.FiredAt == nil && !t.FireAt.After(now) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FireAt.Before(out[j].FireAt) })
	return out, nil
}

func (m *Memory) FireTimer(_ context.Context, id int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.timers {
		if m.timers[i].ID == id {
			m.timers[i].FiredAt = &at
		}
	}
	return nil
}

func (m *Memory) Timers(_ context.Context) ([]model.Timer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.timers)
	sort.Slice(out, func(i, j int) bool { return out[i].FireAt.Before(out[j].FireAt) })
	return out, nil
}

func (m *Memory) PutIntent(_ context.Context, in model.Intent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.intents[in.ID] = in
	return nil
}

func (m *Memory) Intent(_ context.Context, id string) (model.Intent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.intents[id]
	if !ok {
		return model.Intent{}, model.ErrNotFound
	}
	return in, nil
}

func (m *Memory) Intents(_ context.Context, limit int) ([]model.Intent, error) {
	return m.filter(func(model.Intent) bool { return true }, limit), nil
}

func (m *Memory) IntentsFor(_ context.Context, entityID string) ([]model.Intent, error) {
	return m.filter(func(in model.Intent) bool { return in.EntityID == entityID }, 1000), nil
}

func (m *Memory) IntentsSince(_ context.Context, t time.Time) ([]model.Intent, error) {
	return m.filter(func(in model.Intent) bool { return !in.CreatedAt.Before(t) }, 10000), nil
}

func (m *Memory) filter(keep func(model.Intent) bool, limit int) []model.Intent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Intent{}
	for _, in := range m.intents {
		if keep(in) {
			out = append(out, in)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *Memory) Cursor(_ context.Context, name string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cursors[name], nil
}

func (m *Memory) SetCursor(_ context.Context, name string, v int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cursors[name] = v
	return nil
}

func clone(e model.Entity) model.Entity {
	e.State.People = slices.Clone(e.State.People)
	return e
}

func sortMessages(ms []model.Message) {
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].SentAt.Equal(ms[j].SentAt) {
			return ms[i].Ref < ms[j].Ref
		}
		return ms[i].SentAt.Before(ms[j].SentAt)
	})
}
