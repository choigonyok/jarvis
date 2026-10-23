// Package notify decides what happens to an event once it arrives: stored
// always, and then pushed now, held for the night to end, gathered into the
// evening's digest, or only kept for the inbox.
//
// The tiers are the sender's call - it knows whether someone has to act - and
// the settings are the person's: a category turned off is never pushed, and
// quiet hours hold even an urgent push until morning, when everything held
// goes out as one. Driving or in a meeting (context-svc's context.state)
// holds pushes the same way, until it ends.
//
// Every notification is someone's: the operator's (owner) or the guest
// account's. Each has their own inbox, devices, settings, quiet hours and
// digest; being busy is the operator's state and holds only theirs.
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
	// Who it is for: owner (the default) or guest.
	Audience string `json:"audience,omitempty"`
}

// Category is one kind of notification a sender may use (the part of kind
// before the dot), as the settings screen lists it.
type Category struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

// Categories is the list the console shows. A new sender adds its line here,
// next to the service that delivers it, not in the screen.
var Categories = []Category{
	{"approval", "결재 대기", "에이전트가 승인을 기다릴 때"},
	{"suggestion", "AI 제안", "약속을 일정에, 비중 이탈을 리밸런싱으로 먼저 권할 때"},
	{"mind", "비서 알림", "아침 브리핑, 일정 하루 전·두 시간 전, 기한이 다가온 일"},
	{"remind", "때 맞춘 알림", "부탁한 리마인드, 집을 나설 때 비 소식처럼 그 순간에 알릴 것 - 방해 금지·운전 중에도 울림"},
	{"location", "위치", "집·회사 같은 영역에 도착하거나 떠날 때 (알림함에만)"},
	{"transit", "출발", "등록한 경로의 시간대에 '슬슬 짐 싸야 해요'·'지금 나가요' - 방해 금지·운전 중에도 울림"},
	{"job", "작업 요청", "로그인·확인이 필요할 때"},
	{"status", "수집·연결 이상", "카톡·카드 알림 수집이 멈췄을 때"},
	{"calendar", "공유 일정", "상대가 일정을 추가했을 때"},
	{"spending", "가계부", "큰 결제, 예산, 하루 지출, 분류 묻기"},
	{"assets", "자산", "비중 이탈, 입출금 감지, 하루 자산"},
	{"memory", "자비스 기억", "자비스가 기억을 새로 저장하거나 고치거나 지울 때"},
}

// GuestCategories is what the guest account is sent: their own jarvis's
// cards and jobs, their own reminders, the shared calendar as the operator's
// side changes it, and when to leave on their own routes.
var GuestCategories = []Category{
	{"approval", "결재 대기", "내 jarvis 가 승인을 기다릴 때"},
	{"remind", "때 맞춘 알림", "부탁한 리마인드, 체크리스트의 남은 것 - 방해 금지 시간에도 울림"},
	{"job", "작업 요청", "내 작업이 확인을 기다릴 때"},
	{"calendar", "공유 일정", "상대가 일정을 추가했을 때"},
	{"transit", "출발", "등록한 경로의 시간대에 '슬슬 짐 싸야 해요'·'지금 나가요' - 방해 금지 시간에도 울림"},
}

// CategoriesFor is the settings list one person is shown.
func CategoriesFor(audience string) []Category {
	if audience == store.Guest {
		return GuestCategories
	}
	return Categories
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
	Pending(ctx context.Context, audience, tier string) ([]store.Notification, error)
	Settings(ctx context.Context, audience string, v any) error
	WriteSettings(ctx context.Context, audience string, v any) error
}

type Sender interface {
	Send(ctx context.Context, audience string, p push.Payload) (int, error)
}

type Service struct {
	store Store
	push  Sender
	loc   *time.Location
	now   func() time.Time
	// One decision at a time: an event arriving while the minute tick flushes
	// must not be pushed twice.
	mu sync.Mutex
	// busyUntil: driving or in a meeting, by context-svc; pushes wait. It
	// lapses by itself in case the end is never heard.
	busyUntil time.Time
	busyWhy   string
	heldBusy  bool // what waits was held for being busy, not for the night
}

// busyLapse is how long "busy" is believed without a word that it ended.
const busyLapse = 4 * time.Hour

// Busy says the operator is driving or in a meeting (why), or no longer.
// While busy, pushes wait as in quiet hours and go out together after.
func (s *Service) Busy(on bool, why string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on {
		s.busyUntil, s.busyWhy = s.now().Add(busyLapse), why
		return
	}
	s.busyUntil, s.busyWhy = time.Time{}, ""
}

// held is whether a push to audience now would wait: their quiet hours, or
// (the operator's only) busy.
func (s *Service) held(audience string, set Settings) bool {
	return s.quiet(set) || (audience == store.Owner && s.now().Before(s.busyUntil))
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
	who := n.Audience
	set := s.settings(ctx, who)
	switch {
	case saved.Tier == "log", saved.Tier == "now" && muted(set, saved.Kind):
		err = s.store.MarkDelivered(ctx, []int64{saved.ID})
	case saved.Tier == "now" && (!s.held(who, set) || moment(saved.Kind)):
		err = s.deliver(ctx, who, []store.Notification{saved}, "")
	case saved.Tier == "now" && !s.quiet(set):
		s.heldBusy = true
	}
	// digest, or now during quiet hours or while busy: left pending for Tick.
	return saved, false, err
}

// Tick runs every minute: what quiet hours held goes out when they end, and
// the digest goes out once a day at its time - for each person on their own
// clock.
func (s *Service) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for _, who := range store.Audiences {
		errs = append(errs, s.tick(ctx, who))
	}
	return errors.Join(errs...)
}

func (s *Service) tick(ctx context.Context, who string) error {
	set := s.settings(ctx, who)
	if s.held(who, set) {
		return nil
	}
	held, err := s.store.Pending(ctx, who, "now")
	if err != nil {
		return err
	}
	group := "밤사이 알림"
	if s.heldBusy && who == store.Owner {
		group = "운전·회의 중 알림"
	}
	if err := s.deliver(ctx, who, held, group); err != nil {
		return err
	}
	if who == store.Owner {
		s.heldBusy = false
	}

	now := s.now().In(s.loc)
	today := now.Format("2006-01-02")
	if set.LastDigest == today || minutes(now) < clock(set.DigestAt, 21*60) {
		return nil
	}
	pending, err := s.store.Pending(ctx, who, "digest")
	if err != nil {
		return err
	}
	if err := s.deliver(ctx, who, pending, "오늘 요약"); err != nil {
		return err
	}
	set.LastDigest = today
	return s.store.WriteSettings(ctx, who, set)
}

// deliver pushes one person's notifications - one as itself, several as one
// titled `group` - and marks them all delivered. Muted ones are marked
// without a push.
func (s *Service) deliver(ctx context.Context, who string, ns []store.Notification, group string) error {
	if len(ns) == 0 {
		return nil
	}
	set := s.settings(ctx, who)
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
			URL:    groupURL(out),
			Tag:    "group",
			Urgent: urgent,
		}
	}
	if payload.Title != "" {
		if _, err := s.push.Send(ctx, who, payload); err != nil {
			return err
		}
	}
	return s.store.MarkDelivered(ctx, ids)
}

// groupURL is where a tap on a grouped push goes: the screen they are all
// about, if they are about one - and the inbox, to see them side by side,
// only when they are not.
func groupURL(ns []store.Notification) string {
	path := func(u string) string { p, _, _ := strings.Cut(u, "?"); return p }
	same, samePath := true, true
	for _, n := range ns[1:] {
		same = same && n.URL == ns[0].URL
		samePath = samePath && path(n.URL) == path(ns[0].URL)
	}
	switch {
	case same:
		return ns[0].URL
	case samePath:
		return path(ns[0].URL)
	}
	return "/?inbox=1"
}

func (s *Service) settings(ctx context.Context, who string) Settings {
	set := Defaults()
	_ = s.store.Settings(ctx, who, &set)
	if set.Muted == nil {
		set.Muted = []string{}
	}
	return set
}

// Settings and SaveSettings are the API's view of one person's settings:
// LastDigest is kept as it was.
func (s *Service) Settings(ctx context.Context, who string) Settings { return s.settings(ctx, who) }

func (s *Service) SaveSettings(ctx context.Context, who string, in Settings) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.settings(ctx, who)
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
	return in, s.store.WriteSettings(ctx, who, in)
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

// moment is a kind said at a moment that will not come again - a time the
// person asked for, a door they are walking out of: held till morning or
// till the drive ends, it would be too late, so neither holds it. Being
// asked for, or moving, the person is plainly not asleep. Turning the
// category off still keeps it from the phone.
func moment(kind string) bool {
	cat, _, _ := strings.Cut(kind, ".")
	// A reminder asked for, and when to leave on a route whose hours one
	// set: both are about their minute, and late is useless.
	return cat == "remind" || cat == "transit"
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
	switch e.Audience {
	case "":
		e.Audience = store.Owner
	case store.Owner, store.Guest:
	default:
		return store.Notification{}, invalid("audience 는 owner, guest 중 하나입니다")
	}
	n := store.Notification{
		Audience: e.Audience, Source: clip(e.Source, 40), Kind: clip(e.Kind, 60), Tier: e.Tier, Level: e.Level,
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
