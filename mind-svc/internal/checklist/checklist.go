// Package checklist keeps the operator's lists - what to pack for a trip,
// what to do on getting home - with which items are done.
//
// A list is said nowhere by itself. It is shown: by the conversation when
// the operator asks for it, or at a moment - a reminder can carry one, and
// when the reminder's moment comes its card shows the list as it stands,
// checks and all. What is checked is kept here, whoever checked it (a tap on
// the card, the conversation), so the next time the list is shown, and
// whatever reads it to know what is left, sees the same.
package checklist

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Statuses.
const (
	Active   = "active"
	Archived = "archived"
)

// Who changed an item.
const (
	ByCard  = "card"  // a tap on the card
	ByChat  = "chat"  // the conversation
	ByMind  = "mind"  // mind itself
	ByOther = "other" // the console, a test
)

// Item is one line of a list.
type Item struct {
	ID     string     `json:"id"`
	Text   string     `json:"text"`
	Done   bool       `json:"done"`
	DoneAt *time.Time `json:"doneAt,omitempty"`
	// By is who last checked or unchecked it.
	By string `json:"by,omitempty"`
}

// List is one checklist.
type List struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Note is a line under the title: what the list is for.
	Note string `json:"note,omitempty"`
	// Topic: one active list per topic; making another with it changes that
	// one (the camping list made twice is one list).
	Topic string `json:"topic,omitempty"`
	// Date is the day the list is for ("2026-10-24"): mind brings what is
	// left up the evening before, and closes the list once the day is past.
	Date   string `json:"date,omitempty"`
	Items  []Item `json:"items"`
	Status string `json:"status"`
	// Source is who made it: chat, mind, console.
	Source string `json:"source,omitempty"`
	// Next numbers the next item, so an item's id is never reused.
	Next      int       `json:"next"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Left is the items not done yet.
func (l List) Left() []Item {
	var out []Item
	for _, it := range l.Items {
		if !it.Done {
			out = append(out, it)
		}
	}
	return out
}

// Count is "3/7": done of all.
func (l List) Count() string {
	return fmt.Sprintf("%d/%d", len(l.Items)-len(l.Left()), len(l.Items))
}

// Line is a list in one line, for the conversation's situation and a
// tool's answer: "[chk_1a2b] 캠핑 준비물 3/7 (10/24 용) · 남은 것: 장갑, 베개".
func (l List) Line() string {
	s := "[" + l.ID + "] " + l.Title + " " + l.Count()
	if d, err := time.Parse("2006-01-02", l.Date); err == nil {
		s += fmt.Sprintf(" (%d/%d 용)", int(d.Month()), d.Day())
	}
	if left := l.Left(); len(left) > 0 {
		s += " · 남은 것: " + Names(left, 6)
	} else if len(l.Items) > 0 {
		s += " · 다 했음"
	}
	if l.Status == Archived {
		s += " (닫음)"
	}
	return s
}

// Names is items' words joined, the first n of them: "장갑, 베개 외 2개".
func Names(items []Item, n int) string {
	var words []string
	for i, it := range items {
		if i == n {
			words = append(words, fmt.Sprintf("외 %d개", len(items)-n))
			break
		}
		words = append(words, it.Text)
	}
	return strings.Join(words, ", ")
}

// Store keeps the lists.
type Store interface {
	PutList(ctx context.Context, l List) error
	List(ctx context.Context, id string) (List, error)
	// Lists is those active, and with since set those archived since then.
	Lists(ctx context.Context, since time.Time) ([]List, error)
}

var ErrNotFound = errors.New("그런 체크리스트가 없습니다")

// ErrInvalid is a list or a change the caller has to fix; its text says how.
type ErrInvalid string

func (e ErrInvalid) Error() string { return string(e) }

// Limits: a list is something to read down on a phone.
const (
	maxItems = 60
	maxText  = 120
	maxTitle = 60
)

// Service makes and changes the lists.
type Service struct {
	St  Store
	Now func() time.Time
	mu  sync.Mutex
}

// New is what a list is made with.
type New struct {
	Title  string   `json:"title"`
	Note   string   `json:"note,omitempty"`
	Topic  string   `json:"topic,omitempty"`
	Date   string   `json:"date,omitempty"`
	Items  []string `json:"items"`
	Source string   `json:"source,omitempty"`
}

// Make stores a new list - or, given the topic of a list still active, puts
// the new items on that one (what is already on it, done or not, stays).
func (s *Service) Make(ctx context.Context, n New) (List, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	title := clip(clean(n.Title), maxTitle)
	if title == "" {
		return List{}, ErrInvalid("체크리스트 이름(title)이 필요합니다")
	}
	topic := clean(n.Topic)
	date, err := day(n.Date)
	if err != nil {
		return List{}, err
	}
	if topic != "" {
		lists, err := s.St.Lists(ctx, time.Time{})
		if err != nil {
			return List{}, err
		}
		for _, l := range lists {
			if l.Topic == topic && l.Status == Active {
				l.Title = title
				if note := clean(n.Note); note != "" {
					l.Note = note
				}
				if date != "" {
					l.Date = date
				}
				if err := add(&l, n.Items); err != nil {
					return List{}, err
				}
				l.UpdatedAt = now
				return l, s.St.PutList(ctx, l)
			}
		}
	}
	source := n.Source
	if source == "" {
		source = ByChat
	}
	l := List{ID: newID(), Title: title, Note: clean(n.Note), Topic: topic, Date: date, Status: Active, Source: source,
		Items: []Item{}, Next: 1, CreatedAt: now, UpdatedAt: now}
	if err := add(&l, n.Items); err != nil {
		return List{}, err
	}
	if len(l.Items) == 0 {
		return List{}, ErrInvalid("항목(items)이 하나는 있어야 합니다")
	}
	return l, s.St.PutList(ctx, l)
}

// Change is what a change gives: every field is optional.
type Change struct {
	Title *string `json:"title,omitempty"`
	Note  *string `json:"note,omitempty"`
	// Date is the list's day; "" takes it away.
	Date *string `json:"date,omitempty"`
	// Add puts new items at the end.
	Add []string `json:"add,omitempty"`
	// Remove, Check and Uncheck name items by id, or by their words.
	Remove  []string `json:"remove,omitempty"`
	Check   []string `json:"check,omitempty"`
	Uncheck []string `json:"uncheck,omitempty"`
	// Rename is an item's new words, by id.
	Rename map[string]string `json:"rename,omitempty"`
	// By is who is changing it (ByCard, ByChat …).
	By string `json:"by,omitempty"`
	// Reopen puts an archived list back.
	Reopen bool `json:"reopen,omitempty"`
}

// Change changes a list. An item named that is not on it is an error, so
// the conversation never tells the operator it checked what it did not.
func (s *Service) Change(ctx context.Context, id string, c Change) (List, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.St.List(ctx, id)
	if err != nil {
		return l, err
	}
	now := s.Now()
	if c.Title != nil {
		t := clean(*c.Title)
		if t == "" {
			return l, ErrInvalid("이름을 비울 수는 없습니다")
		}
		l.Title = clip(t, maxTitle)
	}
	if c.Note != nil {
		l.Note = clean(*c.Note)
	}
	if c.Date != nil {
		d, err := day(*c.Date)
		if err != nil {
			return l, err
		}
		l.Date = d
	}
	for ref, text := range c.Rename {
		i, err := find(l, ref)
		if err != nil {
			return l, err
		}
		if t := clean(text); t != "" {
			l.Items[i].Text = clip(t, maxText)
		}
	}
	for _, ref := range c.Check {
		i, err := find(l, ref)
		if err != nil {
			return l, err
		}
		mark(&l.Items[i], true, c.By, now)
	}
	for _, ref := range c.Uncheck {
		i, err := find(l, ref)
		if err != nil {
			return l, err
		}
		mark(&l.Items[i], false, c.By, now)
	}
	if len(c.Remove) > 0 {
		drop := map[string]bool{}
		for _, ref := range c.Remove {
			i, err := find(l, ref)
			if err != nil {
				return l, err
			}
			drop[l.Items[i].ID] = true
		}
		l.Items = slices.DeleteFunc(l.Items, func(it Item) bool { return drop[it.ID] })
	}
	if err := add(&l, c.Add); err != nil {
		return l, err
	}
	if c.Reopen {
		l.Status = Active
	}
	l.UpdatedAt = now
	return l, s.St.PutList(ctx, l)
}

// Set checks or unchecks one item, by id - a tap on the card.
func (s *Service) Set(ctx context.Context, id, item string, done bool, by string) (List, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.St.List(ctx, id)
	if err != nil {
		return l, err
	}
	i := slices.IndexFunc(l.Items, func(it Item) bool { return it.ID == item })
	if i < 0 {
		return l, ErrInvalid("이 체크리스트에 없는 항목입니다: " + item)
	}
	if l.Items[i].Done == done {
		return l, nil
	}
	now := s.Now()
	mark(&l.Items[i], done, by, now)
	l.UpdatedAt = now
	return l, s.St.PutList(ctx, l)
}

// Archive closes a list: it is no longer brought up, and is kept a while.
func (s *Service) Archive(ctx context.Context, id string) (List, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.St.List(ctx, id)
	if err != nil {
		return l, err
	}
	if l.Status == Archived {
		return l, nil
	}
	l.Status, l.UpdatedAt = Archived, s.Now()
	return l, s.St.PutList(ctx, l)
}

// Get is one list.
func (s *Service) Get(ctx context.Context, id string) (List, error) { return s.St.List(ctx, id) }

// recent is how far back All shows the lists closed.
const recent = 14 * 24 * time.Hour

// All is the lists active, the latest changed first - and with closed,
// those closed in the last two weeks after them.
func (s *Service) All(ctx context.Context, closed bool) ([]List, error) {
	since := time.Time{}
	if closed {
		since = s.Now().Add(-recent)
	}
	ls, err := s.St.Lists(ctx, since)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(ls, func(i, j int) bool {
		if (ls[i].Status == Active) != (ls[j].Status == Active) {
			return ls[i].Status == Active
		}
		return ls[i].UpdatedAt.After(ls[j].UpdatedAt)
	})
	return ls, nil
}

// day reads a list's day: "2026-10-24", or "" for none.
func day(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return "", ErrInvalid(fmt.Sprintf("날짜(date)는 YYYY-MM-DD 입니다: %q", v))
	}
	return v, nil
}

func add(l *List, texts []string) error {
	for _, t := range texts {
		t = clean(t)
		if t == "" {
			continue
		}
		if len(l.Items) >= maxItems {
			return ErrInvalid(fmt.Sprintf("한 체크리스트에는 %d개까지입니다", maxItems))
		}
		// The same words twice is one item: making the list again adds what
		// is new, not a second "장갑".
		if slices.ContainsFunc(l.Items, func(it Item) bool { return strings.EqualFold(it.Text, t) }) {
			continue
		}
		if l.Next < 1 {
			l.Next = len(l.Items) + 1
		}
		l.Items = append(l.Items, Item{ID: fmt.Sprintf("i%d", l.Next), Text: clip(t, maxText)})
		l.Next++
	}
	return nil
}

// find is an item by id, or else by its words.
func find(l List, ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if i := slices.IndexFunc(l.Items, func(it Item) bool { return it.ID == ref }); i >= 0 {
		return i, nil
	}
	if i := slices.IndexFunc(l.Items, func(it Item) bool { return strings.EqualFold(it.Text, ref) }); i >= 0 {
		return i, nil
	}
	return -1, ErrInvalid(fmt.Sprintf("체크리스트 '%s'에 없는 항목입니다: %s", l.Title, ref))
}

func mark(it *Item, done bool, by string, now time.Time) {
	it.Done, it.By = done, by
	if done {
		at := now
		it.DoneAt = &at
	} else {
		it.DoneAt = nil
	}
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "chk_" + hex.EncodeToString(b)
}
