// Package world is the assistant's model of the operator's life: plans and
// open loops, each with the history of how it came to be as it is and on
// what grounds.
//
// Nothing watches the tables. Every write goes through World, which records
// the change and hands it to whoever listens (the attention layer) - so a
// change is noticed because it happened, not because someone polled for it.
package world

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// Meta is who changed something and why.
type Meta struct {
	Actor         string
	Evidence      []model.Evidence
	Confidence    float64
	ObservationID int64
	Why           string
	// At, when set, is the version the writer last saw: a model reading takes
	// many seconds, and what it read may have moved under it. If the entity is
	// no longer at it the write is not made.
	At int
}

// Listener hears every committed change, in order.
type Listener func(e model.Entity, c model.Change)

type World struct {
	st  store.Store
	now func() time.Time

	mu        sync.Mutex
	listeners []Listener
	len       lengths
}

func New(st store.Store, now func() time.Time) *World {
	return &World{st: st, now: now}
}

// Listen adds a listener. Listeners run on the writer's goroutine and must
// not block; the attention layer only queues.
func (w *World) Listen(l Listener) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.listeners = append(w.listeners, l)
}

func (w *World) Entity(ctx context.Context, id string) (model.Entity, error) {
	return w.st.Entity(ctx, id)
}

func (w *World) Entities(ctx context.Context, kind, room string, liveOnly bool) ([]model.Entity, error) {
	return w.st.Entities(ctx, kind, room, liveOnly)
}

// Create adds an entity.
func (w *World) Create(ctx context.Context, kind, room string, s model.State, meta Meta) (model.Entity, error) {
	e := model.Entity{ID: newID(kind), Kind: kind, Room: room, State: s}
	saved, c, err := w.st.Put(ctx, e, w.change(meta))
	if err != nil {
		return model.Entity{}, err
	}
	w.tell(saved, c)
	return saved, nil
}

// Update applies mutate to the entity's current state. mutate reports
// whether it changed anything; an update that changes nothing is not
// recorded, so repeating an observation is harmless. A write that lost a
// race is read again and re-applied.
func (w *World) Update(ctx context.Context, id string, mutate func(*model.State) bool, meta Meta) (model.Entity, bool, error) {
	for range 5 {
		e, err := w.st.Entity(ctx, id)
		if err != nil {
			return model.Entity{}, false, err
		}
		if meta.At != 0 && e.Version != meta.At {
			return e, false, nil
		}
		before := e.State
		before.People = slices.Clone(before.People)
		if !mutate(&e.State) || Same(before, e.State) {
			return e, false, nil
		}
		saved, c, err := w.st.Put(ctx, e, w.change(meta))
		if errors.Is(err, model.ErrConflict) {
			continue
		}
		if err != nil {
			return model.Entity{}, false, err
		}
		w.tell(saved, c)
		return saved, true, nil
	}
	return model.Entity{}, false, model.ErrConflict
}

func (w *World) change(meta Meta) model.Change {
	conf := meta.Confidence
	if conf == 0 {
		conf = 1
	}
	ev := meta.Evidence
	if ev == nil {
		ev = []model.Evidence{}
	}
	return model.Change{Actor: meta.Actor, Evidence: ev, Confidence: conf, ObservationID: meta.ObservationID, Why: meta.Why, At: w.now()}
}

func (w *World) tell(e model.Entity, c model.Change) {
	w.mu.Lock()
	ls := slices.Clone(w.listeners)
	w.mu.Unlock()
	for _, l := range ls {
		l(e, c)
	}
}

// Same reports whether two states say the same thing.
func Same(a, b model.State) bool {
	return a.Title == b.Title && a.Status == b.Status && a.Date == b.Date && a.EndDate == b.EndDate &&
		a.Start == b.Start && a.End == b.End && a.Place == b.Place && slices.Equal(a.People, b.People) &&
		a.Shared == b.Shared && a.Owner == b.Owner && a.Due == b.Due && a.PlanID == b.PlanID &&
		a.CalendarID == b.CalendarID && a.Note == b.Note && a.Type == b.Type && a.PlaceKind == b.PlaceKind && a.Area == b.Area
}

func newID(kind string) string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return kind + "_" + hex.EncodeToString(b)
}
