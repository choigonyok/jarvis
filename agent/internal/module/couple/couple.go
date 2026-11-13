// Package couple is the couple's space in uniple beyond the calendar: the
// D-day, the bucket lists, the habit checks, the free notes and the date map,
// as the 일정 tab's 우리 shows them. The conversation reads it, and changes it
// when the operator asks - a wish added or done, an anniversary, a habit day,
// a note, a date on the map.
//
// None of it takes a card: the card is for the calendar, logins and payments,
// and these are the operator's own words written where they said. They are
// the two's, though - the partner sees each change on their phone at once,
// and a habit day checked is pushed to it - so only what was asked is written.
package couple

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

// Name is the module.
const Name = "couple"

// Module reads and writes the couple's space at calendar-svc, which holds the
// one uniple session.
type Module struct {
	url, token string
	http       *http.Client
	now        func() time.Time
}

// New reads and writes calendar-svc at url with the services' token.
func New(url, token string) *Module {
	return &Module{url: strings.TrimRight(url, "/"), token: token, http: &http.Client{Timeout: 20 * time.Second}, now: time.Now}
}

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

/* ───────────────────────────── shapes ───────────────────────────── */

type person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type space struct {
	Me          person  `json:"me"`
	Partner     *person `json:"partner"`
	Anniversary string  `json:"anniversary"`
	WeekStart   string  `json:"weekStart"`
	Blocks      []block `json:"blocks"`
}

type block struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Title   string   `json:"title"`
	DDay    []dday   `json:"dday"`
	Bucket  []wish   `json:"bucket"`
	Free    []note   `json:"free"`
	Visits  []visit  `json:"visits"`
	Records []record `json:"records"`
	Log     []any    `json:"log"`
	Food    []any    `json:"food"`
	Diary   []any    `json:"diary"`
	Budget  []any    `json:"budget"`
}

type dday struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Date   string `json:"date"`
	Start  bool   `json:"start"`
	Repeat string `json:"repeat"`
}

type wish struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type note struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	By      string `json:"by"`
}

type visit struct {
	ID     string `json:"id"`
	Region string `json:"region"`
	Asset  string `json:"asset"`
	Rep    bool   `json:"rep"`
	Record string `json:"record"`
}

type record struct {
	ID     string `json:"id"`
	Region string `json:"region"`
	Title  string `json:"title"`
	From   string `json:"from"`
	To     string `json:"to"`
	Note   string `json:"note"`
}

type check struct {
	ID   string `json:"id"`
	Date string `json:"date"`
	Memo string `json:"memo"`
	Who  string `json:"who"`
}

type news struct {
	Who        string `json:"who"`
	BlockType  string `json:"blockType"`
	BlockTitle string `json:"blockTitle"`
	Kind       string `json:"kind"`
	At         string `json:"at"`
	Item       *struct {
		Title  string `json:"title"`
		Date   string `json:"date"`
		Region string `json:"region"`
	} `json:"item"`
}

// kinds is each block type in the words the 일정 tab uses.
var kinds = map[string]string{
	"dday": "디데이", "calendar": "커플 캘린더", "workout": "습관 체크", "bucket": "버킷리스트", "map": "데이트 지도",
	"free": "자유 블록", "log": "문화 기록장", "worldmap": "세계 지도", "diary": "커플 일기장", "budget": "데이트 가계부",
	"album": "공유 앨범", "food": "맛집 기록장",
}

/* ───────────────────────────── calls ───────────────────────────── */

func (m *Module) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.url+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	res, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("커플 공간에 연결하지 못했습니다: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("커플 공간이 %d 로 응답했습니다", res.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (m *Module) space(ctx context.Context) (space, error) {
	var s space
	err := m.do(ctx, http.MethodGet, "/couple", nil, &s)
	return s, err
}

func (m *Module) block(ctx context.Context, id string) (block, error) {
	var b block
	if strings.TrimSpace(id) == "" {
		return b, fmt.Errorf("block 에 블록 id 가 필요합니다. look 의 couple 에 있습니다")
	}
	err := m.do(ctx, http.MethodGet, "/couple/blocks/"+url.PathEscape(id), nil, &b)
	return b, err
}

func (m *Module) checks(ctx context.Context, blockID, from, to string) ([]check, error) {
	var out struct {
		Checks []check `json:"checks"`
	}
	q := url.Values{"from": {from}, "to": {to}}
	err := m.do(ctx, http.MethodGet, "/couple/blocks/"+url.PathEscape(blockID)+"/checks?"+q.Encode(), nil, &out)
	return out.Checks, err
}

/* ───────────────────────────── days ───────────────────────────── */

var seoul = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Seoul"); err == nil {
		return loc
	}
	return time.FixedZone("KST", 9*3600)
}()

func (m *Module) today() string { return m.now().In(seoul).Format(time.DateOnly) }

func parse(day string) (time.Time, bool) {
	t, err := time.ParseInLocation(time.DateOnly, day, seoul)
	return t, err == nil
}

func between(a, b string) int {
	x, ok1 := parse(a)
	y, ok2 := parse(b)
	if !ok1 || !ok2 {
		return 0
	}
	return int(y.Sub(x).Hours() / 24)
}

// next is a yearly day's next turn from today, today included.
func next(date, today string) string {
	t, ok := parse(date)
	now, ok2 := parse(today)
	if !ok || !ok2 {
		return date
	}
	for y := now.Year(); y <= now.Year()+1; y++ {
		d := time.Date(y, t.Month(), t.Day(), 0, 0, 0, 0, seoul)
		if d.Month() != t.Month() { // 2월 29일은 평년에 2월 28일
			d = time.Date(y, t.Month()+1, 0, 0, 0, 0, 0, seoul)
		}
		if !d.Before(now) {
			return d.Format(time.DateOnly)
		}
	}
	return date
}

// mark is a day against today as the D-day block says it: D-3, D-DAY, D+10.
func mark(day, today string) string {
	n := between(today, day)
	switch {
	case n == 0:
		return "D-DAY"
	case n > 0:
		return fmt.Sprintf("D-%d", n)
	default:
		return fmt.Sprintf("D+%d", -n)
	}
}

// weekOf is the habit week holding today, from the space's first weekday.
func weekOf(today, start string) (string, string) {
	t, ok := parse(today)
	if !ok {
		return today, today
	}
	first := time.Sunday
	if start == "mon" {
		first = time.Monday
	}
	back := (int(t.Weekday()) - int(first) + 7) % 7
	from := t.AddDate(0, 0, -back)
	return from.Format(time.DateOnly), from.AddDate(0, 0, 6).Format(time.DateOnly)
}

func who(by string, s space) string {
	if by == "me" {
		return "운영자"
	}
	if s.Partner != nil && s.Partner.Name != "" {
		return s.Partner.Name
	}
	return "상대"
}

// typed finds a block of the space by id, with the type the change needs.
func typed(s space, id string, want ...string) (block, error) {
	for _, b := range s.Blocks {
		if b.ID != id {
			continue
		}
		for _, w := range want {
			if b.Type == w {
				return b, nil
			}
		}
		names := make([]string, len(want))
		for i, w := range want {
			names[i] = kinds[w]
		}
		return b, fmt.Errorf("%s(%s) 블록에는 할 수 없습니다. %s 블록에서 합니다", b.Title, kinds[b.Type], strings.Join(names, "·"))
	}
	return block{}, fmt.Errorf("그런 블록이 없습니다(%s). look 의 couple 로 블록 id 를 확인하세요", id)
}

/* ───────────────────────────── look ───────────────────────────── */

type LookInput struct {
	Block string `json:"block,omitempty" jsonschema:"블록 id. 비우면 공간 전체의 요약과 블록 id, 주면 그 블록의 항목과 항목 id"`
	From  string `json:"from,omitempty" jsonschema:"습관 블록의 체크를 볼 첫날 YYYY-MM-DD(기본 이번 주)"`
	To    string `json:"to,omitempty" jsonschema:"습관 블록의 체크를 볼 마지막 날 YYYY-MM-DD(기본 이번 주)"`
}

// Look puts the space on the board: one row, its blocks given by id.
func (m *Module) Look(b *tool.Board) {
	tool.Add(b, "couple", "커플 공간(uniple)의 디데이·버킷·습관 체크·자유 기록·데이트 지도. "+
		"block 을 비우면 블록 목록(id)과 요약·최근 소식, 주면 그 블록의 항목과 항목 id. 일정은 calendar 로 읽는다.",
		func(ctx context.Context, in LookInput) (string, error) {
			s, err := m.space(ctx)
			if err != nil {
				return "", err
			}
			if in.Block == "" {
				return m.summary(ctx, s), nil
			}
			b, err := typed(s, in.Block, "dday", "calendar", "workout", "bucket", "map", "free", "log", "worldmap", "diary", "budget", "album", "food")
			if err != nil {
				return "", err
			}
			return m.detail(ctx, s, b, in)
		})
}

func (m *Module) summary(ctx context.Context, s space) string {
	today := m.today()
	var out []string
	head := s.Me.Name
	if head == "" {
		head = "운영자"
	}
	if s.Partner != nil {
		head += " · 상대 " + s.Partner.Name
	}
	if s.Anniversary != "" {
		head += fmt.Sprintf(" · 만난 날 %s(D+%d)", s.Anniversary, between(s.Anniversary, today)+1)
	}
	out = append(out, head, "블록:")
	for _, b := range s.Blocks {
		line := fmt.Sprintf("- %s (%s, id %s)", b.Title, kinds[b.Type], b.ID)
		if say := m.gist(ctx, s, b, today); say != "" {
			line += ": " + say
		}
		out = append(out, line)
	}
	var feed struct {
		News []news `json:"news"`
	}
	if err := m.do(ctx, http.MethodGet, "/couple/news?limit=6", nil, &feed); err == nil && len(feed.News) > 0 {
		out = append(out, "최근 소식:")
		for _, n := range feed.News {
			out = append(out, "- "+told(n, s))
		}
	}
	return strings.Join(out, "\n")
}

// gist is what the home's card says of a block, in a line.
func (m *Module) gist(ctx context.Context, s space, b block, today string) string {
	switch b.Type {
	case "dday":
		best, at := "", ""
		for _, d := range b.DDay {
			day := d.Date
			if d.Repeat == "yearly" || d.Start {
				day = next(d.Date, today)
			}
			if day >= today && (at == "" || day < at) {
				best, at = d.Title, day
			}
		}
		if best == "" {
			return fmt.Sprintf("기념일 %d개", len(b.DDay))
		}
		return fmt.Sprintf("다음 기념일 %s %s(%s)", best, at, mark(at, today))
	case "calendar":
		return "일정은 look 의 calendar 로 읽는다"
	case "workout":
		from, to := weekOf(today, s.WeekStart)
		cs, err := m.checks(ctx, b.ID, from, to)
		if err != nil {
			return "이번 주 체크를 읽지 못했습니다"
		}
		mine, theirs := 0, 0
		for _, c := range cs {
			if c.Who == "me" {
				mine++
			} else {
				theirs++
			}
		}
		return fmt.Sprintf("이번 주(%s~%s) 운영자 %d일 · %s %d일", from[5:], to[5:], mine, who("partner", s), theirs)
	case "bucket":
		done := 0
		for _, w := range b.Bucket {
			if w.Done {
				done++
			}
		}
		return fmt.Sprintf("%d/%d 이룸", done, len(b.Bucket))
	case "free":
		return fmt.Sprintf("기록 %d개", len(b.Free))
	case "map":
		faces := map[string]bool{}
		for _, v := range b.Visits {
			if v.Asset != "" {
				faces[v.Region] = true
			}
		}
		return fmt.Sprintf("사진으로 채운 지역 %d곳(%.1f%%)", len(faces), float64(len(faces))*100/float64(len(regions.list)))
	case "worldmap":
		faces := map[string]bool{}
		for _, v := range b.Visits {
			if v.Asset != "" {
				faces[v.Region] = true
			}
		}
		return fmt.Sprintf("사진으로 채운 나라 %d곳. 바꾸는 것은 일정 탭에서", len(faces))
	case "album":
		return "사진은 일정 탭에서 보고 올린다"
	case "log", "food", "diary", "budget":
		n := len(b.Log) + len(b.Food) + len(b.Diary) + len(b.Budget)
		return fmt.Sprintf("기록 %d개(최근). 일정 탭에서 보고, 쓰기는 uniple 앱에서", n)
	}
	return "uniple 앱에서 여는 블록"
}

func (m *Module) detail(ctx context.Context, s space, b block, in LookInput) (string, error) {
	full, err := m.block(ctx, b.ID)
	if err != nil {
		return "", err
	}
	today := m.today()
	out := []string{fmt.Sprintf("%s (%s, id %s)", full.Title, kinds[full.Type], full.ID)}
	switch full.Type {
	case "dday":
		if len(full.DDay) == 0 {
			out = append(out, "기념일이 없습니다.")
		}
		for _, d := range full.DDay {
			line := fmt.Sprintf("- %s %s", d.Title, d.Date)
			switch {
			case d.Start:
				line += fmt.Sprintf(" · 만난 날(D+%d)", between(d.Date, today)+1)
			case d.Repeat == "yearly":
				n := next(d.Date, today)
				line += fmt.Sprintf(" · 매년, 다음 %s(%s)", n, mark(n, today))
			default:
				line += " · " + mark(d.Date, today)
			}
			out = append(out, line+" (id "+d.ID+")")
		}
		out = append(out, "만난 날로 세는 100일·주년은 디데이 블록이 알아서 센다.")
	case "bucket":
		if len(full.Bucket) == 0 {
			out = append(out, "버킷이 없습니다.")
		}
		for _, w := range full.Bucket {
			state := "할 것"
			if w.Done {
				state = "이룸"
			}
			out = append(out, fmt.Sprintf("- [%s] %s (id %s)", state, w.Title, w.ID))
		}
	case "free":
		if len(full.Free) == 0 {
			out = append(out, "기록이 없습니다.")
		}
		for _, n := range full.Free {
			out = append(out, fmt.Sprintf("- %s · %s (id %s)", n.Content, who(n.By, s), n.ID))
		}
	case "workout":
		from, to := in.From, in.To
		if from == "" || to == "" {
			from, to = weekOf(today, s.WeekStart)
		}
		cs, err := m.checks(ctx, full.ID, from, to)
		if err != nil {
			return "", err
		}
		out = append(out, fmt.Sprintf("%s ~ %s 체크:", from, to))
		if len(cs) == 0 {
			out = append(out, "- 체크한 날이 없습니다.")
		}
		for _, c := range cs {
			line := fmt.Sprintf("- %s %s", c.Date, who(c.Who, s))
			if c.Memo != "" {
				line += " · " + c.Memo
			}
			out = append(out, line)
		}
	case "map":
		faces := map[string]bool{}
		for _, v := range full.Visits {
			if v.Asset != "" {
				faces[v.Region] = true
			}
		}
		names := make([]string, 0, len(faces))
		for code := range faces {
			names = append(names, regions.name(code))
		}
		out = append(out, fmt.Sprintf("사진으로 채운 지역 %d곳: %s", len(faces), strings.Join(names, ", ")))
		if len(full.Records) == 0 {
			out = append(out, "데이트 기록이 없습니다.")
		}
		for _, r := range full.Records {
			line := fmt.Sprintf("- %s · %s", regions.name(r.Region), firstOf(r.Title, "제목 없는 날"))
			if r.From != "" {
				line += " · " + r.From
				if r.To != "" && r.To != r.From {
					line += "~" + r.To
				}
			}
			if r.Note != "" {
				line += " · " + r.Note
			}
			out = append(out, line+" (id "+r.ID+")")
		}
		out = append(out, "사진은 대화로 올리지 못한다. 일정 탭의 지도에서 올린다.")
	case "calendar":
		out = append(out, "일정은 look 의 calendar 로 읽고, 바꾸는 것은 일정 도구(카드)로 한다.")
	default:
		out = append(out, "대화로는 읽고 쓰지 않는 블록이다. 일정 탭에서 본다(문화·맛집·일기·가계부는 쓰기가 uniple 앱에서).")
	}
	return strings.Join(out, "\n"), nil
}

// told is one piece of news in a line.
func told(n news, s space) string {
	what := map[string]string{"": "추가", "update": "변경", "photo": "사진 추가", "memo": "메모", "record": "기록"}[n.Kind]
	if what == "" {
		what = n.Kind
	}
	parts := []string{who(n.Who, s), firstOf(n.BlockTitle, kinds[n.BlockType]), what}
	if n.Item != nil {
		if n.Item.Title != "" {
			parts = append(parts, n.Item.Title)
		}
		if n.Item.Region != "" {
			parts = append(parts, regions.name(n.Item.Region))
		}
		if n.Item.Date != "" {
			parts = append(parts, n.Item.Date)
		}
	}
	if t, err := time.Parse(time.RFC3339, n.At); err == nil {
		parts = append(parts, t.In(seoul).Format("01-02 15:04"))
	}
	return strings.Join(parts, " · ")
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
