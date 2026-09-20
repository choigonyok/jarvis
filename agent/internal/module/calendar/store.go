// Package calendar is the first module: it changes the world (adds and edits
// events) and it answers questions about it (what is already booked).
package calendar

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

// Event is one entry. Times are local wall-clock strings on purpose: this is
// a personal calendar in one timezone, and storing "19:00" keeps what the
// operator typed identical to what they later read.
type Event struct {
	ID    string `json:"id"`
	Date  string `json:"date"`            // YYYY-MM-DD
	Start string `json:"start,omitempty"` // HH:MM
	End   string `json:"end,omitempty"`   // HH:MM
	Title string `json:"title"`
	Place string `json:"place,omitempty"`
	Memo  string `json:"memo,omitempty"`
	// Source records who put it here. The UI reads it; nothing branches on
	// it, because an approved AI change and a hand-typed one are equally the
	// operator's decision.
	Source    string `json:"source"` // jarvis | me
	UpdatedAt string `json:"updatedAt"`
}

const (
	SourceAgent = "jarvis"
	SourceHuman = "me"
)

// Change is what goes out on the bus when the calendar moves.
type Change struct {
	Op    string `json:"op"` // upsert | delete
	Event Event  `json:"event"`
}

var (
	datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	timePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

	ErrNotFound = errors.New("그런 일정이 없습니다")
)

// Store owns the calendar file. One process holds the whole thing under one
// mutex, so there is no read-modify-write race to design around - the problem
// a shared KV would have had does not exist here.
type Store struct {
	mu     sync.RWMutex
	path   string
	events map[string]*Event
	seq    int
	bus    *bus.Bus
}

func NewStore(path string, b *bus.Bus) *Store {
	return &Store{path: path, events: map[string]*Event{}, bus: b}
}

// Load reads the file into memory. A missing file is an empty calendar, not
// an error: first run must not need a setup step.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	body, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("캘린더 파일 읽기: %w", err)
	}
	var events []Event
	if err := json.Unmarshal(body, &events); err != nil {
		return fmt.Errorf("캘린더 파일 해석: %w", err)
	}
	for i := range events {
		e := events[i]
		s.events[e.ID] = &e
	}
	return nil
}

// save writes through a temp file and renames. A half-written calendar after
// a crash would be worse than a stale one.
func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	all := make([]Event, 0, len(s.events))
	for _, e := range s.events {
		all = append(all, *e)
	}
	sort.Slice(all, func(i, j int) bool { return less(all[i], all[j]) })

	body, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func less(a, b Event) bool {
	if a.Date != b.Date {
		return a.Date < b.Date
	}
	if a.Start != b.Start {
		// 시각 없는 종일 일정이 그날의 맨 위에 온다.
		if a.Start == "" || b.Start == "" {
			return a.Start == ""
		}
		return a.Start < b.Start
	}
	return a.ID < b.ID
}

// Range returns events with date in [from, to], both inclusive, both
// YYYY-MM-DD. An empty bound means unbounded on that side.
func (s *Store) Range(from, to string) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Event, 0, len(s.events))
	for _, e := range s.events {
		if from != "" && e.Date < from {
			continue
		}
		if to != "" && e.Date > to {
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func (s *Store) Get(id string) (Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.events[id]
	if !ok {
		return Event{}, false
	}
	return *e, true
}

// Put inserts or replaces. An empty ID means insert.
func (s *Store) Put(e Event) (Event, error) {
	if err := validate(&e); err != nil {
		return Event{}, err
	}
	s.mu.Lock()
	if e.ID == "" {
		s.seq++
		e.ID = fmt.Sprintf("e-%d-%d", time.Now().UnixMilli(), s.seq)
	}
	if e.Source == "" {
		e.Source = SourceHuman
	}
	e.UpdatedAt = time.Now().Format(time.RFC3339)
	s.events[e.ID] = &e
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		return Event{}, err
	}

	s.bus.Publish(bus.Event{Type: "calendar", Calendar: Change{Op: "upsert", Event: e}})
	return e, nil
}

func (s *Store) Delete(id string) (Event, error) {
	s.mu.Lock()
	e, ok := s.events[id]
	if !ok {
		s.mu.Unlock()
		return Event{}, ErrNotFound
	}
	gone := *e
	delete(s.events, id)
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		return Event{}, err
	}

	s.bus.Publish(bus.Event{Type: "calendar", Calendar: Change{Op: "delete", Event: gone}})
	return gone, nil
}

// validate keeps malformed dates out of storage. The model is told to send
// absolute dates; this is what makes that instruction enforceable.
func validate(e *Event) error {
	e.Title = strings.TrimSpace(e.Title)
	e.Date = strings.TrimSpace(e.Date)
	e.Start = strings.TrimSpace(e.Start)
	e.End = strings.TrimSpace(e.End)
	e.Place = strings.TrimSpace(e.Place)

	if e.Title == "" {
		return errors.New("제목이 비어 있습니다")
	}
	if !datePattern.MatchString(e.Date) {
		return fmt.Errorf("날짜는 YYYY-MM-DD 형식이어야 합니다: %q", e.Date)
	}
	if _, err := time.Parse("2006-01-02", e.Date); err != nil {
		return fmt.Errorf("없는 날짜입니다: %q", e.Date)
	}
	for _, t := range []string{e.Start, e.End} {
		if t != "" && !timePattern.MatchString(t) {
			return fmt.Errorf("시각은 HH:MM 형식이어야 합니다: %q", t)
		}
	}
	if e.Start != "" && e.End != "" && e.End < e.Start {
		return fmt.Errorf("끝나는 시각이 시작보다 빠릅니다: %s → %s", e.Start, e.End)
	}
	return nil
}
