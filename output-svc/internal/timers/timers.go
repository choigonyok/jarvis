// Package timers keeps the pushes a console asks for at a time, each to one
// of its own person's devices: the end of a rest between sets, said on a
// phone that is locked, in a pocket or showing the music app - where the
// page's chime is never heard - and that a push buzzes even on mute.
//
// The console asks when a rest starts or changes and takes it back when the
// rest ends early or the session does. The same device asking again under
// the same tag replaces what it asked before: a nudged rest is one push, at
// its new end.
//
// They are kept in memory only. Each is minutes long at most (MaxAhead), and
// a restart that loses one costs one missed buzz - the screen still says the
// rest is over - which is not worth a table.
package timers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/output-svc/internal/push"
	"github.com/choigonyok/jarvis/output-svc/internal/store"
)

// MaxAhead is how far ahead a push can be asked for. A rest is minutes; a
// time further off is a reminder, which is mind's to keep and the inbox's to
// show.
const MaxAhead = 30 * time.Minute

// PerDevice is how many can wait for one device at once, under different tags.
const PerDevice = 4

// Late is how far in the past a time is still taken - as now: the phone's
// clock and this one need not agree to the second.
const Late = 10 * time.Second

// ttl is how long the push service keeps trying a phone out of reach: a
// rest's end told minutes late is only noise.
const ttl = time.Minute

// sendWithin bounds one push's trip to the push service.
const sendWithin = 30 * time.Second

// Ask is a push asked for: to one device, at a time.
type Ask struct {
	// Whose console asked (store.Owner, store.Guest): the console proxy's
	// word, never the request's own.
	Audience string
	// The device: its push subscription's endpoint, one of Audience's.
	Endpoint string
	At       time.Time
	// The same tag on the same device replaces the earlier one - here, and
	// as the notification's tag, on the device.
	Tag   string
	Title string
	Body  string
	// Where a tap opens: a path in the console.
	URL string
}

// Timer is one waiting, and what the console takes it back by.
type Timer struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

var (
	// ErrInvalid marks an ask the caller has to fix; the error's text is the
	// reason, in words fit to show.
	ErrInvalid = errors.New("invalid")
	// ErrNotYours is an endpoint that is not one of the asking person's devices.
	ErrNotYours = errors.New("not one of this person's devices")
	// ErrTooMany is a device with PerDevice waiting already.
	ErrTooMany = errors.New("too many waiting for one device")
)

type invalid string

func (e invalid) Error() string        { return string(e) }
func (e invalid) Is(target error) bool { return target == ErrInvalid }

// Pusher is the way out (push.Pusher).
type Pusher interface {
	// Owns says whether endpoint is one of audience's devices.
	Owns(ctx context.Context, audience, endpoint string) (bool, error)
	// SendTo pushes to that one device, if it is still theirs.
	SendTo(ctx context.Context, audience, endpoint string, p push.Payload) (bool, error)
}

type Timers struct {
	push Pusher
	log  *slog.Logger
	now  func() time.Time
	// after runs f once d has passed, and returns what stops it.
	after func(d time.Duration, f func()) (stop func() bool)

	mu      sync.Mutex
	waiting map[string]*timer // by id
}

type timer struct {
	id, audience, endpoint, tag string
	payload                     push.Payload
	stop                        func() bool
}

func New(p Pusher, log *slog.Logger) *Timers {
	return &Timers{
		push: p, log: log, now: time.Now, waiting: map[string]*timer{},
		after: func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop },
	}
}

// Schedule keeps a push for its time, in place of any the same device asked
// for under the same tag.
func (t *Timers) Schedule(ctx context.Context, a Ask) (Timer, error) {
	a, err := t.check(a)
	if err != nil {
		return Timer{}, err
	}
	// Never someone else's device: the endpoint has to be one this person
	// subscribed, not one they name.
	mine, err := t.push.Owns(ctx, a.Audience, a.Endpoint)
	if err != nil {
		return Timer{}, err
	}
	if !mine {
		return Timer{}, ErrNotYours
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	var earlier *timer
	others := 0
	for _, w := range t.waiting {
		switch {
		case w.endpoint != a.Endpoint:
		case w.tag == a.Tag:
			earlier = w
		default:
			others++
		}
	}
	if others >= PerDevice {
		return Timer{}, ErrTooMany
	}
	if earlier != nil {
		earlier.stop()
		delete(t.waiting, earlier.id)
	}
	w := &timer{
		id: newID(), audience: a.Audience, endpoint: a.Endpoint, tag: a.Tag,
		// Urgent, to wake the phone at the second; short-lived, so a phone
		// out of reach then is not told long after.
		payload: push.Payload{Title: a.Title, Body: a.Body, URL: a.URL, Tag: a.Tag, Urgent: true, TTL: ttl},
	}
	w.stop = t.after(max(a.At.Sub(t.now()), 0), func() { t.fire(w.id) })
	t.waiting[w.id] = w
	return Timer{ID: w.id, At: a.At}, nil
}

// Cancel takes back one of audience's. False when there is none by that id:
// gone off, replaced, lost to a restart - or someone else's.
func (t *Timers) Cancel(audience, id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	w, ok := t.waiting[id]
	if !ok || w.audience != who(audience) {
		return false
	}
	w.stop()
	delete(t.waiting, id)
	return true
}

// fire sends one that came due - unless it was taken back or replaced as it
// went off.
func (t *Timers) fire(id string) {
	t.mu.Lock()
	w, ok := t.waiting[id]
	delete(t.waiting, id)
	t.mu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendWithin)
	defer cancel()
	// Whose the device is, is asked again: one signed in as the other person
	// since is theirs now, and this was not for them.
	sent, err := t.push.SendTo(ctx, w.audience, w.endpoint, w.payload)
	switch {
	case err != nil:
		t.log.Warn("예약한 푸시를 보내지 못했습니다", "err", err, "tag", w.tag)
	case !sent:
		t.log.Info("예약한 푸시가 기기에 가지 않았습니다", "tag", w.tag)
	}
}

// tags are short names and never a notification's kind ("approval.pending"):
// what a timer replaces on the device is only what a timer showed there.
var tags = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// check is an ask made fit to keep, or why it is not.
func (t *Timers) check(a Ask) (Ask, error) {
	a.Audience = who(a.Audience)
	a.Title, a.Body = strings.TrimSpace(a.Title), strings.TrimSpace(a.Body)
	now := t.now()
	switch {
	case a.Endpoint == "":
		return a, invalid("endpoint 가 필요합니다")
	case !tags.MatchString(a.Tag):
		return a, invalid("tag 는 영문 소문자로 시작하는 32자 이하의 소문자·숫자·- 입니다")
	case a.Title == "":
		return a, invalid("title 이 필요합니다")
	case a.At.IsZero():
		return a, invalid("at 이 필요합니다")
	case a.At.Before(now.Add(-Late)):
		return a, invalid("이미 지난 시각입니다")
	case a.At.After(now.Add(MaxAhead)):
		return a, invalid(fmt.Sprintf("%d분보다 먼 시각에는 예약할 수 없습니다", int(MaxAhead/time.Minute)))
	}
	a.Title, a.Body, a.URL = clip(a.Title, 120), clip(a.Body, 600), clip(console(a.URL), 300)
	return a, nil
}

// console is a path in the console, or "/" for anything that could leave it
// once a browser reads it: "//host", "/\host", a tab that falls out to "//".
func console(u string) string {
	if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") ||
		strings.ContainsFunc(u, func(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f }) {
		return "/"
	}
	return u
}

// who is an audience as the store keeps it: anything not the guest is the operator's.
func who(audience string) string {
	if audience == store.Guest {
		return store.Guest
	}
	return store.Owner
}

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
