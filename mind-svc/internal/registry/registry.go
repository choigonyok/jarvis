// Package registry is what kinds there are and what may be done with them, as
// data: rows of mind.registry, read at the start and again when one changes.
// A kind says which lane reads it, an action how far it may go, a slot how long
// what fills it stays good, a relation whether it has one answer at a time,
// and the policy row the gate's numbers.
//
// What ships with the code is the seed (seed.json): rows nobody has changed
// follow it, and a row someone changed stays theirs. Changing a row is a
// record like any other (registry.write), and the model has no way to one.
package registry

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Sections of the table.
const (
	SectionKind   = "kind"
	SectionAction = "action"
	SectionSlot   = "slot"
	SectionFact   = "fact"
	SectionPolicy = "policy"
)

// Who wrote a row.
const (
	BySeed     = "seed"
	ByOperator = "operator"
)

// Lane is how a kind is read.
type Lane string

const (
	// Deterministic: mind's own loop coming back, or asked of it. No model.
	Deterministic Lane = "deterministic"
	// Structured: an adapter's reading, by rule. No model.
	Structured Lane = "structured"
	// Interpret: read by the loop, with the model where it is words.
	Interpret Lane = "interpret"
	// Relay: nothing to read; its wording, if it has any, is passed on.
	Relay Lane = "relay"
)

// Kind is one kind of record, or every kind under a prefix ("reminder.*").
type Kind struct {
	Lane Lane `json:"lane"`
	// Guest: read on the guest's stream too.
	Guest bool `json:"guest,omitempty"`
	// Consumer is a reader of its own, for a kind that must not wait in its
	// lane's line.
	Consumer string `json:"consumer,omitempty"`
}

// Action is what a kind of action is allowed.
type Action struct {
	// Level is where it starts: ask, notify, auto.
	Level string `json:"level"`
	// Fixed: asked every time, whatever its record. It is also how a context
	// is held to asking (a trip moved for the weather).
	Fixed bool `json:"fixed,omitempty"`
	// Hand is who carries it out: mind (its own ledgers), output (an adapter's
	// fixed API), tool (a person's flow), none.
	Hand string `json:"hand,omitempty"`
}

// Slot is what has to be filled in to do something.
type Slot struct {
	Kind       string   `json:"kind"`
	Skill      string   `json:"skill"`
	Context    string   `json:"context"`
	Evidence   []string `json:"evidence,omitempty"`
	Life       string   `json:"life"`
	Secret     bool     `json:"secret,omitempty"`
	Reversible bool     `json:"reversible,omitempty"`
	Cap        string   `json:"cap,omitempty"`
}

// Fact is a relation.
type Fact struct {
	// Single: one answer at a time. A move ends the old address.
	Single bool `json:"single"`
}

// Duration is a time span written as "72h".
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Gate is the gate's numbers: what a card costs and how much a day may hold.
type Gate struct {
	Window          Duration `json:"window"`
	DailyInterrupts int      `json:"dailyInterrupts"`
	DailyCards      int      `json:"dailyCards"`
	MinConfidence   float64  `json:"minConfidence"`
	InterruptCost   float64  `json:"interruptCost"`
	QuietRaised     Duration `json:"quietRaised"`
	QuietApproved   Duration `json:"quietApproved"`
	QuietRejected   Duration `json:"quietRejected"`
}

// Row is one row of the table.
type Row struct {
	Section string          `json:"section"`
	Key     string          `json:"key"`
	Body    json.RawMessage `json:"body"`
	By      string          `json:"by"`
	At      time.Time       `json:"at"`
}

// locked are the kinds of mind's own loop: what comes back from it is read
// deterministically, whoever edits the table.
var locked = []string{"intent.answer", "execute.result", "manual.edit", "payment.requested", "timer.fired", "registry.changed"}

// Snapshot is the table as it stands, whole. It is never changed: a new one
// replaces it.
type Snapshot struct {
	kinds   map[string]Kind
	actions map[string]Action
	slots   map[string]Slot
	facts   map[string]Fact
	gate    Gate
}

// Parse reads the rows.
func Parse(rows []Row) (*Snapshot, error) {
	s := &Snapshot{kinds: map[string]Kind{}, actions: map[string]Action{}, slots: map[string]Slot{}, facts: map[string]Fact{}}
	for _, r := range rows {
		var err error
		switch r.Section {
		case SectionKind:
			var v Kind
			err = json.Unmarshal(r.Body, &v)
			s.kinds[r.Key] = v
		case SectionAction:
			var v Action
			err = json.Unmarshal(r.Body, &v)
			s.actions[r.Key] = v
		case SectionSlot:
			var v Slot
			err = json.Unmarshal(r.Body, &v)
			s.slots[r.Key] = v
		case SectionFact:
			var v Fact
			err = json.Unmarshal(r.Body, &v)
			s.facts[r.Key] = v
		case SectionPolicy:
			if r.Key == "gate" {
				err = json.Unmarshal(r.Body, &s.gate)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("레지스트리 %s/%s: %w", r.Section, r.Key, err)
		}
	}
	return s, nil
}

// Check says what is wrong with a row someone wants to write.
func Check(section, key string, body json.RawMessage) error {
	if key == "" {
		return errors.New("키가 비었습니다")
	}
	dec := func(v any) error {
		if err := json.Unmarshal(body, v); err != nil {
			return fmt.Errorf("본문을 읽지 못했습니다: %w", err)
		}
		return nil
	}
	switch section {
	case SectionKind:
		var v Kind
		if err := dec(&v); err != nil {
			return err
		}
		if !slices.Contains([]Lane{Deterministic, Structured, Interpret, Relay}, v.Lane) {
			return fmt.Errorf("없는 레인입니다: %q", v.Lane)
		}
		if slices.Contains(locked, key) && v.Lane != Deterministic {
			return fmt.Errorf("%s 는 결정적 레인으로 고정입니다", key)
		}
	case SectionAction:
		var v Action
		if err := dec(&v); err != nil {
			return err
		}
		if !slices.Contains([]string{"ask", "notify", "auto"}, v.Level) {
			return fmt.Errorf("없는 수준입니다: %q", v.Level)
		}
	case SectionSlot:
		var v Slot
		if err := dec(&v); err != nil {
			return err
		}
		if v.Kind == "" || v.Life == "" {
			return errors.New("슬롯에는 kind 와 life 가 있어야 합니다")
		}
	case SectionFact:
		var v Fact
		return dec(&v)
	case SectionPolicy:
		if key != "gate" {
			return fmt.Errorf("없는 정책입니다: %s", key)
		}
		var v Gate
		return dec(&v)
	default:
		return fmt.Errorf("없는 구역입니다: %s", section)
	}
	return nil
}

// find is the row for a kind: its own, or the nearest prefix.
func (s *Snapshot) find(kind string) (Kind, bool) {
	if v, ok := s.kinds[kind]; ok {
		return v, true
	}
	for p := kind; ; {
		i := strings.LastIndexByte(p, '.')
		if i < 0 {
			return Kind{}, false
		}
		p = p[:i]
		if v, ok := s.kinds[p+".*"]; ok {
			return v, true
		}
	}
}

// Known is whether a kind has a row.
func (s *Snapshot) Known(kind string) bool {
	_, ok := s.find(kind)
	return ok
}

// LaneOf is how a kind is read. What has no row goes to interpretation: the
// way in is wide, the way out is not.
func (s *Snapshot) LaneOf(kind string) Lane {
	if v, ok := s.find(kind); ok {
		return v.Lane
	}
	return Interpret
}

// Kinds is what a lane's reader subscribes to, on the operator's stream or the
// guest's. A kind with a reader of its own is not in its lane's.
func (s *Snapshot) Kinds(lane Lane, guest bool) []string {
	out := []string{}
	for k, v := range s.kinds {
		if v.Lane == lane && v.Consumer == "" && (!guest || v.Guest) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Action is a kind of action's row: what has none is not done.
func (s *Snapshot) Action(kind string) (Action, bool) {
	v, ok := s.actions[kind]
	return v, ok
}

// Actions is every action row.
func (s *Snapshot) Actions() map[string]Action { return s.actions }

// Slot is a slot's row.
func (s *Snapshot) Slot(name string) (Slot, bool) {
	v, ok := s.slots[name]
	return v, ok
}

// Single is whether a relation has one answer at a time.
func (s *Snapshot) Single(predicate string) bool { return s.facts[predicate].Single }

// Gate is the gate's numbers.
func (s *Snapshot) Gate() Gate { return s.gate }

//go:embed seed.json
var seedJSON []byte

// Seed is the rows that ship with the code.
func Seed() []Row {
	var sections map[string]map[string]json.RawMessage
	if err := json.Unmarshal(seedJSON, &sections); err != nil {
		panic("registry: seed.json: " + err.Error())
	}
	var out []Row
	for section, rows := range sections {
		for key, body := range rows {
			out = append(out, Row{Section: section, Key: key, Body: body, By: BySeed})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Section != out[j].Section {
			return out[i].Section < out[j].Section
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Default is the table as it ships.
func Default() *Snapshot {
	s, err := Parse(Seed())
	if err != nil {
		panic(err)
	}
	return s
}

// Live is the table as it stands now, for whoever reads it.
type Live struct{ now atomic.Pointer[Snapshot] }

// NewLive starts from s.
func NewLive(s *Snapshot) *Live {
	l := &Live{}
	l.now.Store(s)
	return l
}

// Now is the table as it stands.
func (l *Live) Now() *Snapshot { return l.now.Load() }

// Set replaces it.
func (l *Live) Set(s *Snapshot) { l.now.Store(s) }
