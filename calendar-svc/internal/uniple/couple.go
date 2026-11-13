package uniple

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

// The rest of the couple's space besides the calendar: the blocks on uniple's
// home - the D-day, bucket lists, habit checks, the date map, free notes - and
// what the partner did to them. Same session, same rules: what the app reads,
// read the same way; what it writes, written with the columns it writes.
// Columns the app's own queries do not name are not touched.

// Person is one of the two, by the nickname they gave themselves.
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Space is the couple's home: who, since when, and the blocks in their order.
type Space struct {
	Me      Person  `json:"me"`
	Partner *Person `json:"partner,omitempty"`
	// Anniversary is the day they met, YYYY-MM-DD: that day is D+1.
	Anniversary string  `json:"anniversary,omitempty"`
	WeekStart   string  `json:"weekStart"`
	Blocks      []Block `json:"blocks"`
}

// Block is one card on the home. The items that come with it depend on its
// type; a type this console does not open comes with none.
type Block struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Title  string          `json:"title"`
	Order  int             `json:"order"`
	Config json.RawMessage `json:"config,omitempty"`
	DDay   []DDay          `json:"dday,omitempty"`
	Bucket []Bucket        `json:"bucket,omitempty"`
	Free   []Free          `json:"free,omitempty"`
	// Visits are the date map's photos: on the home only each region's
	// face, in a block's detail every one.
	Visits  []Visit  `json:"visits,omitempty"`
	Records []Record `json:"records,omitempty"`
	// Archived is when a block in 보관 was put there.
	Archived string `json:"archived,omitempty"`
}

// DDay is an anniversary on the D-day block. Start marks the day they met.
type DDay struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Date     string `json:"date"`
	Start    bool   `json:"start,omitempty"`
	Repeat   string `json:"repeat,omitempty"`
	Reminder *int   `json:"reminder,omitempty"`
}

type Bucket struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Done      bool   `json:"done"`
	Order     int    `json:"order"`
	CreatedAt string `json:"createdAt"`
}

type Free struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	By        string `json:"by"` // me | partner
	Order     int    `json:"order"`
	CreatedAt string `json:"createdAt"`
}

// Visit is a photo on the date map, in a region and maybe of a record.
type Visit struct {
	ID        string  `json:"id"`
	Region    string  `json:"region"`
	Asset     string  `json:"asset"`
	Rotation  float64 `json:"rotation,omitempty"`
	Rep       bool    `json:"rep,omitempty"`
	Record    string  `json:"record,omitempty"`
	Note      string  `json:"note,omitempty"`
	CreatedAt string  `json:"createdAt"`
}

// Record is a date written down against a region.
type Record struct {
	ID        string `json:"id"`
	Region    string `json:"region"`
	Title     string `json:"title,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Note      string `json:"note,omitempty"`
	Order     int    `json:"order"`
	CreatedAt string `json:"createdAt"`
}

// Check is one person's day on a habit block.
type Check struct {
	ID   string `json:"id"`
	Date string `json:"date"`
	Memo string `json:"memo,omitempty"`
	Who  string `json:"who"` // me | partner
}

// News is something one of the two did in the space, newest first.
type News struct {
	ID         string    `json:"id"`
	Who        string    `json:"who"` // me | partner
	Block      string    `json:"block,omitempty"`
	BlockType  string    `json:"blockType,omitempty"`
	BlockTitle string    `json:"blockTitle,omitempty"`
	Kind       string    `json:"kind"`
	At         string    `json:"at"`
	ItemID     string    `json:"itemId,omitempty"`
	Item       *NewsItem `json:"item,omitempty"`
}

// NewsItem is what the news is about, as it stands now. Absent when it is gone.
type NewsItem struct {
	Title  string `json:"title,omitempty"`
	Date   string `json:"date,omitempty"`
	Time   string `json:"time,omitempty"`
	Region string `json:"region,omitempty"`
	Note   string `json:"note,omitempty"`
}

// DayPhoto is a photo pinned to a calendar day.
type DayPhoto struct {
	ID    string `json:"id"`
	Date  string `json:"date"`
	Asset string `json:"asset"`
	Event string `json:"event,omitempty"`
	Cover bool   `json:"cover,omitempty"`
}

// ErrBlock is a block that is not in the space, or not of the kind asked.
var ErrBlock = errors.New("그런 블록이 없어요")

// What a block may hold at most, as the app counts (a second bucket list,
// a second habit), and the caps on items.
var blockCap = map[string]int{"bucket": 2, "free": 2, "workout": 2}

const (
	ddayCap   = 50
	bucketCap = 500
	freeCap   = 500
	tz        = "Asia/Seoul"
)

// The blocks a space may hold, by the titles the app gives new ones.
var newBlockTitle = map[string]string{
	"dday":     "📅 디데이",
	"calendar": "🗓️ 커플 캘린더",
	"bucket":   "🪣 버킷리스트",
	"workout":  "🔥 습관 체크",
	"free":     "📝 자유 블록",
	"map":      "🗺️ 데이트 지도",
	"worldmap": "🌍 세계 지도",
	"log":      "📜 문화 기록장",
	"food":     "🍜 맛집 기록장",
	"diary":    "✍🏻 커플 일기장",
	"budget":   "💰 데이트 가계부",
	"album":    "📸 공유 앨범",
}

/* ───────────────────────────── space ───────────────────────────── */

const blockItems = "*," +
	"dday_items(id,title,date,is_start_date,recurrence,reminder_minutes)," +
	"bucket_items(id,title,is_completed,created_at,order_index)," +
	"free_items(id,content,created_at,created_by,order_index)," +
	"map_visits(id,region_code,asset_id,created_at,is_rep,rotation)"

type blockRow struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Title       string          `json:"title"`
	OrderIndex  int             `json:"order_index"`
	Config      json.RawMessage `json:"config"`
	DDayItems   []ddayRow       `json:"dday_items"`
	BucketItems []bucketRow     `json:"bucket_items"`
	FreeItems   []freeRow       `json:"free_items"`
	MapVisits   []visitRow      `json:"map_visits"`
}

type ddayRow struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	Date            string  `json:"date"`
	IsStartDate     bool    `json:"is_start_date"`
	Recurrence      *string `json:"recurrence"`
	ReminderMinutes *int    `json:"reminder_minutes"`
}

type bucketRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	IsCompleted bool   `json:"is_completed"`
	CreatedAt   string `json:"created_at"`
	OrderIndex  int    `json:"order_index"`
}

type freeRow struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	CreatedAt  string `json:"created_at"`
	CreatedBy  string `json:"created_by"`
	OrderIndex int    `json:"order_index"`
}

type visitRow struct {
	ID         string   `json:"id"`
	RegionCode string   `json:"region_code"`
	AssetID    *string  `json:"asset_id"`
	CreatedAt  string   `json:"created_at"`
	IsRep      bool     `json:"is_rep"`
	Rotation   *float64 `json:"rotation"`
	RecordID   *string  `json:"record_id"`
	Note       *string  `json:"note"`
}

type recordRow struct {
	ID         string  `json:"id"`
	RegionCode string  `json:"region_code"`
	Title      *string `json:"title"`
	DateStart  *string `json:"date_start"`
	DateEnd    *string `json:"date_end"`
	Note       *string `json:"note"`
	OrderIndex int     `json:"order_index"`
	CreatedAt  string  `json:"created_at"`
}

type checkRow struct {
	ID     string  `json:"id"`
	UserID string  `json:"user_id"`
	Date   string  `json:"date"`
	Memo   *string `json:"memo"`
}

// where finds the couple's space (the calendar's) and who is signed in.
func (c *Client) where(ctx context.Context) (space, me string, err error) {
	_, space, me, err = c.calendar(ctx)
	return space, me, err
}

// Space reads the home: the two people, the day they met, and every block
// with what the home shows of it.
func (c *Client) Space(ctx context.Context) (Space, error) {
	space, me, err := c.where(ctx)
	if err != nil {
		return Space{}, err
	}
	out := Space{Me: Person{ID: me}, WeekStart: "sun", Blocks: []Block{}}

	var people []struct {
		ID       string  `json:"id"`
		Nickname *string `json:"nickname"`
	}
	q := url.Values{"space_id": {"eq." + space}, "select": {"id,nickname"}}
	if err := c.rest(ctx, http.MethodGet, "profiles?"+q.Encode(), nil, &people); err != nil {
		return Space{}, err
	}
	for _, p := range people {
		if p.ID == me {
			out.Me.Name = str(p.Nickname)
		} else if out.Partner == nil {
			out.Partner = &Person{ID: p.ID, Name: str(p.Nickname)}
		}
	}
	if out.Partner != nil {
		c.mu.Lock()
		c.partner = out.Partner.ID
		c.mu.Unlock()
	}

	// The space's own settings: the day they met and which day a week starts.
	var settings []map[string]any
	q = url.Values{"id": {"eq." + space}, "select": {"*"}}
	if err := c.rest(ctx, http.MethodGet, "spaces?"+q.Encode(), nil, &settings); err != nil {
		return Space{}, err
	}
	if len(settings) > 0 {
		if v, ok := settings[0]["anniversary_date"].(string); ok {
			out.Anniversary = v
		}
		if v, ok := settings[0]["week_start"].(string); ok && v != "" {
			out.WeekStart = v
		}
	}

	rows, err := c.blocks(ctx, space, "")
	if err != nil {
		return Space{}, err
	}
	for _, r := range rows {
		b := r.toBlock(me)
		// The home shows each region's face; the detail reads every photo.
		if r.Type == "map" {
			b.Visits = faces(b.Visits)
		}
		out.Blocks = append(out.Blocks, b)
	}
	// The start date a D-day block keeps, when the space has none of its own.
	if out.Anniversary == "" {
		for _, b := range out.Blocks {
			for _, d := range b.DDay {
				if d.Start {
					out.Anniversary = d.Date
				}
			}
		}
	}
	return out, nil
}

// blocks reads the space's live blocks with their items, or one of them.
func (c *Client) blocks(ctx context.Context, space, id string) ([]blockRow, error) {
	q := url.Values{
		"space_id":           {"eq." + space},
		"archived_at":        {"is.null"},
		"select":             {blockItems},
		"order":              {"order_index"},
		"bucket_items.order": {"order_index.asc"},
		"free_items.order":   {"order_index.asc"},
		"map_visits.order":   {"created_at.desc"},
		"dday_items.order":   {"date.asc"},
	}
	if id != "" {
		q.Set("id", "eq."+id)
	}
	var rows []blockRow
	if err := c.rest(ctx, http.MethodGet, "blocks?"+q.Encode(), nil, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// Block reads one block whole: a map's every photo and record with it.
func (c *Client) Block(ctx context.Context, id string) (Block, error) {
	space, me, err := c.where(ctx)
	if err != nil {
		return Block{}, err
	}
	rows, err := c.blocks(ctx, space, id)
	if err != nil {
		return Block{}, err
	}
	if len(rows) == 0 {
		return Block{}, ErrBlock
	}
	b := rows[0].toBlock(me)
	if b.Type == "map" {
		if b.Visits, b.Records, err = c.mapItems(ctx, b.ID); err != nil {
			return Block{}, err
		}
	}
	return b, nil
}

func (r blockRow) toBlock(me string) Block {
	b := Block{ID: r.ID, Type: r.Type, Title: r.Title, Order: r.OrderIndex}
	if len(r.Config) > 0 && string(r.Config) != "null" {
		b.Config = r.Config
	}
	for _, d := range r.DDayItems {
		b.DDay = append(b.DDay, d.toDDay())
	}
	for _, it := range r.BucketItems {
		b.Bucket = append(b.Bucket, Bucket{ID: it.ID, Title: it.Title, Done: it.IsCompleted, Order: it.OrderIndex, CreatedAt: it.CreatedAt})
	}
	for _, it := range r.FreeItems {
		b.Free = append(b.Free, Free{ID: it.ID, Content: it.Content, By: side(it.CreatedBy, me), Order: it.OrderIndex, CreatedAt: it.CreatedAt})
	}
	for _, v := range r.MapVisits {
		b.Visits = append(b.Visits, v.toVisit())
	}
	return b
}

func (d ddayRow) toDDay() DDay {
	return DDay{ID: d.ID, Title: d.Title, Date: d.Date, Start: d.IsStartDate, Repeat: str(d.Recurrence), Reminder: d.ReminderMinutes}
}

func (v visitRow) toVisit() Visit {
	out := Visit{ID: v.ID, Region: Region(v.RegionCode), Asset: str(v.AssetID), Rep: v.IsRep, Record: str(v.RecordID), Note: str(v.Note), CreatedAt: v.CreatedAt}
	if v.Rotation != nil {
		out.Rotation = *v.Rotation
	}
	return out
}

func (r recordRow) toRecord() Record {
	return Record{ID: r.ID, Region: Region(r.RegionCode), Title: str(r.Title), From: str(r.DateStart), To: str(r.DateEnd), Note: str(r.Note), Order: r.OrderIndex, CreatedAt: r.CreatedAt}
}

func side(who, me string) string {
	if who == me {
		return "me"
	}
	return "partner"
}

// faces keeps one photo per region the way the app's card picks it: the one
// marked as the face, else the newest.
func faces(all []Visit) []Visit {
	pick := map[string]Visit{}
	var order []string
	for _, v := range all {
		if v.Asset == "" {
			continue
		}
		cur, seen := pick[v.Region]
		if !seen {
			order = append(order, v.Region)
			pick[v.Region] = v
			continue
		}
		if v.Rep && !cur.Rep {
			pick[v.Region] = v
		}
	}
	out := make([]Visit, 0, len(order))
	for _, r := range order {
		out = append(out, pick[r])
	}
	return out
}

/* ───────────────────────────── regions ───────────────────────────── */

// Region is a region code as the map draws it. Seoul's 구 are kept by the
// app as S01-S25 (the order of their statistics codes); everywhere else by
// the five-digit code itself.
func Region(code string) string {
	if len(code) == 3 && code[0] == 'S' && code[1] >= '0' && code[1] <= '9' && code[2] >= '0' && code[2] <= '9' {
		n := int(code[1]-'0')*10 + int(code[2]-'0')
		return fmt.Sprintf("11%03d", n*10)
	}
	return code
}

// stored is the code the app keeps a region under: Seoul's back to S01-S25.
func stored(region string) (string, error) {
	if len(region) != 5 || strings.Trim(region, "0123456789") != "" {
		return "", event.ValidationError{Msg: "지역 코드는 다섯 자리 숫자여야 해요"}
	}
	if strings.HasPrefix(region, "11") {
		n := 0
		fmt.Sscanf(region[2:], "%d", &n)
		if n%10 != 0 || n < 10 || n > 250 {
			return "", event.ValidationError{Msg: "서울에 없는 구예요"}
		}
		return fmt.Sprintf("S%02d", n/10), nil
	}
	return region, nil
}

/* ───────────────────────────── reads ───────────────────────────── */

func (c *Client) mapItems(ctx context.Context, block string) ([]Visit, []Record, error) {
	var visits []visitRow
	q := url.Values{
		"block_id": {"eq." + block},
		"select":   {"id,region_code,asset_id,created_at,note,is_rep,rotation,order_index,record_id"},
		"order":    {"created_at.desc,id.asc"},
	}
	if err := c.rest(ctx, http.MethodGet, "map_visits?"+q.Encode(), nil, &visits); err != nil {
		return nil, nil, err
	}
	var records []recordRow
	q = url.Values{
		"block_id": {"eq." + block},
		"select":   {"id,region_code,title,date_start,date_end,note,order_index,created_at"},
	}
	if err := c.rest(ctx, http.MethodGet, "map_records?"+q.Encode(), nil, &records); err != nil {
		return nil, nil, err
	}
	vs := make([]Visit, 0, len(visits))
	for _, v := range visits {
		vs = append(vs, v.toVisit())
	}
	rs := make([]Record, 0, len(records))
	for _, r := range records {
		rs = append(rs, r.toRecord())
	}
	// Newest date first, the way the region's list reads.
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i].From, rs[j].From
		if a == "" {
			a = rs[i].CreatedAt
		}
		if b == "" {
			b = rs[j].CreatedAt
		}
		if a != b {
			return a > b
		}
		return rs[i].CreatedAt > rs[j].CreatedAt
	})
	return vs, rs, nil
}

// Checks reads a habit block's checks between two days, both people's.
func (c *Client) Checks(ctx context.Context, block, from, to string) ([]Check, error) {
	_, me, err := c.where(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := c.typed(ctx, block, "workout"); err != nil {
		return nil, err
	}
	if !day(from) || !day(to) {
		return nil, event.ValidationError{Msg: "from 과 to 는 YYYY-MM-DD 여야 해요"}
	}
	var rows []checkRow
	q := url.Values{"block_id": {"eq." + block}, "select": {"id,user_id,date,memo"}, "order": {"date.asc"}}
	q.Add("date", "gte."+from)
	q.Add("date", "lte."+to)
	if err := c.rest(ctx, http.MethodGet, "workout_checks?"+q.Encode(), nil, &rows); err != nil {
		return nil, err
	}
	out := make([]Check, 0, len(rows))
	for _, r := range rows {
		out = append(out, Check{ID: r.ID, Date: r.Date, Memo: str(r.Memo), Who: side(r.UserID, me)})
	}
	return out, nil
}

// DayPhotos reads the photos pinned to calendar days between two days.
func (c *Client) DayPhotos(ctx context.Context, from, to string) ([]DayPhoto, error) {
	block, _, _, err := c.calendar(ctx)
	if err != nil {
		return nil, err
	}
	if !day(from) || !day(to) {
		return nil, event.ValidationError{Msg: "from 과 to 는 YYYY-MM-DD 여야 해요"}
	}
	q := url.Values{
		"block_id": {"eq." + block},
		"select":   {"id,date,asset_id,event_id,is_cover,sort_order,created_at"},
		"order":    {"date.asc,is_cover.desc,sort_order.asc,created_at.asc"},
	}
	q.Add("date", "gte."+from)
	q.Add("date", "lte."+to)
	return c.dayPhotos(ctx, q)
}

// Cover is the photo the calendar card shows for a day: the last one on or
// before it, else the first after - as the app picks.
func (c *Client) Cover(ctx context.Context, date string) (*DayPhoto, error) {
	block, _, _, err := c.calendar(ctx)
	if err != nil {
		return nil, err
	}
	if !day(date) {
		return nil, event.ValidationError{Msg: "date 는 YYYY-MM-DD 여야 해요"}
	}
	for _, q := range []url.Values{
		{"block_id": {"eq." + block}, "date": {"lte." + date}, "order": {"date.desc,is_cover.desc,sort_order.asc,created_at.asc"}},
		{"block_id": {"eq." + block}, "date": {"gt." + date}, "order": {"date.asc,is_cover.desc,sort_order.asc,created_at.asc"}},
	} {
		q.Set("select", "id,date,asset_id,event_id,is_cover,sort_order,created_at")
		q.Set("limit", "1")
		got, err := c.dayPhotos(ctx, q)
		if err != nil {
			return nil, err
		}
		if len(got) > 0 {
			return &got[0], nil
		}
	}
	return nil, nil
}

func (c *Client) dayPhotos(ctx context.Context, q url.Values) ([]DayPhoto, error) {
	var rows []struct {
		ID      string  `json:"id"`
		Date    string  `json:"date"`
		AssetID string  `json:"asset_id"`
		EventID *string `json:"event_id"`
		IsCover bool    `json:"is_cover"`
	}
	if err := c.rest(ctx, http.MethodGet, "calendar_day_photos?"+q.Encode(), nil, &rows); err != nil {
		return nil, err
	}
	out := make([]DayPhoto, 0, len(rows))
	for _, r := range rows {
		out = append(out, DayPhoto{ID: r.ID, Date: r.Date, Asset: r.AssetID, Event: str(r.EventID), Cover: r.IsCover})
	}
	return out, nil
}

// News reads what was done in the space, newest first, with what each was
// about as it stands now.
func (c *Client) News(ctx context.Context, limit int) ([]News, error) {
	space, me, err := c.where(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	var rows []struct {
		ID        string  `json:"id"`
		ActorID   *string `json:"actor_id"`
		BlockID   *string `json:"block_id"`
		BlockType *string `json:"block_type"`
		Kind      string  `json:"kind"`
		ItemID    *string `json:"item_id"`
		EventTime *string `json:"event_time"`
		CreatedAt string  `json:"created_at"`
	}
	q := url.Values{
		"space_id": {"eq." + space},
		"select":   {"id,actor_id,block_id,block_type,kind,item_id,event_time,created_at"},
		"order":    {"created_at.desc"},
		"limit":    {fmt.Sprint(limit)},
	}
	if err := c.rest(ctx, http.MethodGet, "notifications?"+q.Encode(), nil, &rows); err != nil {
		return nil, err
	}

	titles := map[string]string{}
	if blocks, err := c.blocks(ctx, space, ""); err == nil {
		for _, b := range blocks {
			titles[b.ID] = b.Title
		}
	}

	// What each is about, read once per table.
	ids := map[string][]string{}
	for _, r := range rows {
		// Only ids go into the filter below: an item id that is not one is not read.
		if r.ItemID != nil && r.BlockType != nil && uuidLike(*r.ItemID) {
			ids[*r.BlockType] = append(ids[*r.BlockType], *r.ItemID)
		}
	}
	items := c.newsItems(ctx, ids)

	out := make([]News, 0, len(rows))
	for _, r := range rows {
		n := News{ID: r.ID, Who: side(str(r.ActorID), me), Block: str(r.BlockID), BlockType: str(r.BlockType), Kind: r.Kind, At: r.CreatedAt}
		if r.EventTime != nil && *r.EventTime != "" {
			n.At = *r.EventTime
		}
		n.BlockTitle = titles[n.Block]
		if r.ItemID != nil {
			n.ItemID = *r.ItemID
			if it, ok := items[*r.ItemID]; ok {
				n.Item = &it
			} else if day(*r.ItemID) {
				// Some news is about a day rather than a row: the day itself.
				n.Item = &NewsItem{Date: *r.ItemID}
			}
		}
		out = append(out, n)
	}
	return out, nil
}

// newsItems reads the items news is about. A table that cannot be read leaves
// its news without detail rather than failing the list.
func (c *Client) newsItems(ctx context.Context, ids map[string][]string) map[string]NewsItem {
	out := map[string]NewsItem{}
	in := func(list []string) string { return "in.(" + strings.Join(list, ",") + ")" }
	read := func(table, cols string, list []string, into any) bool {
		if len(list) == 0 {
			return false
		}
		q := url.Values{"id": {in(list)}, "select": {cols}}
		return c.rest(ctx, http.MethodGet, table+"?"+q.Encode(), nil, into) == nil
	}

	var events []struct {
		ID        string  `json:"id"`
		Title     string  `json:"title"`
		StartDate string  `json:"start_date"`
		StartTime *string `json:"start_time"`
	}
	if read("calendar_events", "id,title,start_date,start_time", ids["calendar"], &events) {
		for _, e := range events {
			out[e.ID] = NewsItem{Title: e.Title, Date: e.StartDate, Time: hm(e.StartTime)}
		}
	}
	// A photo put on a calendar day is the day's, not an entry's.
	var dayPhotos []struct {
		ID   string `json:"id"`
		Date string `json:"date"`
	}
	var unseen []string
	for _, id := range ids["calendar"] {
		if _, ok := out[id]; !ok {
			unseen = append(unseen, id)
		}
	}
	if read("calendar_day_photos", "id,date", unseen, &dayPhotos) {
		for _, p := range dayPhotos {
			out[p.ID] = NewsItem{Date: p.Date}
		}
	}
	var records []recordRow
	if read("map_records", "id,region_code,title,date_start,date_end,note,order_index,created_at", ids["map"], &records) {
		for _, r := range records {
			out[r.ID] = NewsItem{Title: str(r.Title), Date: str(r.DateStart), Region: Region(r.RegionCode), Note: str(r.Note)}
		}
	}
	var visits []visitRow
	if read("map_visits", "id,region_code,note,created_at", ids["map"], &visits) {
		for _, v := range visits {
			if _, ok := out[v.ID]; !ok {
				out[v.ID] = NewsItem{Region: Region(v.RegionCode), Note: str(v.Note)}
			}
		}
	}
	var buckets []bucketRow
	if read("bucket_items", "id,title", ids["bucket"], &buckets) {
		for _, b := range buckets {
			out[b.ID] = NewsItem{Title: b.Title}
		}
	}
	var ddays []ddayRow
	if read("dday_items", "id,title,date", ids["dday"], &ddays) {
		for _, d := range ddays {
			out[d.ID] = NewsItem{Title: d.Title, Date: d.Date}
		}
	}
	var checks []checkRow
	if read("workout_checks", "id,date,memo", ids["workout"], &checks) {
		for _, k := range checks {
			out[k.ID] = NewsItem{Date: k.Date, Note: str(k.Memo)}
		}
	}
	var frees []freeRow
	if read("free_items", "id,content", ids["free"], &frees) {
		for _, f := range frees {
			out[f.ID] = NewsItem{Title: f.Content}
		}
	}
	return out
}

/* ───────────────────────────── blocks ───────────────────────────── */

// typed is a block of the space of the given type.
func (c *Client) typed(ctx context.Context, id, kind string) (string, error) {
	space, _, err := c.where(ctx)
	if err != nil {
		return "", err
	}
	var rows []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	q := url.Values{"id": {"eq." + id}, "space_id": {"eq." + space}, "archived_at": {"is.null"}, "select": {"id,type"}}
	if err := c.rest(ctx, http.MethodGet, "blocks?"+q.Encode(), nil, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 || (kind != "" && rows[0].Type != kind) {
		return "", ErrBlock
	}
	return space, nil
}

// AddBlock puts a new block at the end of the home.
func (c *Client) AddBlock(ctx context.Context, kind, title string) (Block, error) {
	space, me, err := c.where(ctx)
	if err != nil {
		return Block{}, err
	}
	def, ok := newBlockTitle[kind]
	if !ok {
		return Block{}, event.ValidationError{Msg: "그런 블록은 없어요"}
	}
	if title = strings.TrimSpace(title); title == "" {
		title = def
	}
	if utf8.RuneCountInString(title) > 40 {
		return Block{}, event.ValidationError{Msg: "블록 이름은 40자까지예요"}
	}
	rows, err := c.blocks(ctx, space, "")
	if err != nil {
		return Block{}, err
	}
	n, last := 0, -1
	for _, b := range rows {
		if b.Type == kind {
			n++
		}
		if b.OrderIndex > last {
			last = b.OrderIndex
		}
	}
	if limit := blockCap[kind]; n >= max(limit, 1) {
		return Block{}, event.ValidationError{Msg: fmt.Sprintf("이 블록은 %d개까지 둘 수 있어요", max(limit, 1))}
	}
	var saved []blockRow
	body := map[string]any{"space_id": space, "type": kind, "title": title, "order_index": last + 1, "is_default": false}
	if err := c.rest(ctx, http.MethodPost, "blocks?select=*", body, &saved); err != nil {
		return Block{}, err
	}
	if len(saved) == 0 {
		return Block{}, errors.New("uniple 이 만든 블록을 돌려주지 않았습니다")
	}
	return saved[0].toBlock(me), nil
}

// RenameBlock changes a block's title.
func (c *Client) RenameBlock(ctx context.Context, id, title string) error {
	if _, err := c.typed(ctx, id, ""); err != nil {
		return err
	}
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 40 {
		return event.ValidationError{Msg: "블록 이름은 1자에서 40자까지예요"}
	}
	q := url.Values{"id": {"eq." + id}}
	return c.rest(ctx, http.MethodPatch, "blocks?"+q.Encode(), map[string]any{"title": title}, nil)
}

/* ───────────────────────────── D-day ───────────────────────────── */

// DDayInput is an anniversary as a person writes it. Reminder is minutes
// before the day starts; nil leaves it, -1 takes it off.
type DDayInput struct {
	Title    *string `json:"title"`
	Date     *string `json:"date"`
	Repeat   *string `json:"repeat"`
	Reminder *int    `json:"reminder"`
}

func (in DDayInput) fields(isNew bool) (map[string]any, error) {
	f := map[string]any{}
	if in.Title != nil || isNew {
		t := strings.TrimSpace(deref(in.Title))
		if t == "" || utf8.RuneCountInString(t) > 40 {
			return nil, event.ValidationError{Msg: "기념일 이름은 1자에서 40자까지예요"}
		}
		f["title"] = t
	}
	if in.Date != nil || isNew {
		d := strings.TrimSpace(deref(in.Date))
		if !day(d) {
			return nil, event.ValidationError{Msg: "날짜는 YYYY-MM-DD 여야 해요"}
		}
		f["date"] = d
	}
	if in.Repeat != nil {
		switch *in.Repeat {
		case "", "none":
			f["recurrence"] = nil
		case "yearly":
			f["recurrence"] = "yearly"
		default:
			return nil, event.ValidationError{Msg: "기념일 반복은 안 함이나 매년이에요"}
		}
	}
	if in.Reminder != nil {
		switch m := *in.Reminder; {
		case m < 0:
			f["reminder_minutes"], f["reminder_tz"] = nil, nil
		case m > 60*24*28:
			return nil, event.ValidationError{Msg: "알림은 4주 전까지예요"}
		default:
			f["reminder_minutes"], f["reminder_tz"] = m, tz
		}
	}
	return f, nil
}

// AddDDay adds an anniversary, and has the calendar show it.
func (c *Client) AddDDay(ctx context.Context, block string, in DDayInput) (DDay, error) {
	space, err := c.typed(ctx, block, "dday")
	if err != nil {
		return DDay{}, err
	}
	_, me, _ := c.where(ctx)
	f, err := in.fields(true)
	if err != nil {
		return DDay{}, err
	}
	var have []struct {
		ID string `json:"id"`
	}
	if err := c.rest(ctx, http.MethodGet, "dday_items?"+url.Values{"block_id": {"eq." + block}, "select": {"id"}}.Encode(), nil, &have); err != nil {
		return DDay{}, err
	}
	if len(have) >= ddayCap {
		return DDay{}, event.ValidationError{Msg: fmt.Sprintf("기념일은 %d개까지 둘 수 있어요", ddayCap)}
	}
	f["id"], f["block_id"], f["space_id"], f["created_by"], f["is_start_date"] = newUUID(), block, space, me, false
	var saved []ddayRow
	if err := c.rest(ctx, http.MethodPost, "dday_items?select=id,title,date,is_start_date,recurrence,reminder_minutes", f, &saved); err != nil {
		return DDay{}, err
	}
	if len(saved) == 0 {
		return DDay{}, errors.New("uniple 이 저장한 기념일을 돌려주지 않았습니다")
	}
	c.syncDDays(ctx, space)
	return saved[0].toDDay(), nil
}

// SetDDay changes an anniversary. The day they met is not one of these.
func (c *Client) SetDDay(ctx context.Context, block, id string, in DDayInput) (DDay, error) {
	space, err := c.typed(ctx, block, "dday")
	if err != nil {
		return DDay{}, err
	}
	f, err := in.fields(false)
	if err != nil {
		return DDay{}, err
	}
	if len(f) == 0 {
		return DDay{}, event.ValidationError{Msg: "바꿀 것이 없어요"}
	}
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "is_start_date": {"eq.false"}, "select": {"id,title,date,is_start_date,recurrence,reminder_minutes"}}
	var saved []ddayRow
	if err := c.rest(ctx, http.MethodPatch, "dday_items?"+q.Encode(), f, &saved); err != nil {
		return DDay{}, err
	}
	if len(saved) == 0 {
		return DDay{}, event.ErrNotFound
	}
	c.syncDDays(ctx, space)
	return saved[0].toDDay(), nil
}

// DeleteDDay takes an anniversary off, and off the calendar.
func (c *Client) DeleteDDay(ctx context.Context, block, id string) error {
	space, err := c.typed(ctx, block, "dday")
	if err != nil {
		return err
	}
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "is_start_date": {"eq.false"}, "select": {"id"}}
	var gone []struct {
		ID string `json:"id"`
	}
	if err := c.rest(ctx, http.MethodDelete, "dday_items?"+q.Encode(), nil, &gone); err != nil {
		return err
	}
	if len(gone) == 0 {
		return event.ErrNotFound
	}
	c.syncDDays(ctx, space)
	return nil
}

// syncDDays has uniple rewrite the calendar's anniversary entries, as the app
// does after every change. A failure leaves the calendar a step behind, not
// the anniversary unsaved.
func (c *Client) syncDDays(ctx context.Context, space string) {
	_ = c.rest(ctx, http.MethodPost, "rpc/sync_dday_calendar_events",
		map[string]any{"p_space_id": space, "p_tz": tz, "p_locale": "ko"}, nil)
}

/* ───────────────────────────── bucket ───────────────────────────── */

// AddBucket puts a wish at the end of a bucket list.
func (c *Client) AddBucket(ctx context.Context, block, title string) (Bucket, error) {
	space, err := c.typed(ctx, block, "bucket")
	if err != nil {
		return Bucket{}, err
	}
	_, me, _ := c.where(ctx)
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 100 {
		return Bucket{}, event.ValidationError{Msg: "버킷은 1자에서 100자까지예요"}
	}
	var have []bucketRow
	q := url.Values{"block_id": {"eq." + block}, "select": {"id,order_index"}, "order": {"order_index.desc"}}
	if err := c.rest(ctx, http.MethodGet, "bucket_items?"+q.Encode(), nil, &have); err != nil {
		return Bucket{}, err
	}
	if len(have) >= bucketCap {
		return Bucket{}, event.ValidationError{Msg: fmt.Sprintf("버킷은 %d개까지예요", bucketCap)}
	}
	next := 0
	if len(have) > 0 {
		next = have[0].OrderIndex + 1
	}
	body := map[string]any{"id": newUUID(), "block_id": block, "space_id": space, "title": title, "is_completed": false, "order_index": next, "created_by": me}
	var saved []bucketRow
	if err := c.rest(ctx, http.MethodPost, "bucket_items?select=id,title,is_completed,created_at,order_index", body, &saved); err != nil {
		return Bucket{}, err
	}
	if len(saved) == 0 {
		return Bucket{}, errors.New("uniple 이 저장한 버킷을 돌려주지 않았습니다")
	}
	s := saved[0]
	return Bucket{ID: s.ID, Title: s.Title, Done: s.IsCompleted, Order: s.OrderIndex, CreatedAt: s.CreatedAt}, nil
}

// BucketInput is what changes on a wish.
type BucketInput struct {
	Title *string `json:"title"`
	Done  *bool   `json:"done"`
}

// SetBucket marks a wish done or not, or renames it.
func (c *Client) SetBucket(ctx context.Context, block, id string, in BucketInput) (Bucket, error) {
	if _, err := c.typed(ctx, block, "bucket"); err != nil {
		return Bucket{}, err
	}
	f := map[string]any{}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" || utf8.RuneCountInString(t) > 100 {
			return Bucket{}, event.ValidationError{Msg: "버킷은 1자에서 100자까지예요"}
		}
		f["title"] = t
	}
	if in.Done != nil {
		f["is_completed"] = *in.Done
	}
	if len(f) == 0 {
		return Bucket{}, event.ValidationError{Msg: "바꿀 것이 없어요"}
	}
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "select": {"id,title,is_completed,created_at,order_index"}}
	var saved []bucketRow
	if err := c.rest(ctx, http.MethodPatch, "bucket_items?"+q.Encode(), f, &saved); err != nil {
		return Bucket{}, err
	}
	if len(saved) == 0 {
		return Bucket{}, event.ErrNotFound
	}
	s := saved[0]
	return Bucket{ID: s.ID, Title: s.Title, Done: s.IsCompleted, Order: s.OrderIndex, CreatedAt: s.CreatedAt}, nil
}

// DeleteBucket takes a wish off the list.
func (c *Client) DeleteBucket(ctx context.Context, block, id string) error {
	if _, err := c.typed(ctx, block, "bucket"); err != nil {
		return err
	}
	return c.deleteOne(ctx, "bucket_items", block, id, nil)
}

/* ───────────────────────────── free ───────────────────────────── */

// AddFree writes a note at the end of a free block.
func (c *Client) AddFree(ctx context.Context, block, content string) (Free, error) {
	space, err := c.typed(ctx, block, "free")
	if err != nil {
		return Free{}, err
	}
	_, me, _ := c.where(ctx)
	content = strings.TrimSpace(content)
	if content == "" || utf8.RuneCountInString(content) > 500 {
		return Free{}, event.ValidationError{Msg: "기록은 1자에서 500자까지예요"}
	}
	var have []freeRow
	q := url.Values{"block_id": {"eq." + block}, "select": {"id,order_index"}, "order": {"order_index.desc"}}
	if err := c.rest(ctx, http.MethodGet, "free_items?"+q.Encode(), nil, &have); err != nil {
		return Free{}, err
	}
	if len(have) >= freeCap {
		return Free{}, event.ValidationError{Msg: fmt.Sprintf("기록은 %d개까지예요", freeCap)}
	}
	next := 0
	if len(have) > 0 {
		next = have[0].OrderIndex + 1
	}
	body := map[string]any{"id": newUUID(), "block_id": block, "space_id": space, "content": content, "created_by": me, "order_index": next}
	var saved []freeRow
	if err := c.rest(ctx, http.MethodPost, "free_items?select=id,content,created_at,created_by,order_index", body, &saved); err != nil {
		return Free{}, err
	}
	if len(saved) == 0 {
		return Free{}, errors.New("uniple 이 저장한 기록을 돌려주지 않았습니다")
	}
	s := saved[0]
	return Free{ID: s.ID, Content: s.Content, By: side(s.CreatedBy, me), Order: s.OrderIndex, CreatedAt: s.CreatedAt}, nil
}

// SetFree rewrites a note's words.
func (c *Client) SetFree(ctx context.Context, block, id, content string) (Free, error) {
	if _, err := c.typed(ctx, block, "free"); err != nil {
		return Free{}, err
	}
	_, me, _ := c.where(ctx)
	content = strings.TrimSpace(content)
	if content == "" || utf8.RuneCountInString(content) > 500 {
		return Free{}, event.ValidationError{Msg: "기록은 1자에서 500자까지예요"}
	}
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "select": {"id,content,created_at,created_by,order_index"}}
	var saved []freeRow
	if err := c.rest(ctx, http.MethodPatch, "free_items?"+q.Encode(), map[string]any{"content": content}, &saved); err != nil {
		return Free{}, err
	}
	if len(saved) == 0 {
		return Free{}, event.ErrNotFound
	}
	f := saved[0]
	return Free{ID: f.ID, Content: f.Content, By: side(f.CreatedBy, me), Order: f.OrderIndex, CreatedAt: f.CreatedAt}, nil
}

// OrderItems puts a bucket or free block's items in the given order: every
// item, each once. Only the ones whose place changed are written.
func (c *Client) OrderItems(ctx context.Context, block, kind string, ids []string) error {
	table := map[string]string{"bucket": "bucket_items", "free": "free_items"}[kind]
	if table == "" {
		return event.ValidationError{Msg: "순서는 버킷과 자유 블록만 바꿀 수 있어요"}
	}
	if _, err := c.typed(ctx, block, kind); err != nil {
		return err
	}
	var have []struct {
		ID         string `json:"id"`
		OrderIndex int    `json:"order_index"`
	}
	q := url.Values{"block_id": {"eq." + block}, "select": {"id,order_index"}}
	if err := c.rest(ctx, http.MethodGet, table+"?"+q.Encode(), nil, &have); err != nil {
		return err
	}
	at := make(map[string]int, len(have))
	for _, h := range have {
		at[h.ID] = h.OrderIndex
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if _, ok := at[id]; !ok || seen[id] {
			return event.ValidationError{Msg: "목록이 바뀌었어요. 새로고침한 뒤 다시 옮겨 주세요"}
		}
		seen[id] = true
	}
	if len(seen) != len(at) {
		return event.ValidationError{Msg: "목록이 바뀌었어요. 새로고침한 뒤 다시 옮겨 주세요"}
	}
	for i, id := range ids {
		if at[id] == i {
			continue
		}
		q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}}
		if err := c.rest(ctx, http.MethodPatch, table+"?"+q.Encode(), map[string]any{"order_index": i}, nil); err != nil {
			return err
		}
	}
	return nil
}

// DeleteFree takes a note off a free block.
func (c *Client) DeleteFree(ctx context.Context, block, id string) error {
	if _, err := c.typed(ctx, block, "free"); err != nil {
		return err
	}
	return c.deleteOne(ctx, "free_items", block, id, nil)
}

/* ───────────────────────────── habit ───────────────────────────── */

// Check marks a day on a habit block, as the signed-in person. A day already
// checked answers with that check.
func (c *Client) Check(ctx context.Context, block, date, memo string) (Check, error) {
	space, err := c.typed(ctx, block, "workout")
	if err != nil {
		return Check{}, err
	}
	_, me, _ := c.where(ctx)
	if !day(date) {
		return Check{}, event.ValidationError{Msg: "날짜는 YYYY-MM-DD 여야 해요"}
	}
	if date > today() {
		return Check{}, event.ValidationError{Msg: "오지 않은 날은 체크할 수 없어요"}
	}
	var have []checkRow
	q := url.Values{"block_id": {"eq." + block}, "user_id": {"eq." + me}, "date": {"eq." + date}, "select": {"id,user_id,date,memo"}}
	if err := c.rest(ctx, http.MethodGet, "workout_checks?"+q.Encode(), nil, &have); err != nil {
		return Check{}, err
	}
	if len(have) > 0 {
		return Check{ID: have[0].ID, Date: have[0].Date, Memo: str(have[0].Memo), Who: "me"}, nil
	}
	body := map[string]any{"id": newUUID(), "block_id": block, "space_id": space, "user_id": me, "date": date}
	var saved []checkRow
	if err := c.rest(ctx, http.MethodPost, "workout_checks?select=id,user_id,date,memo", body, &saved); err != nil {
		return Check{}, err
	}
	if len(saved) == 0 {
		return Check{}, errors.New("uniple 이 저장한 체크를 돌려주지 않았습니다")
	}
	out := Check{ID: saved[0].ID, Date: saved[0].Date, Who: "me"}
	c.tell(ctx, block, map[string]any{"date": date})
	if memo = strings.TrimSpace(memo); memo != "" {
		if err := c.SetCheckMemo(ctx, block, out.ID, memo); err != nil {
			return out, err
		}
		out.Memo = memo
	}
	return out, nil
}

// SetCheckMemo writes the memo on one of the signed-in person's checks.
func (c *Client) SetCheckMemo(ctx context.Context, block, id, memo string) error {
	if _, err := c.typed(ctx, block, "workout"); err != nil {
		return err
	}
	_, me, _ := c.where(ctx)
	memo = strings.TrimSpace(memo)
	if utf8.RuneCountInString(memo) > 200 {
		return event.ValidationError{Msg: "메모는 200자까지예요"}
	}
	var m any
	if memo != "" {
		m = memo
	}
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "user_id": {"eq." + me}, "select": {"id"}}
	var saved []checkRow
	if err := c.rest(ctx, http.MethodPatch, "workout_checks?"+q.Encode(), map[string]any{"memo": m}, &saved); err != nil {
		return err
	}
	if len(saved) == 0 {
		return event.ErrNotFound
	}
	return nil
}

// Uncheck takes one of the signed-in person's checks off, memo and all.
func (c *Client) Uncheck(ctx context.Context, block, id string) error {
	if _, err := c.typed(ctx, block, "workout"); err != nil {
		return err
	}
	_, me, _ := c.where(ctx)
	return c.deleteOne(ctx, "workout_checks", block, id, url.Values{"user_id": {"eq." + me}})
}

/* ───────────────────────────── date map ───────────────────────────── */

// RecordInput is a date written against a region.
type RecordInput struct {
	Region *string `json:"region"`
	Title  *string `json:"title"`
	From   *string `json:"from"`
	To     *string `json:"to"`
	Note   *string `json:"note"`
}

func (in RecordInput) fields(isNew bool) (map[string]any, error) {
	f := map[string]any{}
	if isNew {
		code, err := stored(strings.TrimSpace(deref(in.Region)))
		if err != nil {
			return nil, err
		}
		f["region_code"] = code
	}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if utf8.RuneCountInString(t) > 60 {
			return nil, event.ValidationError{Msg: "제목은 60자까지예요"}
		}
		f["title"] = nullable(t)
	}
	from, to := strings.TrimSpace(deref(in.From)), strings.TrimSpace(deref(in.To))
	if in.From != nil {
		if from != "" && !day(from) {
			return nil, event.ValidationError{Msg: "시작일은 YYYY-MM-DD 여야 해요"}
		}
		f["date_start"] = nullable(from)
	}
	if in.To != nil {
		if to != "" && !day(to) {
			return nil, event.ValidationError{Msg: "종료일은 YYYY-MM-DD 여야 해요"}
		}
		if to != "" && from != "" && to < from {
			return nil, event.ValidationError{Msg: "종료일이 시작일보다 빨라요"}
		}
		if to == from {
			to = ""
		}
		f["date_end"] = nullable(to)
	}
	if in.Note != nil {
		n := strings.TrimSpace(*in.Note)
		if utf8.RuneCountInString(n) > 1000 {
			return nil, event.ValidationError{Msg: "메모는 1000자까지예요"}
		}
		f["note"] = nullable(n)
	}
	return f, nil
}

// AddRecord writes a date down against a region of a date map.
func (c *Client) AddRecord(ctx context.Context, block string, in RecordInput) (Record, error) {
	space, err := c.typed(ctx, block, "map")
	if err != nil {
		return Record{}, err
	}
	f, err := in.fields(true)
	if err != nil {
		return Record{}, err
	}
	var have []recordRow
	q := url.Values{"block_id": {"eq." + block}, "region_code": {"eq." + f["region_code"].(string)}, "select": {"id,order_index"}, "order": {"order_index.desc"}, "limit": {"1"}}
	if err := c.rest(ctx, http.MethodGet, "map_records?"+q.Encode(), nil, &have); err != nil {
		return Record{}, err
	}
	next := 0
	if len(have) > 0 {
		next = have[0].OrderIndex + 1
	}
	f["id"], f["block_id"], f["space_id"], f["order_index"] = newUUID(), block, space, next
	var saved []recordRow
	if err := c.rest(ctx, http.MethodPost, "map_records?select=id,region_code,title,date_start,date_end,note,order_index,created_at", f, &saved); err != nil {
		return Record{}, err
	}
	if len(saved) == 0 {
		return Record{}, errors.New("uniple 이 저장한 기록을 돌려주지 않았습니다")
	}
	return saved[0].toRecord(), nil
}

// SetRecord changes a date's title, days or note.
func (c *Client) SetRecord(ctx context.Context, block, id string, in RecordInput) (Record, error) {
	if _, err := c.typed(ctx, block, "map"); err != nil {
		return Record{}, err
	}
	f, err := in.fields(false)
	if err != nil {
		return Record{}, err
	}
	if len(f) == 0 {
		return Record{}, event.ValidationError{Msg: "바꿀 것이 없어요"}
	}
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "select": {"id,region_code,title,date_start,date_end,note,order_index,created_at"}}
	var saved []recordRow
	if err := c.rest(ctx, http.MethodPatch, "map_records?"+q.Encode(), f, &saved); err != nil {
		return Record{}, err
	}
	if len(saved) == 0 {
		return Record{}, event.ErrNotFound
	}
	return saved[0].toRecord(), nil
}

// DeleteRecord takes a date off the map, its photos with it, as the app does.
func (c *Client) DeleteRecord(ctx context.Context, block, id string) error {
	if _, err := c.typed(ctx, block, "map"); err != nil {
		return err
	}
	q := url.Values{"block_id": {"eq." + block}, "record_id": {"eq." + id}}
	if err := c.rest(ctx, http.MethodDelete, "map_visits?"+q.Encode(), nil, nil); err != nil {
		return err
	}
	return c.deleteOne(ctx, "map_records", block, id, nil)
}

/* ───────────────────────────── helpers ───────────────────────────── */

// deleteOne removes a row of a block, answering ErrNotFound when none went.
func (c *Client) deleteOne(ctx context.Context, table, block, id string, extra url.Values) error {
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "select": {"id"}}
	for k, v := range extra {
		q[k] = v
	}
	var gone []struct {
		ID string `json:"id"`
	}
	if err := c.rest(ctx, http.MethodDelete, table+"?"+q.Encode(), nil, &gone); err != nil {
		return err
	}
	if len(gone) == 0 {
		return event.ErrNotFound
	}
	return nil
}

func day(s string) bool {
	if len(s) != 10 {
		return false
	}
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// today is the date in Seoul, where the two live.
func today() string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.FixedZone("KST", 9*3600)
	}
	return time.Now().In(loc).Format(time.DateOnly)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

/* ───────────────────────────── photos on the map ───────────────────────────── */

// VisitInput is a photo put on the date map: a region's face, or one of a
// record's photos.
type VisitInput struct {
	Region string `json:"region"`
	Asset  string `json:"asset"`
	Record string `json:"record"`
	Face   bool   `json:"face"`
}

const visitColumns = "id,region_code,asset_id,created_at,note,is_rep,rotation,order_index,record_id"

// AddVisit puts an uploaded photo on the map: as the region's face (the app's
// own call, which takes over from the face before), or under a record, in the
// record's region.
func (c *Client) AddVisit(ctx context.Context, block string, in VisitInput) (Visit, error) {
	space, err := c.typed(ctx, block, "map")
	if err != nil {
		return Visit{}, err
	}
	if !uuidLike(in.Asset) {
		return Visit{}, event.ValidationError{Msg: "올린 사진이 없어요"}
	}
	id := newUUID()
	if in.Face {
		code, err := stored(strings.TrimSpace(in.Region))
		if err != nil {
			return Visit{}, err
		}
		if err := c.rest(ctx, http.MethodPost, "rpc/add_map_face_asset", map[string]any{
			"p_id": id, "p_block_id": block, "p_space_id": space, "p_region_code": code, "p_asset_id": in.Asset, "p_clip_asset_id": nil,
		}, nil); err != nil {
			return Visit{}, err
		}
		c.tell(ctx, block, map[string]any{"itemId": id})
		return c.visit(ctx, block, id)
	}
	var recs []recordRow
	q := url.Values{"id": {"eq." + in.Record}, "block_id": {"eq." + block}, "select": {"id,region_code,order_index,created_at"}}
	if err := c.rest(ctx, http.MethodGet, "map_records?"+q.Encode(), nil, &recs); err != nil {
		return Visit{}, err
	}
	if len(recs) == 0 {
		return Visit{}, event.ValidationError{Msg: "사진을 붙일 기록이 없어요"}
	}
	var have []visitRow
	q = url.Values{"block_id": {"eq." + block}, "record_id": {"eq." + in.Record}, "select": {"id"}}
	if err := c.rest(ctx, http.MethodGet, "map_visits?"+q.Encode(), nil, &have); err != nil {
		return Visit{}, err
	}
	body := map[string]any{
		"id": id, "block_id": block, "space_id": space, "region_code": recs[0].RegionCode, "record_id": in.Record,
		"asset_id": in.Asset, "note": nil, "is_rep": false, "rotation": 0, "order_index": len(have),
	}
	var saved []visitRow
	if err := c.rest(ctx, http.MethodPost, "map_visits?select="+visitColumns, body, &saved); err != nil {
		return Visit{}, err
	}
	if len(saved) == 0 {
		return Visit{}, errors.New("uniple 이 저장한 사진을 돌려주지 않았습니다")
	}
	// A record's photos come several at once; the first one tells.
	if len(have) == 0 {
		c.tell(ctx, block, map[string]any{"itemId": id})
	}
	return saved[0].toVisit(), nil
}

func (c *Client) visit(ctx context.Context, block, id string) (Visit, error) {
	var rows []visitRow
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "select": {visitColumns}}
	if err := c.rest(ctx, http.MethodGet, "map_visits?"+q.Encode(), nil, &rows); err != nil {
		return Visit{}, err
	}
	if len(rows) == 0 {
		return Visit{}, event.ErrNotFound
	}
	return rows[0].toVisit(), nil
}

// SetVisit changes a region's face to another uploaded photo, as the app's
// 대표 사진 변경 does.
func (c *Client) SetVisit(ctx context.Context, block, id string, in VisitChange) (Visit, error) {
	if _, err := c.typed(ctx, block, "map"); err != nil {
		return Visit{}, err
	}
	f := map[string]any{}
	if in.Asset != "" {
		if !uuidLike(in.Asset) {
			return Visit{}, event.ValidationError{Msg: "올린 사진이 없어요"}
		}
		// A new photo starts upright, uncut.
		f["asset_id"], f["clip_asset_id"], f["rotation"] = in.Asset, nil, 0
	}
	if in.Rotation != nil {
		r := *in.Rotation
		if math.IsNaN(r) || math.IsInf(r, 0) {
			return Visit{}, event.ValidationError{Msg: "돌릴 각도가 숫자가 아니에요"}
		}
		f["rotation"] = math.Mod(math.Mod(r, 360)+360, 360)
	}
	if len(f) == 0 {
		return Visit{}, event.ValidationError{Msg: "바꿀 것이 없어요"}
	}
	q := url.Values{"id": {"eq." + id}, "block_id": {"eq." + block}, "select": {"id"}}
	var saved []visitRow
	if err := c.rest(ctx, http.MethodPatch, "map_visits?"+q.Encode(), f, &saved); err != nil {
		return Visit{}, err
	}
	if len(saved) == 0 {
		return Visit{}, event.ErrNotFound
	}
	return c.visit(ctx, block, id)
}

// VisitChange is a map photo changed: a new photo for it, or how far it is
// turned inside its region's shape, in degrees.
type VisitChange struct {
	Asset    string   `json:"asset"`
	Rotation *float64 `json:"rotation"`
}

// DeleteVisit takes a photo off the map.
func (c *Client) DeleteVisit(ctx context.Context, block, id string) error {
	if _, err := c.typed(ctx, block, "map"); err != nil {
		return err
	}
	return c.deleteOne(ctx, "map_visits", block, id, nil)
}

/* ───────────────────────────── photos on calendar days ───────────────────────────── */

// DayPhotoInput is an uploaded photo put on a calendar day, maybe of an entry.
type DayPhotoInput struct {
	Date  string `json:"date"`
	Asset string `json:"asset"`
	Event string `json:"event"`
}

// AddDayPhoto puts an uploaded photo on a day, last among the day's.
func (c *Client) AddDayPhoto(ctx context.Context, in DayPhotoInput) (DayPhoto, error) {
	block, space, _, err := c.calendar(ctx)
	if err != nil {
		return DayPhoto{}, err
	}
	if !day(in.Date) {
		return DayPhoto{}, event.ValidationError{Msg: "날짜는 YYYY-MM-DD 여야 해요"}
	}
	if !uuidLike(in.Asset) {
		return DayPhoto{}, event.ValidationError{Msg: "올린 사진이 없어요"}
	}
	// A day of a repeating entry is filed under the series.
	series, _, _ := strings.Cut(in.Event, "#")
	var have []struct {
		ID string `json:"id"`
	}
	q := url.Values{"block_id": {"eq." + block}, "date": {"eq." + in.Date}, "select": {"id"}}
	if err := c.rest(ctx, http.MethodGet, "calendar_day_photos?"+q.Encode(), nil, &have); err != nil {
		return DayPhoto{}, err
	}
	body := map[string]any{"id": newUUID(), "block_id": block, "space_id": space, "date": in.Date, "asset_id": in.Asset, "event_id": nullable(series), "sort_order": len(have)}
	q = url.Values{"select": {"id,date,asset_id,event_id,is_cover,sort_order,created_at"}}
	got, err := c.dayPhotosPost(ctx, q, body)
	if err != nil {
		return DayPhoto{}, err
	}
	return got, nil
}

func (c *Client) dayPhotosPost(ctx context.Context, q url.Values, body map[string]any) (DayPhoto, error) {
	var rows []struct {
		ID      string  `json:"id"`
		Date    string  `json:"date"`
		AssetID string  `json:"asset_id"`
		EventID *string `json:"event_id"`
		IsCover bool    `json:"is_cover"`
	}
	if err := c.rest(ctx, http.MethodPost, "calendar_day_photos?"+q.Encode(), body, &rows); err != nil {
		return DayPhoto{}, err
	}
	if len(rows) == 0 {
		return DayPhoto{}, errors.New("uniple 이 저장한 사진을 돌려주지 않았습니다")
	}
	r := rows[0]
	return DayPhoto{ID: r.ID, Date: r.Date, Asset: r.AssetID, Event: str(r.EventID), Cover: r.IsCover}, nil
}

// DeleteDayPhoto takes a photo off its day.
func (c *Client) DeleteDayPhoto(ctx context.Context, id string) error {
	block, _, _, err := c.calendar(ctx)
	if err != nil {
		return err
	}
	return c.deleteOne(ctx, "calendar_day_photos", block, id, nil)
}

// CoverDayPhoto makes a photo the face of its day, the one the calendar
// card shows.
func (c *Client) CoverDayPhoto(ctx context.Context, id string) error {
	if _, _, _, err := c.calendar(ctx); err != nil {
		return err
	}
	if !uuidLike(id) {
		return event.ErrNotFound
	}
	return c.rest(ctx, http.MethodPost, "rpc/set_calendar_day_photo_cover", map[string]any{"p_photo_id": id}, nil)
}

/* ───────────────────────────── blocks, kept ───────────────────────────── */

// BlockInput is what changes on a block: its title, and the settings the app
// keeps in its config that this console offers - the D-day card's upcoming
// days, the date map card's 시/도.
type BlockInput struct {
	Title        *string `json:"title"`
	ShowUpcoming *bool   `json:"showUpcoming"`
	CardRegion   *string `json:"cardRegion"`
}

// UpdateBlock renames a block or changes those settings.
func (c *Client) UpdateBlock(ctx context.Context, id string, in BlockInput) error {
	if _, err := c.typed(ctx, id, ""); err != nil {
		return err
	}
	if in.Title != nil {
		if err := c.RenameBlock(ctx, id, *in.Title); err != nil {
			return err
		}
	}
	set, unset := map[string]any{}, []string{}
	if in.ShowUpcoming != nil {
		set["showUpcoming"] = *in.ShowUpcoming
	}
	if in.CardRegion != nil {
		switch r := strings.TrimSpace(*in.CardRegion); {
		case r == "" || r == "all":
			unset = append(unset, "card_region")
		case len(r) == 2 && strings.Trim(r, "0123456789") == "":
			set["card_region"] = r
		default:
			return event.ValidationError{Msg: "카드에 둘 지도는 전국이나 시/도예요"}
		}
	}
	if len(set) == 0 && len(unset) == 0 {
		return nil
	}
	return c.rest(ctx, http.MethodPost, "rpc/patch_block_config", map[string]any{
		"p_block_id": id, "p_set": set, "p_unset": unset, "p_add": map[string]any{}, "p_remove": map[string]any{},
	}, nil)
}

// ArchiveBlock puts a block away, as the app's 보관 does - the app can bring
// it back. The calendar stays: this service reads it.
func (c *Client) ArchiveBlock(ctx context.Context, id string) error {
	space, _, err := c.where(ctx)
	if err != nil {
		return err
	}
	var rows []struct {
		Type string `json:"type"`
	}
	q := url.Values{"id": {"eq." + id}, "space_id": {"eq." + space}, "archived_at": {"is.null"}, "select": {"type"}}
	if err := c.rest(ctx, http.MethodGet, "blocks?"+q.Encode(), nil, &rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		return ErrBlock
	}
	if rows[0].Type == "calendar" {
		return event.ValidationError{Msg: "캘린더 블록은 uniple 앱에서 보관해 주세요"}
	}
	q = url.Values{"id": {"eq." + id}, "space_id": {"eq." + space}, "select": {"id"}}
	var saved []struct {
		ID string `json:"id"`
	}
	if err := c.rest(ctx, http.MethodPatch, "blocks?"+q.Encode(), map[string]any{"archived_at": time.Now().UTC().Format(time.RFC3339)}, &saved); err != nil {
		return err
	}
	if len(saved) == 0 {
		return ErrBlock
	}
	return nil
}

// Archived is the blocks put away, the last put away first: what the app's
// 보관함 holds.
func (c *Client) Archived(ctx context.Context) ([]Block, error) {
	space, _, err := c.where(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{
		"space_id":    {"eq." + space},
		"archived_at": {"not.is.null"},
		"select":      {"id,type,title,order_index,archived_at"},
		"order":       {"archived_at.desc"},
	}
	var rows []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Title      string `json:"title"`
		OrderIndex int    `json:"order_index"`
		ArchivedAt string `json:"archived_at"`
	}
	if err := c.rest(ctx, http.MethodGet, "blocks?"+q.Encode(), nil, &rows); err != nil {
		return nil, err
	}
	out := make([]Block, 0, len(rows))
	for _, r := range rows {
		out = append(out, Block{ID: r.ID, Type: r.Type, Title: r.Title, Order: r.OrderIndex, Archived: r.ArchivedAt})
	}
	return out, nil
}

// Restore takes a block out of 보관 and puts it last on the home, its items
// as they were. A type already at its count stays put away.
func (c *Client) Restore(ctx context.Context, id string) error {
	space, _, err := c.where(ctx)
	if err != nil {
		return err
	}
	var rows []struct {
		Type string `json:"type"`
	}
	q := url.Values{"id": {"eq." + id}, "space_id": {"eq." + space}, "archived_at": {"not.is.null"}, "select": {"type"}}
	if err := c.rest(ctx, http.MethodGet, "blocks?"+q.Encode(), nil, &rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		return ErrBlock
	}
	live, err := c.blocks(ctx, space, "")
	if err != nil {
		return err
	}
	n, last := 0, -1
	for _, b := range live {
		if b.Type == rows[0].Type {
			n++
		}
		last = max(last, b.OrderIndex)
	}
	if limit := max(blockCap[rows[0].Type], 1); n >= limit {
		return event.ValidationError{Msg: fmt.Sprintf("이 블록은 %d개까지 둘 수 있어요. 같은 블록 하나를 보관한 뒤 꺼내 주세요", limit)}
	}
	q = url.Values{"id": {"eq." + id}, "space_id": {"eq." + space}, "select": {"id"}}
	var saved []struct {
		ID string `json:"id"`
	}
	if err := c.rest(ctx, http.MethodPatch, "blocks?"+q.Encode(), map[string]any{"archived_at": nil, "order_index": last + 1}, &saved); err != nil {
		return err
	}
	if len(saved) == 0 {
		return ErrBlock
	}
	return nil
}

// OrderBlocks puts the home's blocks in the given order: every live block,
// each once.
func (c *Client) OrderBlocks(ctx context.Context, ids []string) error {
	space, _, err := c.where(ctx)
	if err != nil {
		return err
	}
	rows, err := c.blocks(ctx, space, "")
	if err != nil {
		return err
	}
	at := map[string]int{}
	for _, b := range rows {
		at[b.ID] = b.OrderIndex
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := at[id]; !ok || seen[id] {
			return event.ValidationError{Msg: "블록 순서가 홈과 맞지 않아요. 새로 읽고 다시 해 주세요"}
		}
		seen[id] = true
	}
	if len(ids) != len(at) {
		return event.ValidationError{Msg: "블록 순서가 홈과 맞지 않아요. 새로 읽고 다시 해 주세요"}
	}
	for i, id := range ids {
		if at[id] == i {
			continue
		}
		q := url.Values{"id": {"eq." + id}, "space_id": {"eq." + space}}
		if err := c.rest(ctx, http.MethodPatch, "blocks?"+q.Encode(), map[string]any{"order_index": i}, nil); err != nil {
			return err
		}
	}
	return nil
}

/* ───────────────────────────── the day they met ───────────────────────────── */

// SetStart moves the day they met: the space's own date and the D-day
// block's start entry, then the calendar's anniversary entries.
func (c *Client) SetStart(ctx context.Context, block, title, date string) error {
	space, err := c.typed(ctx, block, "dday")
	if err != nil {
		return err
	}
	_, me, _ := c.where(ctx)
	date = strings.TrimSpace(date)
	if !day(date) {
		return event.ValidationError{Msg: "날짜는 YYYY-MM-DD 여야 해요"}
	}
	if date > today() {
		return event.ValidationError{Msg: "만난 날은 오늘이나 그 전이에요"}
	}
	title = strings.TrimSpace(title)
	if utf8.RuneCountInString(title) > 40 {
		return event.ValidationError{Msg: "이름은 40자까지예요"}
	}
	// The space first: a person who may not change it changes nothing.
	var saved []struct {
		ID string `json:"id"`
	}
	q := url.Values{"id": {"eq." + space}, "select": {"id"}}
	if err := c.rest(ctx, http.MethodPatch, "spaces?"+q.Encode(), map[string]any{"anniversary_date": date}, &saved); err != nil {
		return err
	}
	if len(saved) == 0 {
		return event.ValidationError{Msg: "만난 날은 uniple 앱에서 바꿔 주세요"}
	}
	var start []ddayRow
	q = url.Values{"block_id": {"eq." + block}, "is_start_date": {"eq.true"}, "select": {"id,title,date,is_start_date"}}
	if err := c.rest(ctx, http.MethodGet, "dday_items?"+q.Encode(), nil, &start); err != nil {
		return err
	}
	if len(start) > 0 {
		f := map[string]any{"date": date}
		if title != "" {
			f["title"] = title
		}
		q = url.Values{"id": {"eq." + start[0].ID}, "block_id": {"eq." + block}}
		if err := c.rest(ctx, http.MethodPatch, "dday_items?"+q.Encode(), f, nil); err != nil {
			return err
		}
	} else {
		if title == "" {
			title = "우리가 만난 날"
		}
		body := map[string]any{"id": newUUID(), "block_id": block, "space_id": space, "title": title, "date": date, "is_start_date": true, "created_by": me}
		if err := c.rest(ctx, http.MethodPost, "dday_items", body, nil); err != nil {
			return err
		}
	}
	c.syncDDays(ctx, space)
	return nil
}
