package sensor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// Observer records an occurrence and logs that it was seen (mind.Mind).
type Observer interface {
	Observe(ctx context.Context, o model.Occurrence) error
}

// Structured reads the services that keep structured records - spending,
// photos, workouts, the calendar - into occurrences, by rule. Each source is
// read over a fixed window around now, and an occurrence is observed again
// only when it changed, so a poll costs the window, not the history.
type Structured struct {
	Store    store.Store
	Observer Observer
	World    *world.World // the calendar's entries become plans here
	Token    string
	Loc      *time.Location
	Out      *slog.Logger
	HTTP     *http.Client
	Now      func() time.Time

	SpendingURL string
	WorkoutURL  string
	CalendarURL string
	ImmichURL   string
	ImmichKey   string
	// Back is how far back a first start reads.
	Back time.Duration
}

// Run polls every source every so often until ctx ends.
func (s *Structured) Run(ctx context.Context, every time.Duration) {
	if s.HTTP == nil {
		s.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		for name, poll := range map[string]func(context.Context) error{
			"spending": s.Spending, "workout": s.Workout, "calendar": s.Calendar, "photos": s.Photos,
		} {
			if err := poll(ctx); err != nil && ctx.Err() == nil {
				s.Out.Warn("구조화 소스를 읽지 못했습니다", "source", name, "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// observe records o unless it is already known exactly as it is.
func (s *Structured) observe(ctx context.Context, o model.Occurrence) error {
	if old, err := s.Store.Occurrence(ctx, o.ID); err == nil {
		a, _ := json.Marshal(old)
		b, _ := json.Marshal(o)
		if bytes.Equal(a, b) {
			return nil
		}
	}
	return s.Observer.Observe(ctx, o)
}

// --- spending ---------------------------------------------------------------

type tx struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	AmountKrw  int64     `json:"amountKrw"`
	Merchant   string    `json:"merchant"`
	Category   string    `json:"category"`
	ApprovedAt time.Time `json:"approvedAt"`
	Status     string    `json:"status"`
	Items      []struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Quantity  int    `json:"quantity"`
		AmountKrw int64  `json:"amountKrw"`
		Category  string `json:"category"`
	} `json:"items"`
}

// Spending reads this month's and last month's charges: a payment, and
// each item in it when the marketplace order was read.
func (s *Structured) Spending(ctx context.Context) error {
	if s.SpendingURL == "" {
		return nil
	}
	now := s.Now().In(s.Loc)
	for _, m := range []time.Time{now.AddDate(0, -1, 0), now} {
		var out struct {
			Transactions []tx `json:"transactions"`
		}
		if err := s.get(ctx, s.SpendingURL+"/spending?month="+m.Format("2006-01"), &out); err != nil {
			return err
		}
		for _, t := range out.Transactions {
			if t.Status != "ok" || t.Kind != "approval" || t.AmountKrw <= 0 {
				continue
			}
			for _, o := range Purchase(t) {
				if err := s.observe(ctx, o); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Purchase is a charge as occurrences: the payment, then its items.
func Purchase(t tx) []model.Occurrence {
	ref := fmt.Sprintf("spending:%d", t.ID)
	pay := model.Occurrence{ID: ref, Kind: "purchase", At: t.ApprovedAt, Title: t.Merchant, Place: t.Merchant,
		Area: AreaOf(t.Merchant), Class: t.Category, Amount: t.AmountKrw, Ref: ref,
		Quote: fmt.Sprintf("%s %s원", t.Merchant, won(t.AmountKrw))}
	out := []model.Occurrence{pay}
	for _, it := range t.Items {
		pay.Things = append(pay.Things, it.Name)
		out = append(out, model.Occurrence{ID: fmt.Sprintf("%s#%d", ref, it.ID), Kind: "purchase.item", At: t.ApprovedAt,
			Title: it.Name, Class: it.Category, Amount: it.AmountKrw, Count: max(it.Quantity, 1), Ref: ref,
			Quote: fmt.Sprintf("%s %s원", it.Name, won(it.AmountKrw))})
	}
	out[0] = pay
	return out
}

var branch = regexp.MustCompile(`([가-힣]{2,5})(점|역점|지점)$`)

// AreaOf is the neighbourhood a merchant's name tells, if any: "GS25
// 망원점" → 망원, "스타벅스 강남역점" → 강남.
func AreaOf(merchant string) string {
	m := branch.FindStringSubmatch(strings.TrimSpace(merchant))
	if m == nil {
		return ""
	}
	return strings.TrimSuffix(m[1], "역")
}

// --- workouts ---------------------------------------------------------------

// Workout reads the workout log's sessions of the last weeks.
func (s *Structured) Workout(ctx context.Context) error {
	if s.WorkoutURL == "" {
		return nil
	}
	var book struct {
		Sessions []struct {
			ID          string `json:"id"`
			Date        string `json:"date"`
			RoutineName string `json:"routineName"`
			Exercises   []struct {
				Name string `json:"name"`
			} `json:"exercises"`
			StartedAt int64  `json:"startedAt"`
			EndedAt   *int64 `json:"endedAt"`
		} `json:"sessions"`
	}
	if err := s.get(ctx, s.WorkoutURL+"/workout", &book); err != nil {
		return err
	}
	since := s.Now().Add(-s.back())
	for _, w := range book.Sessions {
		at := time.UnixMilli(w.StartedAt)
		if w.StartedAt == 0 || at.Before(since) || w.EndedAt == nil {
			continue
		}
		until := time.UnixMilli(*w.EndedAt)
		var names []string
		for _, e := range w.Exercises {
			names = append(names, e.Name)
		}
		title := w.RoutineName
		if title == "" {
			title = "운동"
		}
		if err := s.observe(ctx, model.Occurrence{ID: "workout:" + w.ID, Kind: "workout", At: at, Until: &until, Title: title,
			Things: names, Count: len(names), Ref: "workout:" + w.ID, Quote: title}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Structured) back() time.Duration {
	if s.Back > 0 {
		return s.Back
	}
	return 21 * 24 * time.Hour
}

// --- photos -----------------------------------------------------------------

// Photos reads Immich's photos taken since the last read (a day back on a
// first start): when, which city, who is in them, favourites.
func (s *Structured) Photos(ctx context.Context) error {
	if s.ImmichURL == "" || s.ImmichKey == "" {
		return nil
	}
	cur, err := s.Store.Cursor(ctx, "sensor:photos")
	if err != nil {
		return err
	}
	since := s.Now().Add(-24 * time.Hour)
	if cur > 0 {
		since = time.UnixMilli(cur).Add(-time.Hour) // late uploads
	}
	last := since
	for page := 1; page > 0 && page < 20; {
		body, _ := json.Marshal(map[string]any{"takenAfter": since.UTC().Format(time.RFC3339), "withExif": true, "withPeople": true, "size": 500, "page": page})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.ImmichURL, "/")+"/api/search/metadata", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", s.ImmichKey)
		var out struct {
			Assets struct {
				Items    []asset `json:"items"`
				NextPage *string `json:"nextPage"`
			} `json:"assets"`
		}
		if err := s.do(req, &out); err != nil {
			return err
		}
		for _, a := range out.Assets.Items {
			o, ok := a.occurrence()
			if !ok {
				continue
			}
			if err := s.observe(ctx, o); err != nil {
				return err
			}
			if o.At.After(last) {
				last = o.At
			}
		}
		page = 0
		if out.Assets.NextPage != nil {
			page, _ = strconv.Atoi(*out.Assets.NextPage)
		}
	}
	return s.Store.SetCursor(ctx, "sensor:photos", last.UnixMilli())
}

type asset struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	FileCreatedAt string `json:"fileCreatedAt"`
	LocalDateTime string `json:"localDateTime"`
	IsFavorite    bool   `json:"isFavorite"`
	IsTrashed     bool   `json:"isTrashed"`
	IsArchived    bool   `json:"isArchived"`
	ExifInfo      *struct {
		City             *string `json:"city"`
		State            *string `json:"state"`
		DateTimeOriginal *string `json:"dateTimeOriginal"`
	} `json:"exifInfo"`
	People []struct {
		Name string `json:"name"`
	} `json:"people"`
}

func (a asset) occurrence() (model.Occurrence, bool) {
	if a.IsTrashed || a.ID == "" {
		return model.Occurrence{}, false
	}
	stamp := a.FileCreatedAt
	if a.ExifInfo != nil && a.ExifInfo.DateTimeOriginal != nil {
		stamp = *a.ExifInfo.DateTimeOriginal
	}
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return model.Occurrence{}, false
	}
	o := model.Occurrence{ID: "photo:" + a.ID, Kind: "photo", At: at, Count: 1, Ref: "immich:" + a.ID}
	if a.ExifInfo != nil && a.ExifInfo.City != nil {
		o.Area = *a.ExifInfo.City
		o.Place = *a.ExifInfo.City
		if a.ExifInfo.State != nil {
			o.Place += ", " + *a.ExifInfo.State
		}
	}
	for _, p := range a.People {
		if p.Name != "" {
			o.People = append(o.People, p.Name)
		}
	}
	if a.IsFavorite {
		o.Class = "favorite"
	}
	return o, true
}

// --- calendar ---------------------------------------------------------------

type entry struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Date    string `json:"date"`
	EndDate string `json:"endDate"`
	Start   string `json:"start"`
	End     string `json:"end"`
	Place   string `json:"place"`
	Owner   string `json:"owner"`
	Source  string `json:"source"`
}

// CalendarRoom is the room plans read from the calendar live in.
const CalendarRoom = "calendar"

// Calendar reads the calendar from a week back to two months ahead. Each
// entry is an occurrence, and - unless a plan from a conversation already
// is it - a plan in the world model, kept in step with the entry: moved
// when it moves, cancelled when it goes.
func (s *Structured) Calendar(ctx context.Context) error {
	if s.CalendarURL == "" || s.World == nil {
		return nil
	}
	now := s.Now().In(s.Loc)
	from, to := now.AddDate(0, 0, -7).Format("2006-01-02"), now.AddDate(0, 2, 0).Format("2006-01-02")
	var out struct {
		Events []entry `json:"events"`
	}
	if err := s.get(ctx, s.CalendarURL+"/events?from="+from+"&to="+to, &out); err != nil {
		return err
	}
	return s.SyncCalendar(ctx, out.Events, from, to)
}

// SyncCalendar brings the world model in step with the calendar's entries
// between from and to.
func (s *Structured) SyncCalendar(ctx context.Context, events []entry, from, to string) error {
	plans, err := s.Store.PlansOn(ctx, from, to)
	if err != nil {
		return err
	}
	byCal := map[string][]model.Entity{}
	for _, p := range plans {
		if p.State.CalendarID != "" {
			byCal[p.State.CalendarID] = append(byCal[p.State.CalendarID], p)
		}
	}
	seen := map[string]bool{}
	for _, ev := range events {
		if ev.ID == "" || ev.Date == "" {
			continue
		}
		seen[ev.ID] = true
		at, err := time.ParseInLocation("2006-01-02 15:04", ev.Date+" "+orMidnight(ev.Start), s.Loc)
		if err != nil {
			continue
		}
		if err := s.observe(ctx, model.Occurrence{ID: "calendar:" + ev.ID, Kind: "calendar", At: at, Title: ev.Title,
			Place: ev.Place, Area: world.Region(ev.Place), Class: Classify(ev.Title), People: People(ev.Title), Ref: "calendar:" + ev.ID}); err != nil {
			return err
		}
		if err := s.syncEntry(ctx, ev, byCal[ev.ID]); err != nil {
			return err
		}
	}
	for _, p := range plans {
		if p.Room == CalendarRoom && p.Live() && p.State.CalendarID != "" && !seen[p.State.CalendarID] {
			if _, _, err := s.World.Update(ctx, p.ID, func(st *model.State) bool { st.Status = model.Cancelled; return true },
				world.Meta{Actor: "sensor:calendar", Why: "캘린더에서 지워짐"}); err != nil {
				return err
			}
		}
	}
	return nil
}

// syncEntry keeps the plan an entry is in step with it. A plan from a
// conversation that is on the calendar speaks for its entry; a plan read
// from the calendar before that one was linked is then a duplicate, and
// goes.
func (s *Structured) syncEntry(ctx context.Context, ev entry, linked []model.Entity) error {
	meta := world.Meta{Actor: "sensor:calendar", Why: "캘린더"}
	var own *model.Entity
	spoken := false
	for i, p := range linked {
		switch {
		case p.Room != CalendarRoom && p.Live():
			spoken = true
		case p.Room == CalendarRoom && p.Live():
			own = &linked[i]
		}
	}
	if spoken {
		if own != nil {
			_, _, err := s.World.Update(ctx, own.ID, func(st *model.State) bool { st.Status = model.Cancelled; return true },
				world.Meta{Actor: "sensor:calendar", Why: "대화의 일정과 같은 캘린더 일정"})
			return err
		}
		return nil
	}
	if own == nil {
		_, err := s.World.Create(ctx, model.KindPlan, CalendarRoom, fromEntry(ev), meta)
		return err
	}
	_, _, err := s.World.Update(ctx, own.ID, func(st *model.State) bool {
		want := fromEntry(ev)
		want.Type, want.PlaceKind, want.Area = keep(st.Type, want.Type), keep(st.PlaceKind, want.PlaceKind), keep(st.Area, want.Area)
		if len(st.People) > 0 && len(want.People) == 0 {
			want.People = st.People
		}
		if st.Status == model.Done {
			want.Status = model.Done
		}
		*st = want
		return true
	}, meta)
	return err
}

func keep(old, new string) string {
	if new != "" {
		return new
	}
	return old
}

func orMidnight(t string) string {
	if t == "" {
		return "00:00"
	}
	return t
}

func fromEntry(ev entry) model.State {
	return model.State{Title: ev.Title, Status: model.Confirmed, Date: ev.Date, EndDate: ev.EndDate, Start: ev.Start, End: ev.End,
		Place: ev.Place, People: People(ev.Title), Shared: ev.Owner == "shared", CalendarID: ev.ID, Type: Classify(ev.Title)}
}

// classes are the words in an entry's title that say what kind of plan it
// is - a rule, since a title is a few words and a model call per entry
// would cost more than it knows.
var classes = []struct {
	kind  string
	words []string
}{
	{"캠핑", []string{"캠핑", "글램핑", "차박"}},
	{"여행", []string{"여행", "제주", "부산", "강릉", "속초", "해외", "출국", "숙소"}},
	{"데이트", []string{"데이트", "💕", "❤"}},
	{"회식", []string{"회식", "송년회", "워크샵"}},
	{"경조사", []string{"결혼식", "돌잔치", "장례", "조문"}},
	{"기념일", []string{"기념일", "생일", "주년"}},
	{"병원", []string{"병원", "치과", "진료", "검진", "의원", "약속 진료"}},
	{"운동", []string{"PT", "헬스", "운동", "필라테스", "요가", "클라이밍", "러닝"}},
	{"공연", []string{"영화", "공연", "콘서트", "뮤지컬", "전시"}},
	{"업무", []string{"회의", "미팅", "면접", "발표", "출장"}},
	{"식사", []string{"저녁", "점심", "브런치", "식사", "밥", "술"}},
	{"모임", []string{"모임", "동창", "정모"}},
	{"쇼핑", []string{"쇼핑", "장보기"}},
}

// Classify is a plan type from an entry's title.
func Classify(title string) string {
	for _, c := range classes {
		for _, w := range c.words {
			if strings.Contains(strings.ToUpper(title), strings.ToUpper(w)) {
				return c.kind
			}
		}
	}
	return "기타"
}

var withRE = regexp.MustCompile(`^(.+?)(와|과|랑|이랑|하고)\s`)

// People is who an entry's title says the plan is with: "민수·지훈과
// 저녁" → 민수, 지훈.
func People(title string) []string {
	m := withRE.FindStringSubmatch(strings.TrimSpace(title) + " ")
	if m == nil {
		return nil
	}
	var out []string
	for _, p := range regexp.MustCompile(`[·,&/ ]+`).Split(m[1], -1) {
		p = strings.TrimSpace(p)
		if r := []rune(p); len(r) >= 2 && len(r) <= 5 && regexp.MustCompile(`^[가-힣A-Za-z]+$`).MatchString(p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// --- transfers --------------------------------------------------------------

var transferRE = []*regexp.Regexp{
	regexp.MustCompile(`([가-힣A-Za-z*]{2,10})\s*님이\s*([\d,]+)\s*원을?\s*(보냈|송금)`),
	regexp.MustCompile(`([가-힣A-Za-z*]{2,10})\s*님(?:으로)?부터\s*([\d,]+)\s*원`),
	regexp.MustCompile(`입금\s*([\d,]+)\s*원\s*([가-힣A-Za-z*]{2,10})`),
}

// Transfer reads a payment app's notice of money received ("김민수님이
// 32,000원을 보냈어요") into a transfer occurrence. Only money in: a notice
// it cannot read is left alone.
func Transfer(ref string, at time.Time, text string) (model.Occurrence, bool) {
	for i, re := range transferRE {
		m := re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		who, amount := m[1], m[2]
		if i == 2 {
			who, amount = m[2], m[1]
		}
		n, err := strconv.ParseInt(strings.ReplaceAll(amount, ",", ""), 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		return model.Occurrence{ID: ref, Kind: "transfer", At: at, Title: "송금 받음", People: []string{who}, Amount: n, Ref: ref,
			Quote: strings.TrimSpace(m[0])}, true
	}
	return model.Occurrence{}, false
}

// --- http -------------------------------------------------------------------

func (s *Structured) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
	return s.do(req, out)
}

func (s *Structured) do(req *http.Request, out any) error {
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d", req.Method, req.URL.Path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func won(n int64) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}
