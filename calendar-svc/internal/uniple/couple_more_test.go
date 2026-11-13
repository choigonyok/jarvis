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

// routed stands in for PostgREST where one table answers differently by what
// is asked of it: answer sees the method, the table and the query.
type routed struct {
	mu     sync.Mutex
	seen   []seenReq
	raw    []string // "METHOD table body", for bodies that are not one object
	answer func(method, table string, q url.Values, body map[string]any) (int, string)
}

func (f *routed) find(method, table string) []seenReq {
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

func signedInRouted(t *testing.T, f *routed) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		table := strings.TrimPrefix(r.URL.Path, "/rest/v1/")
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		f.mu.Lock()
		f.seen = append(f.seen, seenReq{Method: r.Method, Table: table, Query: r.URL.Query(), Body: body})
		f.raw = append(f.raw, r.Method+" "+table+" "+string(raw))
		f.mu.Unlock()
		code, out := f.answer(r.Method, table, r.URL.Query(), body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, out)
	}))
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

// The app's server is told of a habit day checked and of a face put on the
// map, as the app tells it, so the partner's phone hears of it.
func TestThePartnerIsToldAsTheAppTells(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks":                  `[{"id":"b-w","type":"workout"}]`,
		"POST workout_checks":         `[{"id":"c1","user_id":"u-me","date":"2026-11-13","memo":null}]`,
		"POST rpc/add_map_face_asset": `null`,
	}}
	c := signedIn(t, f)
	var mu sync.Mutex
	var told []map[string]any
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/push/notify-partner" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if _, err := r.Cookie(cookieName); err != nil {
			t.Error("상대 알림에 세션 쿠키가 없습니다")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		told = append(told, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer app.Close()
	c.cfg.AppURL = app.URL

	if _, err := c.Check(context.Background(), "b-w", "2026-11-13", ""); err != nil {
		t.Fatal(err)
	}
	f.answers["GET blocks"] = mapBlock
	f.answers["GET map_visits"] = `[{"id":"v9","region_code":"S04","asset_id":"a9","is_rep":true}]`
	if _, err := c.AddVisit(context.Background(), "b-map", VisitInput{Region: "11040", Asset: "11111111-2222-3333-4444-555555555555", Face: true}); err != nil {
		t.Fatal(err)
	}
	c.pushes.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(told) != 2 {
		t.Fatalf("상대 알림 %d번: %v", len(told), told)
	}
	byBlock := map[string]map[string]any{}
	for _, b := range told {
		byBlock[b["blockId"].(string)] = b
	}
	if byBlock["b-w"]["date"] != "2026-11-13" {
		t.Errorf("습관 체크 알림: %v", byBlock["b-w"])
	}
	if id, _ := byBlock["b-map"]["itemId"].(string); !uuidLike(id) {
		t.Errorf("지도 사진 알림: %v", byBlock["b-map"])
	}
}

// A push that does not go is not the change's failure.
func TestAPushThatFailsLeavesTheChange(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks":          `[{"id":"b-w","type":"workout"}]`,
		"POST workout_checks": `[{"id":"c1","user_id":"u-me","date":"2026-11-13","memo":null}]`,
	}}
	c := signedIn(t, f)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer app.Close()
	c.cfg.AppURL = app.URL
	if _, err := c.Check(context.Background(), "b-w", "2026-11-13", ""); err != nil {
		t.Fatalf("알림이 실패해도 체크는 된 것입니다: %v", err)
	}
	c.pushes.Wait()
}

func TestItemsReorderedOnlyWhereTheyMoved(t *testing.T) {
	f := &routed{answer: func(method, table string, q url.Values, body map[string]any) (int, string) {
		switch {
		case method == "GET" && table == "blocks":
			return 200, `[{"id":"b-b","type":"bucket"}]`
		case method == "GET" && table == "bucket_items":
			return 200, `[{"id":"w1","order_index":0},{"id":"w2","order_index":1},{"id":"w3","order_index":2}]`
		}
		return 200, `[]`
	}}
	c := signedInRouted(t, f)
	if err := c.OrderItems(context.Background(), "b-b", "bucket", []string{"w1", "w3", "w2"}); err != nil {
		t.Fatal(err)
	}
	patched := f.find("PATCH", "bucket_items")
	if len(patched) != 2 {
		t.Fatalf("자리가 바뀐 두 개만 써야 합니다: %d", len(patched))
	}
	for _, p := range patched {
		id := strings.TrimPrefix(p.Query.Get("id"), "eq.")
		want := map[string]float64{"w3": 1, "w2": 2}[id]
		if p.Body["order_index"] != want || p.Query.Get("block_id") != "eq.b-b" {
			t.Errorf("%s: %v %v", id, p.Body, p.Query)
		}
	}

	// A list someone changed meanwhile is not half applied.
	for _, ids := range [][]string{{"w1", "w2"}, {"w1", "w2", "w3", "w9"}, {"w1", "w1", "w2"}} {
		err := c.OrderItems(context.Background(), "b-b", "bucket", ids)
		var invalid event.ValidationError
		if !errors.As(err, &invalid) {
			t.Errorf("%v: %v", ids, err)
		}
	}
	if err := c.OrderItems(context.Background(), "b-b", "checks", []string{"w1"}); err == nil {
		t.Error("습관 체크에는 순서가 없습니다")
	}
}

func TestFreeNoteRewritten(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks":       `[{"id":"b-f","type":"free"}]`,
		"PATCH free_items": `[{"id":"n1","content":"고친 말","created_by":"u-you","order_index":3,"created_at":"2026-11-01T00:00:00Z"}]`,
	}}
	c := signedIn(t, f)
	got, err := c.SetFree(context.Background(), "b-f", "n1", "  고친 말 ")
	if err != nil || got.Content != "고친 말" || got.By != "partner" {
		t.Fatalf("%+v %v", got, err)
	}
	p := f.find("PATCH", "free_items")[0]
	if p.Body["content"] != "고친 말" || p.Query.Get("id") != "eq.n1" || p.Query.Get("block_id") != "eq.b-f" {
		t.Fatalf("%v %v", p.Body, p.Query)
	}
	if _, err := c.SetFree(context.Background(), "b-f", "n1", "   "); err == nil {
		t.Fatal("빈 기록은 거절해야 합니다")
	}
}

func TestPutAwayBlocksComeBackLast(t *testing.T) {
	live := `[{"id":"b-1","type":"bucket","order_index":0},{"id":"b-2","type":"map","order_index":5}]`
	f := &routed{answer: func(method, table string, q url.Values, body map[string]any) (int, string) {
		switch {
		case method == "GET" && table == "blocks" && q.Get("archived_at") == "not.is.null" && q.Get("id") == "":
			return 200, `[{"id":"b-9","type":"bucket","title":"옛 버킷","order_index":2,"archived_at":"2026-11-10T03:00:00Z"}]`
		case method == "GET" && table == "blocks" && q.Get("archived_at") == "not.is.null":
			return 200, `[{"type":"` + map[string]string{"eq.b-9": "bucket", "eq.b-8": "map"}[q.Get("id")] + `"}]`
		case method == "GET" && table == "blocks":
			return 200, live
		case method == "PATCH" && table == "blocks":
			return 200, `[{"id":"b-9"}]`
		}
		return 200, `[]`
	}}
	c := signedInRouted(t, f)

	away, err := c.Archived(context.Background())
	if err != nil || len(away) != 1 || away[0].Archived == "" || away[0].Title != "옛 버킷" {
		t.Fatalf("%+v %v", away, err)
	}
	if err := c.Restore(context.Background(), "b-9"); err != nil {
		t.Fatal(err)
	}
	p := f.find("PATCH", "blocks")[0]
	if v, ok := p.Body["archived_at"]; !ok || v != nil || p.Body["order_index"] != float64(6) {
		t.Fatalf("보관을 풀고 맨 뒤로: %v", p.Body)
	}
	// One date map is all a space holds.
	err = c.Restore(context.Background(), "b-8")
	var invalid event.ValidationError
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "1개까지") {
		t.Fatalf("개수를 넘으면 거절: %v", err)
	}
}

// An anniversary the D-day block writes on the calendar is changed there:
// here it would be written over, or the two would drift apart.
func TestAnAnniversaryEntryIsChangedAtTheDDay(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET calendar_events": `[{"id":"e-a","title":"100일","start_date":"2026-11-22","created_by":"u-me","origin":"dday","updated_at":"x"}]`,
	}}
	c := signedIn(t, f)
	_, err := c.Delete(context.Background(), "e-a")
	var invalid event.ValidationError
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "디데이") {
		t.Fatalf("지우기: %v", err)
	}
	_, err = c.Put(context.Background(), event.Event{ID: "e-a", Title: "바꿈", Date: "2026-11-22"})
	if !errors.As(err, &invalid) {
		t.Fatalf("고치기: %v", err)
	}
	if len(f.find("DELETE", "calendar_events"))+len(f.find("PATCH", "calendar_events")) != 0 {
		t.Fatal("아무것도 쓰지 않아야 합니다")
	}
}

// A day of a series changed on its own, taken off, does not bring the
// series' own day back.
func TestAChangedDayTakenOffStaysOff(t *testing.T) {
	f := &routed{answer: func(method, table string, q url.Values, body map[string]any) (int, string) {
		if method == "GET" && table == "calendar_events" {
			switch q.Get("id") {
			case "eq.e-day":
				return 200, `[{"id":"e-day","title":"늦은 수업","start_date":"2026-11-18","created_by":"u-me","recurrence_parent_id":"e-s","recurrence_override_date":"2026-11-18","updated_at":"x"}]`
			case "eq.e-s":
				return 200, `[{"id":"e-s","title":"수업","start_date":"2026-11-04","created_by":"u-me","recurrence":"weekly","recurrence_exdates":["2026-11-11"],"updated_at":"x"}]`
			}
		}
		return 200, `[]`
	}}
	c := signedInRouted(t, f)
	if _, err := c.Delete(context.Background(), "e-day"); err != nil {
		t.Fatal(err)
	}
	patched := f.find("PATCH", "calendar_events")
	if len(patched) != 1 || patched[0].Query.Get("id") != "eq.e-s" {
		t.Fatalf("시리즈에 그날을 빼야 합니다: %+v", patched)
	}
	ex, _ := patched[0].Body["recurrence_exdates"].([]any)
	if len(ex) != 2 || ex[0] != "2026-11-11" || ex[1] != "2026-11-18" {
		t.Fatalf("예외 날짜: %v", ex)
	}
	gone := f.find("DELETE", "calendar_events")
	if len(gone) == 0 || gone[len(gone)-1].Query.Get("id") != "eq.e-day" {
		t.Fatalf("바꾼 날의 행을 지워야 합니다: %+v", gone)
	}
}

func TestOccurrenceKnowsWhereItsSeriesStarts(t *testing.T) {
	s := row{ID: "e-s", Title: "수업", StartDate: "2026-11-04", Recurrence: ptr("weekly")}
	got := occurrence(s, "2026-11-18").toEvent("u-me")
	if got.ID != "e-s#2026-11-18" || got.Date != "2026-11-18" || got.SeriesDate != "2026-11-04" {
		t.Fatalf("%+v", got)
	}
	if plain := s.toEvent("u-me"); plain.SeriesDate != "" {
		t.Fatalf("시리즈 자체에는 없습니다: %+v", plain)
	}
}

func TestAFaceTurns(t *testing.T) {
	f := &fakeREST{answers: map[string]string{
		"GET blocks":       mapBlock,
		"PATCH map_visits": `[{"id":"v1"}]`,
		"GET map_visits":   `[{"id":"v1","region_code":"S04","asset_id":"a1","is_rep":true,"rotation":90}]`,
	}}
	c := signedIn(t, f)
	turn := -270.0
	if _, err := c.SetVisit(context.Background(), "b-map", "v1", VisitChange{Rotation: &turn}); err != nil {
		t.Fatal(err)
	}
	p := f.find("PATCH", "map_visits")[0]
	if p.Body["rotation"] != float64(90) {
		t.Fatalf("각도는 0에서 360 사이로: %v", p.Body)
	}
	if _, ok := p.Body["asset_id"]; ok {
		t.Fatal("돌리기만 했는데 사진을 바꿨습니다")
	}
	if _, err := c.SetVisit(context.Background(), "b-map", "v1", VisitChange{}); err == nil {
		t.Fatal("바꿀 것이 없으면 거절")
	}
}

// What uniple answers is kept as what it was, so the screen can say what to do.
func TestRefusalsKeepTheirStatus(t *testing.T) {
	f := &routed{answer: func(method, table string, q url.Values, body map[string]any) (int, string) {
		if table == "free_items" {
			return 403, `{"code":"42501","message":"new row violates row-level security policy"}`
		}
		if table == "blocks" {
			return 200, `[{"id":"b-f","type":"free"}]`
		}
		return 521, ``
	}}
	c := signedInRouted(t, f)
	_, err := c.SetFree(context.Background(), "b-f", "n1", "말")
	var refused *Refused
	if !errors.As(err, &refused) || !refused.Forbidden() || refused.Down() {
		t.Fatalf("권한 거절: %v", err)
	}
	f.answer = func(method, table string, q url.Values, body map[string]any) (int, string) { return 521, `` }
	_, err = c.Archived(context.Background())
	if !errors.As(err, &refused) || !refused.Down() {
		t.Fatalf("uniple 장애: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

// A world map keeps its countries and trips in its own tables, through the
// same calls the date map takes.
func TestTheWorldMapKeepsItsOwn(t *testing.T) {
	const asset = "11111111-2222-3333-4444-555555555555"
	f := &routed{answer: func(method, table string, q url.Values, body map[string]any) (int, string) {
		switch {
		case method == "GET" && table == "blocks":
			return 200, `[{"id":"b-w","type":"worldmap"}]`
		case method == "GET" && table == "worldmap_visits" && q.Get("record_id") == "is.null":
			return 200, `[]`
		case method == "POST" && table == "rpc/set_worldmap_fill_asset":
			return 200, `[{"id":"wv1","country_code":"JP","asset_id":"` + asset + `","clip_asset_id":"` + asset + `","created_at":"x","record_id":null}]`
		case method == "POST" && table == "worldmap_records":
			return 201, `[{"id":"wr1","country_code":"JP","city":"교토","date_start":"2026-10-01","created_at":"x"}]`
		case method == "GET" && table == "worldmap_records":
			return 200, `[{"id":"wr1","country_code":"JP","city":"교토","created_at":"x"}]`
		case method == "POST" && table == "worldmap_visits":
			return 201, `[{"id":"wv2","country_code":"JP","asset_id":"` + asset + `","record_id":"wr1","created_at":"x"}]`
		case method == "DELETE" && table == "worldmap_records":
			return 200, `[{"id":"wr1"}]`
		}
		return 200, `[]`
	}}
	c := signedInRouted(t, f)
	var told []map[string]any
	var mu sync.Mutex
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		told = append(told, b)
		mu.Unlock()
	}))
	defer app.Close()
	c.cfg.AppURL = app.URL
	ctx := context.Background()

	face, err := c.AddVisit(ctx, "b-w", VisitInput{Region: "jp", Asset: asset, Face: true})
	if err != nil || face.Region != "JP" || !face.Rep || face.Clip != "" {
		t.Fatalf("얼굴: %+v %v", face, err)
	}
	fill := f.find("POST", "rpc/set_worldmap_fill_asset")[0].Body
	if fill["p_country_code"] != "JP" || fill["p_clip_asset_id"] != asset || fill["p_rotation"] != float64(0) || fill["p_space_id"] != "s-1" {
		t.Fatalf("나라 사진: %v", fill)
	}
	trip, err := c.AddRecord(ctx, "b-w", RecordInput{Region: ptr("JP"), Title: ptr("교토"), From: ptr("2026-10-01"), To: ptr("2026-10-03")})
	if err != nil || trip.Title != "교토" {
		t.Fatalf("여행: %+v %v", trip, err)
	}
	rec := f.find("POST", "worldmap_records")[0].Body
	if rec["city"] != "교토" || rec["country_code"] != "JP" || rec["date_end"] != "2026-10-03" || rec["title"] != nil {
		t.Fatalf("여행 기록: %v", rec)
	}
	if _, err := c.AddVisit(ctx, "b-w", VisitInput{Record: "wr1", Asset: asset}); err != nil {
		t.Fatal(err)
	}
	photo := f.find("POST", "worldmap_visits")[0].Body
	if photo["record_id"] != "wr1" || photo["country_code"] != "JP" {
		t.Fatalf("여행 사진: %v", photo)
	}
	if err := c.DeleteRecord(ctx, "b-w", "wr1"); err != nil {
		t.Fatal(err)
	}
	gone := f.find("DELETE", "worldmap_visits")
	if len(gone) != 1 || gone[0].Query.Get("record_id") != "eq.wr1" || len(f.find("DELETE", "worldmap_records")) != 1 {
		t.Fatalf("여행을 지우면 사진도: %+v", gone)
	}
	if _, err := c.AddVisit(ctx, "b-w", VisitInput{Region: "Japan", Asset: asset, Face: true}); err == nil {
		t.Fatal("나라는 두 글자 코드")
	}
	c.pushes.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(told) < 1 || told[0]["itemId"] != "wv1" {
		t.Fatalf("나라의 첫 사진은 상대 폰에: %v", told)
	}
}

func TestTheAlbum(t *testing.T) {
	const a1, a2 = "11111111-2222-3333-4444-555555555551", "11111111-2222-3333-4444-555555555552"
	f := &routed{answer: func(method, table string, q url.Values, body map[string]any) (int, string) {
		switch {
		case method == "GET" && table == "blocks":
			return 200, `[{"id":"b-a","type":"album"}]`
		case method == "POST" && table == "album_photos":
			return 201, `[{"id":"p1","uploaded_by":"u-me","asset_id":"` + a1 + `","media_type":"photo","width":1080,"height":810,"created_at":"x"},{"id":"p2","uploaded_by":"u-me","asset_id":"` + a2 + `","media_type":"photo","created_at":"y"}]`
		}
		return 200, `[]`
	}}
	c := signedInRouted(t, f)
	in := AlbumInput{Snap: true}
	in.Photos = append(in.Photos, struct {
		Asset  string `json:"asset"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}{a1, 1080, 810}, struct {
		Asset  string `json:"asset"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}{a2, 0, 0})
	got, err := c.AddAlbumPhotos(context.Background(), "b-a", in)
	if err != nil || len(got) != 2 || got[0].By != "me" || got[0].Width != 1080 {
		t.Fatalf("%+v %v", got, err)
	}
	var sent string
	for _, r := range f.raw {
		if strings.HasPrefix(r, "POST album_photos ") {
			sent = strings.TrimPrefix(r, "POST album_photos ")
		}
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(sent), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("한 번에 넣어야 합니다: %s", sent)
	}
	if rows[0]["uploaded_by"] != "u-me" || rows[0]["is_snap"] != true || rows[0]["media_type"] != "photo" || rows[1]["width"] != nil || rows[0]["space_id"] != "s-1" {
		t.Fatalf("앨범 행: %v", rows)
	}
	if err := c.DeleteAlbumPhotos(context.Background(), "b-a", []string{a1}); err != nil {
		t.Fatal(err)
	}
	del := f.find("POST", "rpc/album_bulk_delete")[0].Body
	if ids, _ := del["p_photo_ids"].([]any); len(ids) != 1 || del["p_block_id"] != "b-a" || !uuidLike(del["p_request_id"].(string)) {
		t.Fatalf("지우기: %v", del)
	}
	if err := c.PinAlbum(context.Background(), "b-a", ""); err != nil {
		t.Fatal(err)
	}
	if pin := f.find("POST", "rpc/set_album_pinned_asset")[0].Body; pin["p_asset_id"] != nil {
		t.Fatalf("홈에서 내리기: %v", pin)
	}
	if err := c.DeleteAlbumPhotos(context.Background(), "b-a", nil); err == nil {
		t.Fatal("고른 사진이 없으면 거절")
	}
}

// The blocks shown but not written are read with the columns uniple has, and
// what the two wrote reads from the viewer's side.
func TestShelvesReadWhatTheyHold(t *testing.T) {
	f := &routed{answer: func(method, table string, q url.Values, body map[string]any) (int, string) {
		switch table {
		case "blocks":
			return 200, `[{"id":"b-x","type":"` + q.Get("id")[len("eq.b-"):] + `","title":"t","order_index":0}]`
		case "log_entries":
			return 200, `[{"id":"l1","category":"movie","title":"헤어질 결심","rating":4.5,"note":"또 보자","photo_asset_ids":["p1"],"card_asset":"c1","experienced_on":"2026-10-01","experienced_end":"2026-10-02","created_by":"u-you","created_at":"x"}]`
		case "food_entries":
			return 200, `[{"id":"f1","meal_type":"dinner","title":"을지로 노포","rating":5,"tags":["mood","revisit"],"photo_asset_ids":null,"card_asset":"c2","experienced_on":"2026-11-01","created_by":"u-me","created_at":"x"}]`
		case "diary_entries":
			return 200, `[{"id":"d1","author_id":"u-you","entry_at":"2026-11-10T12:00:00Z","title":null,"body":"오늘","photo_asset_ids":[],"comment_count":1,"created_at":"x","diary_comments":[{"id":"c1","author_id":"u-me","body":"나도","created_at":"y"}]}]`
		case "budget_dates":
			return 200, `[{"id":"g1","title":"온천","start_on":"2026-09-07","end_on":null,"total_amount":115000,"expense_count":2,"top_category":"stay","memo":"좋았다","created_at":"x","budget_expenses":[{"id":"e1","amount":90000,"category":"stay","paid_by":"u-me","created_at":"x"},{"id":"e2","amount":25000,"category":"meal","paid_by":"shared","created_at":"y"}]}]`
		}
		return 200, `[]`
	}}
	c := signedInRouted(t, f)
	ctx := context.Background()
	b, err := c.Block(ctx, "b-log")
	if err != nil || len(b.Log) != 1 || b.Log[0].Title != "헤어질 결심" || *b.Log[0].Rating != 4.5 || b.Log[0].Cover != "p1" || b.Log[0].To != "2026-10-02" || b.Log[0].By != "partner" {
		t.Fatalf("문화 기록장: %+v %v", b.Log, err)
	}
	b, err = c.Block(ctx, "b-food")
	if err != nil || len(b.Food) != 1 || len(b.Food[0].Tags) != 2 || b.Food[0].Cover != "c2" || b.Food[0].By != "me" {
		t.Fatalf("맛집: %+v %v", b.Food, err)
	}
	b, err = c.Block(ctx, "b-diary")
	if err != nil || len(b.Diary) != 1 || b.Diary[0].By != "partner" || len(b.Diary[0].Replies) != 1 || b.Diary[0].Replies[0].By != "me" {
		t.Fatalf("일기: %+v %v", b.Diary, err)
	}
	b, err = c.Block(ctx, "b-budget")
	if err != nil || len(b.Budget) != 1 || b.Budget[0].Total != 115000 || len(b.Budget[0].Expenses) != 2 || b.Budget[0].Expenses[0].PaidBy != "me" || b.Budget[0].Expenses[1].PaidBy != "shared" {
		t.Fatalf("가계부: %+v %v", b.Budget, err)
	}
	for _, s := range f.find("GET", "budget_dates") {
		if !strings.Contains(s.Query.Get("select"), "budget_expenses(") {
			t.Fatalf("지출은 날짜에 붙여 읽습니다: %v", s.Query)
		}
	}
}

// A home whose shown-only tables uniple turns down still opens.
func TestTheHomeOpensWithoutTheShelves(t *testing.T) {
	f := &routed{answer: func(method, table string, q url.Values, body map[string]any) (int, string) {
		if table == "blocks" && strings.Contains(q.Get("select"), "log_entries(") {
			return 400, `{"code":"PGRST200","message":"Could not find a relationship"}`
		}
		if table == "blocks" {
			return 200, `[{"id":"b-b","type":"bucket","title":"버킷","order_index":0,"bucket_items":[{"id":"w1","title":"제주"}]}]`
		}
		return 200, `[]`
	}}
	c := signedInRouted(t, f)
	rows, err := c.blocks(context.Background(), "s-1", "")
	if err != nil || len(rows) != 1 || len(rows[0].BucketItems) != 1 {
		t.Fatalf("%+v %v", rows, err)
	}
	asked := f.find("GET", "blocks")
	if len(asked) != 2 || asked[1].Query.Get("log_entries.order") != "" || strings.Contains(asked[1].Query.Get("select"), "log_entries") {
		t.Fatalf("두 번째 읽기는 기본 목록으로: %+v", asked)
	}
}
