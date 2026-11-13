package uniple

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

// fakeREST stands in for uniple's PostgREST: it answers each table with a
// canned body and keeps every request it saw.
type fakeREST struct {
	mu      sync.Mutex
	seen    []seenReq
	answers map[string]string // "METHOD table" -> JSON
	objects map[string]string // storage path -> bytes
}

type seenReq struct {
	Method string
	Table  string
	Query  url.Values
	Body   map[string]any
}

func (f *fakeREST) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path, ok := strings.CutPrefix(r.URL.Path, "/storage/v1/object/public/"); ok {
			body, found := f.objects[path]
			if !found {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = io.WriteString(w, body)
			return
		}
		table := strings.TrimPrefix(r.URL.Path, "/rest/v1/")
		var body map[string]any
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("본문이 JSON 이 아닙니다: %s", raw)
			}
		}
		if r.Header.Get("Authorization") != "Bearer at" || r.Header.Get("apikey") != "anon" {
			t.Errorf("%s %s: 인증 머리글이 없습니다", r.Method, table)
		}
		f.mu.Lock()
		f.seen = append(f.seen, seenReq{Method: r.Method, Table: table, Query: r.URL.Query(), Body: body})
		answer, ok := f.answers[r.Method+" "+table]
		f.mu.Unlock()
		if !ok {
			answer = "[]"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, answer)
	})
}

func (f *fakeREST) find(method, table string) []seenReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []seenReq
	for _, s := range f.seen {
		if s.Method == method && s.Table == table {
			out = append(out, s)
		}
	}
	return out
}

// signedIn is a client already holding a live session in the couple's space,
// so nothing is refreshed and no database is needed.
func signedIn(t *testing.T, f *fakeREST) *Client {
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return &Client{
		cfg:       Config{BaseURL: srv.URL, AnonKey: "anon"},
		http:      srv.Client(),
		access:    "at",
		accessExp: time.Now().Add(time.Hour),
		me:        "u-me",
		block:     "b-cal",
		space:     "s-1",
	}
}

const mapBlock = `[{"id":"b-map","type":"map"}]`

func TestSpaceReadsPeopleSettingsAndBlocks(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET profiles": `[{"id":"u-me","nickname":"하늘"},{"id":"u-you","nickname":"바다"}]`,
		"GET spaces":   `[{"id":"s-1","anniversary_date":"2026-08-15","week_start":"sun"}]`,
		"GET blocks": `[
			{"id":"b-dday","type":"dday","title":"♥","order_index":0,"config":null,"dday_items":[{"id":"d1","title":"바다💕","date":"2025-11-21","is_start_date":false,"recurrence":"yearly","reminder_minutes":1440}],"bucket_items":[],"free_items":[],"map_visits":[]},
			{"id":"b-map","type":"map","title":"지도","order_index":4,"config":{"region":"all"},"dday_items":[],"bucket_items":[],"free_items":[],"map_visits":[
				{"id":"v1","region_code":"S04","asset_id":"a1","created_at":"2026-11-10T00:00:00Z","is_rep":false,"rotation":0},
				{"id":"v2","region_code":"S04","asset_id":"a2","created_at":"2026-11-09T00:00:00Z","is_rep":true,"rotation":12},
				{"id":"v3","region_code":"31580","asset_id":"a3","created_at":"2026-10-03T00:00:00Z","is_rep":false,"rotation":null}
			]}]`,
	}}
	c := signedIn(t, f)
	got, err := c.Space(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Me.Name != "하늘" || got.Partner == nil || got.Partner.Name != "바다" || got.Anniversary != "2026-08-15" {
		t.Fatalf("사람과 만난 날: %+v %+v %q", got.Me, got.Partner, got.Anniversary)
	}
	if len(got.Blocks) != 2 || got.Blocks[0].DDay[0].Reminder == nil || *got.Blocks[0].DDay[0].Reminder != 1440 {
		t.Fatalf("블록: %+v", got.Blocks)
	}
	faces := got.Blocks[1].Visits
	if len(faces) != 2 || faces[0].Region != "11040" || faces[0].Asset != "a2" || faces[0].Rotation != 12 || faces[1].Region != "31580" {
		t.Fatalf("지역마다 대표 사진 하나, 서울은 다섯 자리로: %+v", faces)
	}
	q := f.find("GET", "blocks")[0].Query
	if q.Get("space_id") != "eq.s-1" || q.Get("archived_at") != "is.null" || !strings.Contains(q.Get("select"), "bucket_items(") {
		t.Fatalf("블록 읽기: %v", q)
	}
}

func TestRegionCodes(t *testing.T) {
	for in, want := range map[string]string{"S01": "11010", "S25": "11250", "31580": "31580", "Sxx": "Sxx"} {
		if got := Region(in); got != want {
			t.Errorf("Region(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"11040": "S04", "11250": "S25", "31580": "31580"} {
		if got, err := stored(in); err != nil || got != want {
			t.Errorf("stored(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "1104", "11045", "11260", "abcde"} {
		if _, err := stored(bad); err == nil {
			t.Errorf("stored(%q) 는 거절해야 합니다", bad)
		}
	}
}

func TestCheckIsMineAndNotInTheFuture(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks":           `[{"id":"b-h","type":"workout"}]`,
		"POST workout_checks":  `[{"id":"c-new","user_id":"u-me","date":"2026-11-13","memo":null}]`,
		"PATCH workout_checks": `[{"id":"c-new"}]`,
	}}
	c := signedIn(t, f)
	ctx := context.Background()

	var invalid event.ValidationError
	if _, err := c.Check(ctx, "b-h", "2999-01-01", ""); !errors.As(err, &invalid) {
		t.Fatalf("오지 않은 날은 거절: %v", err)
	}
	got, err := c.Check(ctx, "b-h", "2026-11-13", "  하체  ")
	if err != nil || got.ID != "c-new" || got.Who != "me" || got.Memo != "하체" {
		t.Fatalf("체크: %+v %v", got, err)
	}
	post := f.find("POST", "workout_checks")
	if len(post) != 1 || post[0].Body["user_id"] != "u-me" || post[0].Body["space_id"] != "s-1" || post[0].Body["block_id"] != "b-h" {
		t.Fatalf("체크는 내 이름으로: %+v", post)
	}
	patch := f.find("PATCH", "workout_checks")
	if len(patch) != 1 || patch[0].Query.Get("user_id") != "eq.u-me" || patch[0].Body["memo"] != "하체" {
		t.Fatalf("메모는 내 체크에만: %+v", patch)
	}

	// The same day again is the same check, not a second one.
	f.answers["GET workout_checks"] = `[{"id":"c-new","user_id":"u-me","date":"2026-11-13","memo":"하체"}]`
	again, err := c.Check(ctx, "b-h", "2026-11-13", "")
	if err != nil || again.ID != "c-new" || len(f.find("POST", "workout_checks")) != 1 {
		t.Fatalf("같은 날 다시 체크: %+v %v", again, err)
	}

	if err := c.Uncheck(ctx, "b-h", "c-other"); !errors.Is(err, event.ErrNotFound) {
		t.Fatalf("지운 것이 없으면 없다고: %v", err)
	}
	del := f.find("DELETE", "workout_checks")
	if len(del) != 1 || del[0].Query.Get("user_id") != "eq.u-me" {
		t.Fatalf("지우기도 내 체크만: %+v", del)
	}
}

func TestBlockOfAnotherKindIsRefused(t *testing.T) {
	f := &fakeREST{answers: map[string]string{"GET blocks": `[{"id":"b-x","type":"bucket"}]`}}
	c := signedIn(t, f)
	if _, err := c.Check(context.Background(), "b-x", "2026-11-13", ""); !errors.Is(err, ErrBlock) {
		t.Fatalf("버킷 블록에 체크하면 안 됩니다: %v", err)
	}
	if len(f.find("POST", "workout_checks")) != 0 {
		t.Fatal("거절했으면 쓰지 않아야 합니다")
	}
}

func TestBucketGoesToTheEnd(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks":        `[{"id":"b-b","type":"bucket"}]`,
		"GET bucket_items":  `[{"id":"x","order_index":7},{"id":"y","order_index":3}]`,
		"POST bucket_items": `[{"id":"n","title":"오로라 보기","is_completed":false,"created_at":"t","order_index":8}]`,
	}}
	c := signedIn(t, f)
	got, err := c.AddBucket(context.Background(), "b-b", " 오로라 보기 ")
	if err != nil || got.Order != 8 {
		t.Fatalf("버킷 추가: %+v %v", got, err)
	}
	b := f.find("POST", "bucket_items")[0].Body
	if b["title"] != "오로라 보기" || b["order_index"] != float64(8) || b["created_by"] != "u-me" || b["is_completed"] != false {
		t.Fatalf("버킷 본문: %+v", b)
	}
}

func TestDDayChangesSyncTheCalendar(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks":      `[{"id":"b-d","type":"dday"}]`,
		"POST dday_items": `[{"id":"d-new","title":"🎂 생일","date":"1999-03-02","is_start_date":false,"recurrence":"yearly","reminder_minutes":0}]`,
	}}
	c := signedIn(t, f)
	title, date, repeat, at := "🎂 생일", "1999-03-02", "yearly", 0
	got, err := c.AddDDay(context.Background(), "b-d", DDayInput{Title: &title, Date: &date, Repeat: &repeat, Reminder: &at})
	if err != nil || got.ID != "d-new" || got.Repeat != "yearly" {
		t.Fatalf("기념일 추가: %+v %v", got, err)
	}
	b := f.find("POST", "dday_items")[0].Body
	if b["is_start_date"] != false || b["reminder_tz"] != "Asia/Seoul" || b["recurrence"] != "yearly" || b["created_by"] != "u-me" {
		t.Fatalf("기념일 본문: %+v", b)
	}
	sync := f.find("POST", "rpc/sync_dday_calendar_events")
	if len(sync) != 1 || sync[0].Body["p_space_id"] != "s-1" {
		t.Fatalf("기념일을 바꾸면 캘린더도: %+v", sync)
	}
	// The day they met is not an anniversary to take off here.
	_ = c.DeleteDDay(context.Background(), "b-d", "d-start")
	if q := f.find("DELETE", "dday_items")[0].Query; q.Get("is_start_date") != "eq.false" {
		t.Fatalf("만난 날은 지우지 않습니다: %v", q)
	}
}

func TestRecordWithPhotosStays(t *testing.T) {
	f := &fakeREST{answers: map[string]string{"GET blocks": mapBlock, "GET map_visits": `[{"id":"v1"}]`}}
	c := signedIn(t, f)
	var invalid event.ValidationError
	if err := c.DeleteRecord(context.Background(), "b-map", "r1"); !errors.As(err, &invalid) {
		t.Fatalf("사진 있는 기록은 앱에서: %v", err)
	}
	if len(f.find("DELETE", "map_records")) != 0 {
		t.Fatal("지우지 않아야 합니다")
	}
}

func TestRecordInSeoulIsKeptUnderTheAppsCode(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks":       mapBlock,
		"POST map_records": `[{"id":"r-new","region_code":"S04","title":"레코드 페어","date_start":"2026-11-08","date_end":null,"note":null,"order_index":0,"created_at":"t"}]`,
	}}
	c := signedIn(t, f)
	region, title, from, to := "11040", "레코드 페어", "2026-11-08", "2026-11-08"
	got, err := c.AddRecord(context.Background(), "b-map", RecordInput{Region: &region, Title: &title, From: &from, To: &to})
	if err != nil || got.Region != "11040" {
		t.Fatalf("기록 추가: %+v %v", got, err)
	}
	b := f.find("POST", "map_records")[0].Body
	if b["region_code"] != "S04" || b["date_end"] != nil || b["date_start"] != "2026-11-08" {
		t.Fatalf("서울은 S04, 같은 날 하루는 끝 없음: %+v", b)
	}
}

func TestBlockCap(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks": `[{"id":"1","type":"bucket","order_index":0},{"id":"2","type":"bucket","order_index":5}]`,
	}}
	c := signedIn(t, f)
	var invalid event.ValidationError
	if _, err := c.AddBlock(context.Background(), "bucket", ""); !errors.As(err, &invalid) {
		t.Fatalf("버킷리스트는 둘까지: %v", err)
	}
	if _, err := c.AddBlock(context.Background(), "album", ""); !errors.As(err, &invalid) {
		t.Fatalf("앨범은 앱에서: %v", err)
	}
	f.answers["POST blocks"] = `[{"id":"3","type":"workout","title":"🔥 습관 체크","order_index":6}]`
	got, err := c.AddBlock(context.Background(), "workout", "")
	if err != nil || got.Title != "🔥 습관 체크" {
		t.Fatalf("습관 블록: %+v %v", got, err)
	}
	if b := f.find("POST", "blocks")[0].Body; b["order_index"] != float64(6) || b["is_default"] != false {
		t.Fatalf("맨 뒤에: %+v", b)
	}
}

func TestPhotoComesFromTheVariantsBucket(t *testing.T) {
	f := &fakeREST{
		answers: map[string]string{
			"POST rpc/media_assets_lookup": `[{"id":"6f1c2c8e-0000-4000-8000-000000000001","kind":"photo","bucket":"map-photos","status":"ready",
				"variants":{"thumb":{"path":"s-1/a/thumb.jpg"},"display":{"path":"s-1/a/display.jpg","bucket":"media"}}}]`,
		},
		objects: map[string]string{"map-photos/s-1/a/thumb.jpg": "THUMB", "media/s-1/a/display.jpg": "DISPLAY"},
	}
	c := signedIn(t, f)
	for size, want := range map[string]string{"thumb": "THUMB", "display": "DISPLAY"} {
		p, err := c.Photo(context.Background(), "6f1c2c8e-0000-4000-8000-000000000001", size)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(p.Body)
		p.Body.Close()
		if string(body) != want || p.Type != "image/jpeg" {
			t.Fatalf("%s: %q %q", size, body, p.Type)
		}
	}
	if n := len(f.find("POST", "rpc/media_assets_lookup")); n != 1 {
		t.Fatalf("사진 위치는 한 번만 묻습니다: %d", n)
	}
	if _, err := c.Photo(context.Background(), "../etc/passwd", "thumb"); !errors.Is(err, ErrNoPhoto) {
		t.Fatalf("id 가 아니면 없다고: %v", err)
	}
}

func TestEventRepeatAndReminderOnlyWhenChosen(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"POST calendar_events":  `[{"id":"e1","title":"합주","start_date":"2026-11-24","created_by":"u-me","updated_at":"t"}]`,
		"GET calendar_events":   `[{"id":"e1","title":"합주","start_date":"2026-11-24","created_by":"u-me","owner_user_id":"u-me","recurrence":"weekly","updated_at":"t"}]`,
		"PATCH calendar_events": `[{"id":"e1","title":"합주","start_date":"2026-11-24","created_by":"u-me","updated_at":"t"}]`,
	}}
	c := signedIn(t, f)
	ctx := context.Background()

	weekly, at := "weekly", 60
	if _, err := c.Put(ctx, event.Event{Title: "합주", Date: "2026-11-24", Start: "21:00", Repeat: &weekly, Reminder: &at}); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", "calendar_events")[0].Body
	if b["recurrence"] != "weekly" || b["reminder_minutes"] != float64(60) || b["reminder_tz"] != "Asia/Seoul" {
		t.Fatalf("고른 반복과 알림: %+v", b)
	}
	if _, err := c.Put(ctx, event.Event{Title: "합주", Date: "2026-11-24"}); err != nil {
		t.Fatal(err)
	}
	b = f.find("POST", "calendar_events")[1].Body
	if b["recurrence"] != nil || b["reminder_minutes"] != nil {
		t.Fatalf("고르지 않았으면 없음: %+v", b)
	}

	// A whole entry sent back without a repeat leaves the series a series.
	if _, err := c.Put(ctx, event.Event{ID: "e1", Title: "합주", Date: "2026-11-24"}); err != nil {
		t.Fatal(err)
	}
	if _, has := f.find("PATCH", "calendar_events")[0].Body["recurrence"]; has {
		t.Fatal("반복을 보내지 않은 수정은 반복을 건드리지 않습니다")
	}
	none, off := "none", -1
	if _, err := c.Put(ctx, event.Event{ID: "e1", Title: "합주", Date: "2026-11-24", Repeat: &none, Reminder: &off}); err != nil {
		t.Fatal(err)
	}
	b = f.find("PATCH", "calendar_events")[1].Body
	if v, has := b["recurrence"]; !has || v != nil {
		t.Fatalf("반복 끄기: %+v", b)
	}
	if v, has := b["reminder_minutes"]; !has || v != nil {
		t.Fatalf("알림 끄기: %+v", b)
	}
}
