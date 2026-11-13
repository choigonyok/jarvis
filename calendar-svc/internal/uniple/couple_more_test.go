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
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		f.mu.Lock()
		f.seen = append(f.seen, seenReq{Method: r.Method, Table: table, Query: r.URL.Query(), Body: body})
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
