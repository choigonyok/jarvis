package uniple

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
		if path, ok := strings.CutPrefix(r.URL.Path, "/storage/v1/object/upload/sign/"); ok && r.Method == http.MethodPut {
			raw, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.seen = append(f.seen, seenReq{Method: "PUT", Table: "storage:" + path, Query: url.Values{"type": {r.Header.Get("Content-Type")}, "size": {fmt.Sprint(len(raw))}}})
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
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

func TestRecordGoesWithItsPhotos(t *testing.T) {
	f := &fakeREST{answers: map[string]string{"GET blocks": mapBlock, "DELETE map_records": `[{"id":"r1"}]`}}
	c := signedIn(t, f)
	if err := c.DeleteRecord(context.Background(), "b-map", "r1"); err != nil {
		t.Fatal(err)
	}
	photos := f.find("DELETE", "map_visits")
	if len(photos) != 1 || photos[0].Query.Get("record_id") != "eq.r1" || photos[0].Query.Get("block_id") != "eq.b-map" {
		t.Fatalf("기록의 사진부터: %+v", photos)
	}
	if len(f.find("DELETE", "map_records")) != 1 {
		t.Fatal("그다음 기록")
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
	if _, err := c.AddBlock(context.Background(), "photos", ""); !errors.As(err, &invalid) {
		t.Fatalf("없는 블록: %v", err)
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

// An upload asks the app's server for a slot with a session cookie it can
// check but not refresh, puts the bytes in the slot and marks the asset ready.
func TestUploadGoesThroughTheAppsSlot(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks": mapBlock,
	}}
	c := signedIn(t, f)
	var asked map[string]any
	var session map[string]any
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/media/uploads" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var joined string
		for i := 0; ; i++ {
			ck, err := r.Cookie(fmt.Sprintf("%s.%d", cookieName, i))
			if err != nil {
				break
			}
			joined += ck.Value
		}
		if ck, err := r.Cookie(cookieName); err == nil {
			joined = ck.Value
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(joined, "base64-"))
		if !strings.HasPrefix(joined, "base64-") || err != nil || json.Unmarshal(raw, &session) != nil {
			t.Errorf("세션 쿠키를 읽지 못했습니다: %.40q", joined)
		}
		_ = json.NewDecoder(r.Body).Decode(&asked)
		item := asked["items"].([]any)[0].(map[string]any)
		id := item["assetId"].(string)
		f.answers["POST rpc/complete_media_uploads"] = `[{"id":"` + id + `","status":"ready"}]`
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
			"assetId": id, "files": []any{map[string]any{"role": "display", "path": "s-1/" + id + ".jpg", "signedUrl": c.cfg.BaseURL + "/storage/v1/object/upload/sign/media/s-1/" + id + ".jpg?token=t"}},
		}}})
	}))
	defer app.Close()
	c.cfg.AppURL = app.URL

	id, err := c.Upload(context.Background(), "b-map", "image/jpeg", []byte("JPEGDATA"))
	if err != nil || !uuidLike(id) {
		t.Fatalf("올리기: %q %v", id, err)
	}
	if session["access_token"] != "at" || session["refresh_token"] == "" || session["refresh_token"] == "rt" {
		t.Fatalf("쿠키는 접속 토큰만 진짜여야 합니다: %v", session)
	}
	if asked["blockId"] != "b-map" {
		t.Fatalf("블록의 자리: %v", asked)
	}
	put := f.find("PUT", "storage:media/s-1/"+id+".jpg")
	if len(put) != 1 || put[0].Query.Get("type") != "image/jpeg" || put[0].Query.Get("size") != "8" {
		t.Fatalf("저장소에 올리기: %+v", put)
	}
	if done := f.find("POST", "rpc/complete_media_uploads"); len(done) != 1 {
		t.Fatalf("마무리: %+v", done)
	}
	if len(f.find("POST", "rpc/abandon_media_uploads")) != 0 {
		t.Fatal("성공했으면 반납하지 않습니다")
	}

	// Not ready: the slot is given back.
	f.answers["POST rpc/complete_media_uploads"] = `[]`
	app.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		id := in["items"].([]any)[0].(map[string]any)["assetId"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"assetId": id, "files": []any{map[string]any{"role": "display", "uploaded": true}}}}})
	})
	if _, err := c.Upload(context.Background(), "b-map", "image/jpeg", []byte("x")); err == nil {
		t.Fatal("받지 않은 사진은 실패여야 합니다")
	}
	if len(f.find("POST", "rpc/abandon_media_uploads")) != 1 {
		t.Fatal("받지 않은 자리는 반납합니다")
	}
	var invalid event.ValidationError
	if _, err := c.Upload(context.Background(), "b-map", "image/gif", []byte("x")); !errors.As(err, &invalid) {
		t.Fatalf("gif 는 안 받습니다: %v", err)
	}
}

func TestSessionCookieChunks(t *testing.T) {
	long := "base64-" + strings.Repeat("a", cookieChunk*2+10)
	cks := sessionCookies(long)
	if len(cks) != 3 || cks[0].Name != cookieName+".0" || cks[2].Name != cookieName+".2" || cks[0].Value+cks[1].Value+cks[2].Value != long {
		t.Fatalf("나눠 담기: %d", len(cks))
	}
	if one := sessionCookies("base64-short"); len(one) != 1 || one[0].Name != cookieName {
		t.Fatalf("한 개: %+v", one)
	}
}

func TestPhotosOnTheMap(t *testing.T) {
	asset := "6f1c2c8e-0000-4000-8000-000000000002"
	f := &fakeREST{answers: map[string]string{
		"GET blocks":      mapBlock,
		"GET map_records": `[{"id":"r1","region_code":"S04","order_index":0,"created_at":"t"}]`,
		"GET map_visits":  `[{"id":"v0","region_code":"S04","asset_id":"a","created_at":"t","is_rep":false}]`,
		"POST map_visits": `[{"id":"v1","region_code":"S04","asset_id":"` + asset + `","created_at":"t","is_rep":false,"record_id":"r1"}]`,
	}}
	c := signedIn(t, f)
	ctx := context.Background()
	v, err := c.AddVisit(ctx, "b-map", VisitInput{Record: "r1", Asset: asset})
	if err != nil || v.Region != "11040" || v.Record != "r1" {
		t.Fatalf("기록의 사진: %+v %v", v, err)
	}
	b := f.find("POST", "map_visits")[0].Body
	if b["region_code"] != "S04" || b["record_id"] != "r1" || b["is_rep"] != false || b["order_index"] != float64(1) {
		t.Fatalf("기록의 지역, 맨 뒤: %+v", b)
	}
	if _, err := c.AddVisit(ctx, "b-map", VisitInput{Region: "31580", Asset: asset, Face: true}); err != nil && !errors.Is(err, event.ErrNotFound) {
		t.Fatal(err)
	}
	face := f.find("POST", "rpc/add_map_face_asset")
	if len(face) != 1 || face[0].Body["p_region_code"] != "31580" || face[0].Body["p_asset_id"] != asset || face[0].Body["p_clip_asset_id"] != nil {
		t.Fatalf("대표 사진은 앱의 호출로: %+v", face)
	}
	var invalid event.ValidationError
	if _, err := c.AddVisit(ctx, "b-map", VisitInput{Record: "r1", Asset: "../x"}); !errors.As(err, &invalid) {
		t.Fatalf("올린 사진이 아니면: %v", err)
	}
}

func TestCalendarDayPhotos(t *testing.T) {
	asset := "6f1c2c8e-0000-4000-8000-000000000003"
	f := &fakeREST{answers: map[string]string{
		"GET calendar_day_photos":  `[{"id":"p0"},{"id":"p1"}]`,
		"POST calendar_day_photos": `[{"id":"p2","date":"2026-11-11","asset_id":"` + asset + `","event_id":"e1","is_cover":false}]`,
	}}
	c := signedIn(t, f)
	got, err := c.AddDayPhoto(context.Background(), DayPhotoInput{Date: "2026-11-11", Asset: asset, Event: "e1#2026-11-11"})
	if err != nil || got.ID != "p2" {
		t.Fatalf("날 사진: %+v %v", got, err)
	}
	b := f.find("POST", "calendar_day_photos")[0].Body
	if b["event_id"] != "e1" || b["sort_order"] != float64(2) || b["block_id"] != "b-cal" || b["space_id"] != "s-1" {
		t.Fatalf("반복 일정의 하루는 시리즈에, 맨 뒤에: %+v", b)
	}
	if err := c.CoverDayPhoto(context.Background(), "6f1c2c8e-0000-4000-8000-0000000000aa"); err != nil {
		t.Fatal(err)
	}
	if cover := f.find("POST", "rpc/set_calendar_day_photo_cover"); len(cover) != 1 {
		t.Fatalf("대표 사진: %+v", cover)
	}
}

func TestBlocksKept(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks": `[{"id":"b1","type":"calendar","order_index":0},{"id":"b2","type":"bucket","order_index":1}]`,
	}}
	c := signedIn(t, f)
	ctx := context.Background()
	var invalid event.ValidationError
	if err := c.OrderBlocks(ctx, []string{"b2"}); !errors.As(err, &invalid) {
		t.Fatalf("모든 블록이어야: %v", err)
	}
	if err := c.OrderBlocks(ctx, []string{"b2", "b1"}); err != nil {
		t.Fatal(err)
	}
	if n := len(f.find("PATCH", "blocks")); n != 2 {
		t.Fatalf("바뀐 것만 둘: %d", n)
	}
	if err := c.ArchiveBlock(ctx, "b1"); !errors.As(err, &invalid) {
		t.Fatalf("캘린더는 앱에서 보관: %v", err)
	}
	up, region := true, "31"
	if err := c.UpdateBlock(ctx, "b2", BlockInput{ShowUpcoming: &up, CardRegion: &region}); err != nil {
		t.Fatal(err)
	}
	patch := f.find("POST", "rpc/patch_block_config")
	if len(patch) != 1 {
		t.Fatalf("설정은 앱의 호출로: %+v", patch)
	}
	set := patch[0].Body["p_set"].(map[string]any)
	if set["showUpcoming"] != true || set["card_region"] != "31" {
		t.Fatalf("설정: %+v", set)
	}
	bad := "seoul"
	if err := c.UpdateBlock(ctx, "b2", BlockInput{CardRegion: &bad}); !errors.As(err, &invalid) {
		t.Fatalf("시/도 코드만: %v", err)
	}
}

func TestStartDateNeedsTheSpace(t *testing.T) {
	f := &fakeREST{answers: map[string]string{"GET blocks": `[{"id":"b-d","type":"dday"}]`}}
	c := signedIn(t, f)
	var invalid event.ValidationError
	if err := c.SetStart(context.Background(), "b-d", "", "2026-08-15"); !errors.As(err, &invalid) {
		t.Fatalf("공간을 못 바꾸면 아무것도 안 바꿉니다: %v", err)
	}
	if len(f.find("PATCH", "dday_items")) != 0 || len(f.find("POST", "dday_items")) != 0 {
		t.Fatal("시작일 항목도 그대로")
	}
	f.answers["PATCH spaces"] = `[{"id":"s-1"}]`
	f.answers["GET dday_items"] = `[{"id":"d0","title":"우리","date":"2026-08-14","is_start_date":true}]`
	if err := c.SetStart(context.Background(), "b-d", "", "2026-08-15"); err != nil {
		t.Fatal(err)
	}
	if p := f.find("PATCH", "dday_items"); len(p) != 1 || p[0].Body["date"] != "2026-08-15" || p[0].Query.Get("id") != "eq.d0" {
		t.Fatalf("시작일 항목: %+v", p)
	}
	if len(f.find("POST", "rpc/sync_dday_calendar_events")) != 1 {
		t.Fatal("캘린더 기념일도")
	}
}
