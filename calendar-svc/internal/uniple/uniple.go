// Package uniple keeps the calendar in uniple, a couple's app, instead of in
// Postgres.
//
// uniple has no public API. Its web app talks to a Supabase project
// (api.uniple.app) with the signed-in user's token, and row-level security
// limits that token to the couple's own space - so this package does the same:
// it holds one user's session and reads and writes calendar_events through
// PostgREST, exactly as the app does. Table and column names were read off the
// app's bundle, not a contract; an app update can break this, and the errors
// say so rather than returning an empty calendar.
//
// The calendar is shared. Anything written here appears on the partner's
// phone at once, which is why the agent puts every write on a card first.
package uniple

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

type Config struct {
	BaseURL string // https://api.uniple.app
	AnonKey string // the app's public anon key
	// SeedRefresh is UNIPLE_REFRESH_TOKEN from the environment. It is only a
	// seed: Supabase rotates the token on every use, so the live one is kept
	// in Postgres and this is consulted only when it differs from the seed the
	// stored token grew from - i.e. when someone pasted a fresh login.
	SeedRefresh string
	// BlockID pins one calendar when the space has several. Empty picks the
	// first calendar block.
	BlockID string
}

type Client struct {
	cfg  Config
	http *http.Client
	pool *pgxpool.Pool

	mu        sync.Mutex // guards everything below; refresh must never run twice at once
	refresh   string
	access    string
	accessExp time.Time
	me        string
	block     string
	space     string
}

func Open(ctx context.Context, dsn string, cfg Config) (*Client, error) {
	if cfg.BaseURL == "" || cfg.AnonKey == "" {
		return nil, errors.New("UNIPLE_URL 과 UNIPLE_ANON_KEY 가 필요합니다")
	}
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("DSN 해석: %w", err)
	}
	pcfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("풀 생성: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("연결 확인: %w", err)
	}
	c := &Client{
		cfg:  cfg,
		pool: pool,
		// 승인 카드를 띄운 도구 호출 안에서 불린다. 무기한 매달리면 승인을
		// 눌러도 아무 일도 일어나지 않는다.
		http: &http.Client{Timeout: 10 * time.Second},
	}
	if err := c.loadRefresh(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) Close() { c.pool.Close() }

// Ping answers for the part this service owns. uniple itself is not pinged:
// a health check every fifteen seconds must not spend refresh tokens, and an
// outage over there should show up as a failed read, not as this container
// being restarted.
func (c *Client) Ping(ctx context.Context) error { return c.pool.Ping(ctx) }

/* ───────────────────────────── session ───────────────────────────── */

// loadRefresh picks the refresh token to start from: the stored one, unless
// the environment carries a different seed than the one it grew from.
func (c *Client) loadRefresh(ctx context.Context) error {
	var stored, seed string
	err := c.pool.QueryRow(ctx,
		`select refresh_token, seed from uniple_session where id = 1`).Scan(&stored, &seed)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("uniple 세션 읽기: %w", err)
	}

	if c.cfg.SeedRefresh != "" && c.cfg.SeedRefresh != seed {
		c.refresh = c.cfg.SeedRefresh
		return c.saveRefresh(ctx, c.refresh, c.cfg.SeedRefresh)
	}
	if stored == "" {
		return errors.New("uniple 로그인 토큰이 없습니다. UNIPLE_REFRESH_TOKEN 을 설정하세요")
	}
	c.refresh = stored
	return nil
}

func (c *Client) saveRefresh(ctx context.Context, token, seed string) error {
	_, err := c.pool.Exec(ctx, `
		insert into uniple_session (id, refresh_token, seed, updated_at)
		values (1, $1, $2, now())
		on conflict (id) do update set
		  refresh_token = excluded.refresh_token,
		  seed = excluded.seed,
		  updated_at = now()`, token, seed)
	if err != nil {
		return fmt.Errorf("uniple 세션 저장: %w", err)
	}
	return nil
}

// token returns a live access token, refreshing under the lock. The refresh
// token rotates on use, so two refreshes racing would spend the same token
// twice and log the session out - hence one at a time, and the new token
// written down before anything else happens.
func (c *Client) token(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.access != "" && time.Until(c.accessExp) > time.Minute {
		return c.access, nil
	}

	body, _ := json.Marshal(map[string]string{"refresh_token": c.refresh})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/auth/v1/token?grant_type=refresh_token", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("apikey", c.cfg.AnonKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("uniple 로그인 갱신: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		User         struct {
			ID string `json:"id"`
		} `json:"user"`
		Msg string `json:"msg"`
	}
	_ = json.Unmarshal(raw, &out)
	if res.StatusCode != http.StatusOK || out.AccessToken == "" {
		// 회전된 토큰이 무효가 된 경우다. 다시 로그인해 새 토큰을 넣는 것
		// 말고는 방법이 없으므로 그렇게 말한다.
		return "", fmt.Errorf("uniple 로그인이 만료됐습니다(%s). 새 UNIPLE_REFRESH_TOKEN 이 필요합니다", res.Status)
	}

	var seed string
	_ = c.pool.QueryRow(ctx, `select seed from uniple_session where id = 1`).Scan(&seed)
	if err := c.saveRefresh(ctx, out.RefreshToken, seed); err != nil {
		return "", err
	}
	c.refresh = out.RefreshToken
	c.access = out.AccessToken
	c.accessExp = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	c.me = out.User.ID
	return c.access, nil
}

/* ───────────────────────────── REST ───────────────────────────── */

// rest calls PostgREST. A 401 gets one forced refresh and one retry: an
// access token can be revoked before its stated expiry.
func (c *Client) rest(ctx context.Context, method, path string, body any, out any) error {
	var encoded []byte
	if body != nil {
		var err error
		if encoded, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		access, err := c.token(ctx, attempt > 0)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+"/rest/v1/"+path, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		req.Header.Set("apikey", c.cfg.AnonKey)
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Prefer", "return=representation")
		res, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("uniple 호출: %w", err)
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue
		}
		if res.StatusCode >= 300 {
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(raw, &e)
			return fmt.Errorf("uniple 이 거부했습니다(%s): %s", res.Status, e.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(raw, out)
	}
	return errors.New("uniple 인증에 실패했습니다")
}

// calendar finds the couple's calendar block once.
func (c *Client) calendar(ctx context.Context) (block, space, me string, err error) {
	if _, err := c.token(ctx, false); err != nil {
		return "", "", "", err
	}
	c.mu.Lock()
	if c.block != "" {
		defer c.mu.Unlock()
		return c.block, c.space, c.me, nil
	}
	c.mu.Unlock()

	q := url.Values{}
	q.Set("type", "eq.calendar")
	q.Set("archived_at", "is.null")
	q.Set("select", "id,space_id")
	q.Set("order", "order_index")
	if c.cfg.BlockID != "" {
		q.Set("id", "eq."+c.cfg.BlockID)
	}
	var blocks []struct {
		ID      string `json:"id"`
		SpaceID string `json:"space_id"`
	}
	if err := c.rest(ctx, http.MethodGet, "blocks?"+q.Encode(), nil, &blocks); err != nil {
		return "", "", "", err
	}
	if len(blocks) == 0 {
		return "", "", "", errors.New("uniple 에서 캘린더를 찾지 못했습니다")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.block, c.space = blocks[0].ID, blocks[0].SpaceID
	return c.block, c.space, c.me, nil
}

/* ───────────────────────────── rows ───────────────────────────── */

const columns = "id,title,start_date,end_date,start_time,end_time,memo,created_by,owner_user_id," +
	"recurrence,recurrence_from,recurrence_until,recurrence_parent_id,recurrence_override_date," +
	"recurrence_exdates,updated_at"

type row struct {
	ID                     string   `json:"id"`
	Title                  string   `json:"title"`
	StartDate              string   `json:"start_date"`
	EndDate                *string  `json:"end_date"`
	StartTime              *string  `json:"start_time"`
	EndTime                *string  `json:"end_time"`
	Memo                   *string  `json:"memo"`
	CreatedBy              string   `json:"created_by"`
	OwnerUserID            *string  `json:"owner_user_id"`
	Recurrence             *string  `json:"recurrence"`
	RecurrenceFrom         *string  `json:"recurrence_from"`
	RecurrenceUntil        *string  `json:"recurrence_until"`
	RecurrenceParentID     *string  `json:"recurrence_parent_id"`
	RecurrenceOverrideDate *string  `json:"recurrence_override_date"`
	RecurrenceExdates      []string `json:"recurrence_exdates"`
	UpdatedAt              string   `json:"updated_at"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func opt(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// placePrefix carries the place inside the memo. uniple has no place column,
// and a line the app shows as text is better than a field that is dropped.
const placePrefix = "📍 "

func splitMemo(memo string) (place, rest string) {
	if !strings.HasPrefix(memo, placePrefix) {
		return "", memo
	}
	first, rest, _ := strings.Cut(memo, "\n")
	return strings.TrimPrefix(first, placePrefix), rest
}

func joinMemo(place, memo string) string {
	if place == "" {
		return memo
	}
	if memo == "" {
		return placePrefix + place
	}
	return placePrefix + place + "\n" + memo
}

func hm(t *string) string {
	if t == nil || len(*t) < 5 {
		return ""
	}
	return (*t)[:5]
}

func (r row) toEvent(me string) event.Event {
	place, memo := splitMemo(str(r.Memo))
	e := event.Event{
		ID:         r.ID,
		Date:       r.StartDate,
		Start:      hm(r.StartTime),
		End:        hm(r.EndTime),
		Title:      r.Title,
		Place:      place,
		Memo:       memo,
		Source:     event.SourceHuman,
		UpdatedAt:  r.UpdatedAt,
		Recurrence: str(r.Recurrence),
	}
	if end := str(r.EndDate); end != "" && end != r.StartDate {
		e.EndDate = end
	}
	if r.CreatedBy != me {
		e.Source = event.SourcePartner
	}
	switch {
	case r.OwnerUserID == nil:
		e.Owner = event.OwnerShared
	case *r.OwnerUserID == me:
		e.Owner = event.OwnerMe
	default:
		e.Owner = event.OwnerPartner
	}
	return e
}

/* ───────────────────────────── reads ───────────────────────────── */

// Range returns every entry touching [from, to], recurring ones expanded the
// way the app expands them. Unbounded ends are capped at a year either side:
// a daily series has no natural end, and "everything" would be thousands.
func (c *Client) Range(ctx context.Context, from, to string) ([]event.Event, error) {
	block, _, me, err := c.calendar(ctx)
	if err != nil {
		return nil, err
	}
	today := time.Now()
	if from == "" {
		from = today.AddDate(-1, 0, 0).Format(time.DateOnly)
	}
	if to == "" {
		to = today.AddDate(1, 0, 0).Format(time.DateOnly)
	}

	// The app's own filter: series that may reach the window, plus single
	// entries that overlap it.
	q := url.Values{}
	q.Set("block_id", "eq."+block)
	q.Set("select", columns)
	q.Set("order", "start_date")
	q.Set("or", fmt.Sprintf(
		"(and(recurrence.not.is.null,start_date.lte.%[2]s,or(recurrence_from.is.null,recurrence_from.lte.%[2]s)),"+
			"and(recurrence.is.null,start_date.lte.%[2]s,or(and(end_date.is.null,start_date.gte.%[1]s),end_date.gte.%[1]s)))",
		from, to))
	var rows []row
	if err := c.rest(ctx, http.MethodGet, "calendar_events?"+q.Encode(), nil, &rows); err != nil {
		return nil, err
	}

	out := []event.Event{}
	for _, r := range expand(rows, from, to) {
		out = append(out, r.toEvent(me))
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		if a.Start != b.Start {
			return a.Start < b.Start // 종일("")이 그날의 맨 위
		}
		return a.ID < b.ID
	})
	return out, nil
}

func (c *Client) row(ctx context.Context, id string) (row, error) {
	block, _, _, err := c.calendar(ctx)
	if err != nil {
		return row{}, err
	}
	q := url.Values{}
	q.Set("id", "eq."+id)
	q.Set("block_id", "eq."+block)
	q.Set("select", columns)
	var rows []row
	if err := c.rest(ctx, http.MethodGet, "calendar_events?"+q.Encode(), nil, &rows); err != nil {
		return row{}, err
	}
	if len(rows) == 0 {
		return row{}, event.ErrNotFound
	}
	return rows[0], nil
}

// Get accepts a plain id or an occurrence id ("<series>#<date>").
func (c *Client) Get(ctx context.Context, id string) (event.Event, error) {
	series, date, isOccurrence := strings.Cut(id, "#")
	r, err := c.row(ctx, series)
	if err != nil {
		return event.Event{}, err
	}
	if isOccurrence {
		r = occurrence(r, date)
	}
	_, _, me, _ := c.calendar(ctx)
	return r.toEvent(me), nil
}

/* ───────────────────────────── writes ───────────────────────────── */

// Put inserts (empty ID) or updates. Single occurrences of a series are not
// editable from here - the app splits them into override rows through an RPC
// whose exact contract is not visible, and guessing at it on a shared calendar
// is the wrong place to experiment.
func (c *Client) Put(ctx context.Context, e event.Event) (event.Event, error) {
	if err := e.Validate(); err != nil {
		return event.Event{}, err
	}
	if strings.Contains(e.ID, "#") {
		return event.Event{}, event.ValidationError{Msg: "반복 일정의 하루만 바꾸는 것은 uniple 앱에서 해 주세요"}
	}
	block, space, me, err := c.calendar(ctx)
	if err != nil {
		return event.Event{}, err
	}

	fields := map[string]any{
		"title":      e.Title,
		"start_date": e.Date,
		"end_date":   opt(e.EndDate),
		"start_time": opt(withSeconds(e.Start)),
		"end_time":   opt(withSeconds(e.End)),
		"memo":       opt(joinMemo(e.Place, e.Memo)),
	}

	var saved []row
	if e.ID == "" {
		owner, err := ownerID(e.Owner, me, true, nil)
		if err != nil {
			return event.Event{}, err
		}
		fields["id"] = newUUID()
		fields["block_id"] = block
		fields["space_id"] = space
		fields["created_by"] = me
		fields["owner_user_id"] = owner
		fields["recurrence"] = nil
		// 알림은 걸지 않는다. 앱에서 고르는 값이고, 여기서 정하면 상대
		// 휴대폰에 예상하지 못한 푸시가 간다.
		fields["reminder_minutes"] = nil
		fields["reminder_tz"] = nil
		err = c.rest(ctx, http.MethodPost, "calendar_events?select="+columns, fields, &saved)
		if err != nil {
			return event.Event{}, err
		}
	} else {
		current, err := c.row(ctx, e.ID)
		if err != nil {
			return event.Event{}, err
		}
		owner, err := ownerID(e.Owner, me, false, current.OwnerUserID)
		if err != nil {
			return event.Event{}, err
		}
		fields["owner_user_id"] = owner
		q := url.Values{}
		q.Set("id", "eq."+e.ID)
		q.Set("block_id", "eq."+block)
		q.Set("select", columns)
		if err := c.rest(ctx, http.MethodPatch, "calendar_events?"+q.Encode(), fields, &saved); err != nil {
			return event.Event{}, err
		}
	}
	if len(saved) == 0 {
		return event.Event{}, errors.New("uniple 이 저장된 일정을 돌려주지 않았습니다")
	}
	return saved[0].toEvent(me), nil
}

// ownerID maps owner to the column. Empty means mine for a new entry and
// "leave it" for an existing one (whose owner may be nil: shared). "partner"
// can only be kept, never assigned: putting an entry on someone else's name is
// not this service's call.
func ownerID(owner, me string, isNew bool, current *string) (*string, error) {
	switch owner {
	case event.OwnerMe:
		return &me, nil
	case event.OwnerShared:
		return nil, nil
	case event.OwnerPartner:
		if !isNew && current != nil && *current != me {
			return current, nil
		}
		return nil, event.ValidationError{Msg: "상대방 이름으로 일정을 만들 수는 없습니다"}
	}
	if isNew {
		return &me, nil
	}
	return current, nil
}

// Delete removes an entry, or - for an occurrence id - only that day of the
// series, by adding it to the series' exceptions the same way the app hides a
// day.
func (c *Client) Delete(ctx context.Context, id string) (event.Event, error) {
	block, _, me, err := c.calendar(ctx)
	if err != nil {
		return event.Event{}, err
	}
	series, date, isOccurrence := strings.Cut(id, "#")
	r, err := c.row(ctx, series)
	if err != nil {
		return event.Event{}, err
	}

	q := url.Values{}
	q.Set("block_id", "eq."+block)
	if isOccurrence {
		gone := occurrence(r, date).toEvent(me)
		q.Set("id", "eq."+series)
		ex := append(append([]string{}, r.RecurrenceExdates...), date)
		if err := c.rest(ctx, http.MethodPatch, "calendar_events?"+q.Encode(),
			map[string]any{"recurrence_exdates": ex}, nil); err != nil {
			return event.Event{}, err
		}
		return gone, nil
	}

	// A series takes its per-day overrides with it.
	children := url.Values{}
	children.Set("block_id", "eq."+block)
	children.Set("recurrence_parent_id", "eq."+series)
	if err := c.rest(ctx, http.MethodDelete, "calendar_events?"+children.Encode(), nil, nil); err != nil {
		return event.Event{}, err
	}
	q.Set("id", "eq."+series)
	if err := c.rest(ctx, http.MethodDelete, "calendar_events?"+q.Encode(), nil, nil); err != nil {
		return event.Event{}, err
	}
	return r.toEvent(me), nil
}

// Conflicts finds timed entries on the same day whose span, widened by
// buffer, meets this one's. All-day entries never conflict, and an entry with
// no end is taken to last an hour - the same rules the Postgres backend had.
func (c *Client) Conflicts(ctx context.Context, id string, buffer time.Duration) ([]event.Event, error) {
	target, err := c.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []event.Event{}
	if target.Start == "" {
		return out, nil
	}
	day, err := c.Range(ctx, target.Date, target.Date)
	if err != nil {
		return nil, err
	}
	a0, a1 := span(target)
	for _, other := range day {
		if other.ID == target.ID || other.Start == "" || other.Date != target.Date {
			continue
		}
		b0, b1 := span(other)
		if a0 < b1+buffer && b0 < a1+buffer {
			out = append(out, other)
		}
	}
	return out, nil
}

func span(e event.Event) (time.Duration, time.Duration) {
	start := clock(e.Start)
	if e.End == "" || e.EndDate != "" {
		return start, start + time.Hour
	}
	return start, clock(e.End)
}

func clock(hm string) time.Duration {
	t, err := time.Parse("15:04", hm)
	if err != nil {
		return 0
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
}

func withSeconds(hm string) string {
	if hm == "" {
		return ""
	}
	return hm + ":00"
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
