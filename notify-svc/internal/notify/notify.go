// Package notify decides what happens to an event once it arrives: stored
// always, and then pushed now, held for the night to end, gathered into the
// evening's digest, or only kept for the inbox.
//
// The tiers are the sender's call - it knows whether someone has to act - and
// the settings are the person's: a category turned off is never pushed, and
// quiet hours hold even an urgent push until morning, when everything held
// goes out as one.
package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/notify-svc/internal/push"
	"github.com/choigonyok/jarvis/notify-svc/internal/store"
)

// Event is what a service sends to POST /events.
type Event struct {
	// The service sending it: agent, status, spending, assets, jobs, calendar.
	Source string `json:"source"`
	// "category.detail" - the category is what can be turned off: collect.fail, spending.big.
	Kind string `json:"kind"`
	// now: someone has to act, push it. digest: worth knowing today, in the
	// evening summary. log: only for the inbox.
	Tier  string `json:"tier"`
	Level string `json:"level"` // info | warn | alert
	Title string `json:"title"`
	Body  string `json:"body"`
	// Where a tap opens: a path in the console.
	URL string `json:"url"`
	// The same key twice is one notification: retries and repeats are free.
	Key string `json:"key"`
}

type Settings struct {
	// Categories that are never pushed (still kept in the inbox).
	Muted []string `json:"muted"`
	// "HH:MM", Seoul time. Equal start and end means no quiet hours.
	QuietStart string `json:"quietStart"`
	QuietEnd   string `json:"quietEnd"`
	// When the evening digest goes out, "HH:MM".
	DigestAt string `json:"digestAt"`
	// The day the last digest went out - kept here so a restart does not send it twice.
	LastDigest string `json:"lastDigest,omitempty"`
}

func Defaults() Settings {
	return Settings{Muted: []string{}, QuietStart: "00:00", QuietEnd: "08:00", DigestAt: "21:00"}
}

type Store interface {
	Insert(ctx context.Context, n store.Notification) (store.Notification, bool, error)
	MarkDelivered(ctx context.Context, ids []int64) error
	Pending(ctx context.Context, tier string) ([]store.Notification, error)
	Settings(ctx context.Context, v any) error
	WriteSettings(ctx context.Context, v any) error
}

type Sender interface {
	Send(ctx context.Context, p push.Payload) (int, error)
}

type Service struct {
	store Store
	push  Sender
	loc   *time.Location
	now   func() time.Time
	// One decision at a time: an event arriving while the minute tick flushes
	// must not be pushed twice.
	mu sync.Mutex
}

func New(s Store, p Sender, loc *time.Location) *Service {
	return &Service{store: s, push: p, loc: loc, now: time.Now}
}

// ErrInvalid marks an event or settings the caller has to fix; the error's
// text is the reason, in words fit to show.
var ErrInvalid = errors.New("invalid")

type invalid string

func (e invalid) Error() string        { return string(e) }
func (e invalid) Is(target error) bool { return target == ErrInvalid }

// Accept stores an event and does what its tier says. A repeated key is
// reported as a duplicate and does nothing else.
func (s *Service) Accept(ctx context.Context, e Event) (store.Notification, bool, error) {
	n, err := normalize(e)
	if err != nil {
		return store.Notification{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	saved, dup, err := s.store.Insert(ctx, n)
	if err != nil || dup {
		return saved, dup, err
	}
	set := s.settings(ctx)
	switch {
	case saved.Tier == "log", saved.Tier == "now" && muted(set, saved.Kind):
		err = s.store.MarkDelivered(ctx, []int64{saved.ID})
	case saved.Tier == "now" && !s.quiet(set):
		err = s.deliver(ctx, []store.Notification{saved}, "")
	}
	// digest, or now during quiet hours: left pending for Tick.
	return saved, false, err
}

// Tick runs every minute: what quiet hours held goes out when they end, and
// the digest goes out once a day at its time.
func (s *Service) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := s.settings(ctx)
	if s.quiet(set) {
		return nil
	}
	held, err := s.store.Pending(ctx, "now")
	if err != nil {
		return err
	}
	if err := s.deliver(ctx, held, "밤사이 알림"); err != nil {
		return err
	}

	now := s.now().In(s.loc)
	today := now.Format("2006-01-02")
	if set.LastDigest == today || minutes(now) < clock(set.DigestAt, 21*60) {
		return nil
	}
	pending, err := s.store.Pending(ctx, "digest")
	if err != nil {
		return err
	}
	if err := s.deliver(ctx, pending, "오늘 요약"); err != nil {
		return err
	}
	set.LastDigest = today
	return s.store.WriteSettings(ctx, set)
}

// deliver pushes notifications - one as itself, several as one titled
// `group` - and marks them all delivered. Muted ones are marked without a push.
func (s *Service) deliver(ctx context.Context, ns []store.Notification, group string) error {
	if len(ns) == 0 {
		return nil
	}
	set := s.settings(ctx)
	ids := make([]int64, 0, len(ns))
	var out []store.Notification
	for _, n := range ns {
		ids = append(ids, n.ID)
		if !muted(set, n.Kind) {
			out = append(out, n)
		}
	}
	var payload push.Payload
	switch {
	case len(out) == 0:
	case len(out) == 1 || group == "":
		n := out[0]
		payload = push.Payload{Title: n.Title, Body: n.Body, URL: n.URL, Tag: n.Kind, Urgent: n.Level == "alert"}
	default:
		titles := make([]string, 0, len(out))
		urgent := false
		for _, n := range out {
			titles = append(titles, n.Title)
			urgent = urgent || n.Level == "alert"
		}
		payload = push.Payload{
			Title:  fmt.Sprintf("%s %d건", group, len(out)),
			Body:   clip(strings.Join(titles, " · "), 180),
			URL:    "/?inbox=1",
			Tag:    "group",
			Urgent: urgent,
		}
	}
	if payload.Title != "" {
		if _, err := s.push.Send(ctx, payload); err != nil {
			return err
		}
	}
	return s.store.MarkDelivered(ctx, ids)
}

func (s *Service) settings(ctx context.Context) Settings {
	set := Defaults()
	_ = s.store.Settings(ctx, &set)
	if set.Muted == nil {
		set.Muted = []string{}
	}
	return set
}

// Settings and SaveSettings are the API's view: LastDigest is kept as it was.
func (s *Service) Settings(ctx context.Context) Settings { return s.settings(ctx) }

func (s *Service) SaveSettings(ctx context.Context, in Settings) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.settings(ctx)
	for _, v := range []string{in.QuietStart, in.QuietEnd, in.DigestAt} {
		if clock(v, -1) < 0 {
			return cur, invalid("시각은 HH:MM 이어야 합니다")
		}
	}
	// A digest inside quiet hours would wait for morning and take the next
	// day's slot with it.
	start, end, at := clock(in.QuietStart, 0), clock(in.QuietEnd, 0), clock(in.DigestAt, 0)
	if start != end && ((start < end && at >= start && at < end) || (start > end && (at >= start || at < end))) {
		return cur, invalid("요약 시각이 방해 금지 시간 안에 있어요")
	}
	in.LastDigest = cur.LastDigest
	if in.Muted == nil {
		in.Muted = []string{}
	}
	return in, s.store.WriteSettings(ctx, in)
}

func (s *Service) quiet(set Settings) bool {
	start, end := clock(set.QuietStart, 0), clock(set.QuietEnd, 0)
	if start == end {
		return false
	}
	m := minutes(s.now().In(s.loc))
	if start < end {
		return m >= start && m < end
	}
	return m >= start || m < end // across midnight
}

func muted(set Settings, kind string) bool {
	cat, _, _ := strings.Cut(kind, ".")
	for _, m := range set.Muted {
		if m == cat {
			return true
		}
	}
	return false
}

func minutes(t time.Time) int { return t.Hour()*60 + t.Minute() }

// clock reads "HH:MM" as minutes since midnight, or fallback if it is not that.
func clock(v string, fallback int) int {
	var h, m int
	if len(v) != 5 || v[2] != ':' {
		return fallback
	}
	if _, err := fmt.Sscanf(v, "%02d:%02d", &h, &m); err != nil || h > 23 || m > 59 {
		return fallback
	}
	return h*60 + m
}

func normalize(e Event) (store.Notification, error) {
	e.Title = strings.TrimSpace(e.Title)
	if e.Source == "" || e.Kind == "" || e.Title == "" {
		return store.Notification{}, invalid("source, kind, title 이 필요합니다")
	}
	switch e.Tier {
	case "now", "digest", "log":
	default:
		return store.Notification{}, invalid("tier 는 now, digest, log 중 하나입니다")
	}
	switch e.Level {
	case "":
		e.Level = "info"
	case "info", "warn", "alert":
	default:
		return store.Notification{}, invalid("level 은 info, warn, alert 중 하나입니다")
	}
	if !strings.HasPrefix(e.URL, "/") || strings.HasPrefix(e.URL, "//") {
		e.URL = "/"
	}
	n := store.Notification{
		Source: clip(e.Source, 40), Kind: clip(e.Kind, 60), Tier: e.Tier, Level: e.Level,
		Title: clip(e.Title, 120), Body: clip(strings.TrimSpace(e.Body), 600), URL: clip(e.URL, 300),
	}
	if k := strings.TrimSpace(e.Key); k != "" {
		k = clip(k, 200)
		n.DedupeKey = &k
	}
	return n, nil
}

func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}
