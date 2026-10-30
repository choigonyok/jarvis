package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/calendar-svc/internal/event"
)

// book is the calendar as the service holds it: the operator's side.
type book struct {
	entries map[string]event.Event
	next    int
}

func (b *book) Ping(context.Context) error { return nil }

func (b *book) Range(context.Context, string, string) ([]event.Event, error) {
	out := []event.Event{}
	for _, e := range b.entries {
		out = append(out, e)
	}
	return out, nil
}

func (b *book) Get(_ context.Context, id string) (event.Event, error) {
	e, ok := b.entries[id]
	if !ok {
		return e, event.ErrNotFound
	}
	return e, nil
}

func (b *book) Put(_ context.Context, e event.Event) (event.Event, error) {
	if e.ID == "" {
		b.next++
		e.ID = fmt.Sprintf("e%d", b.next)
	}
	b.entries[e.ID] = e
	return e, nil
}

func (b *book) Delete(_ context.Context, id string) (event.Event, error) {
	e, ok := b.entries[id]
	if !ok {
		return e, event.ErrNotFound
	}
	delete(b.entries, id)
	return e, nil
}

func (b *book) Conflicts(context.Context, string, time.Duration) ([]event.Event, error) {
	return nil, nil
}

func sides(t *testing.T) (*book, func(token, method, path, body string) (int, event.Event)) {
	t.Helper()
	b := &book{entries: map[string]event.Event{}}
	h := New(b, "owner", "guest", slog.New(slog.NewTextHandler(io.Discard, nil)), nil).Handler()
	return b, func(token, method, path, body string) (int, event.Event) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var e event.Event
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		return rec.Code, e
	}
}

// The guest is the calendar's other person. What they make as their own
// reaches the service as the partner's, and the operator's own entries read to
// them as the partner's: the same calendar, seen from the other side.
func TestTheGuestSeesItFromTheirSide(t *testing.T) {
	b, do := sides(t)
	code, mine := do("owner", http.MethodPut, "/events", `{"date":"2026-10-10","title":"운영자 일정","owner":"me","source":"me"}`)
	if code != http.StatusOK || mine.Owner != event.OwnerMe {
		t.Fatalf("%d %+v", code, mine)
	}
	code, theirs := do("guest", http.MethodPut, "/events", `{"date":"2026-10-11","title":"손님 일정","owner":"me","source":"me"}`)
	if code != http.StatusOK || theirs.Owner != event.OwnerMe || theirs.Source != event.SourceHuman {
		t.Fatalf("손님에게 돌아온 자기 일정: %d %+v", code, theirs)
	}
	if stored := b.entries[theirs.ID]; stored.Owner != event.OwnerPartner || stored.Source != event.SourcePartner {
		t.Fatalf("서비스에는 상대 일정으로 가야 합니다: %+v", stored)
	}
	if _, seen := do("guest", http.MethodGet, "/events/"+mine.ID, ""); seen.Owner != event.OwnerPartner {
		t.Fatalf("운영자 일정이 손님에게 상대 일정으로 보여야 합니다: %+v", seen)
	}
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	req.Header.Set("Authorization", "Bearer guest")
	rec := httptest.NewRecorder()
	New(b, "owner", "guest", slog.New(slog.NewTextHandler(io.Discard, nil)), nil).Handler().ServeHTTP(rec, req)
	var list struct {
		Events []event.Event `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, e := range list.Events {
		if e.ID == theirs.ID && e.Owner != event.OwnerMe {
			t.Fatalf("목록에서도 손님 자신의 일정은 나: %+v", e)
		}
	}
	if len(list.Events) != 2 {
		t.Fatalf("%+v", list.Events)
	}
}

// A change on the screen is to an entry that exists, and keeps who made it.
func TestAnEditKeepsWhoMadeIt(t *testing.T) {
	b, do := sides(t)
	b.entries["e9"] = event.Event{ID: "e9", Date: "2026-10-10", Title: "캠핑", Owner: event.OwnerMe, Source: "jarvis"}
	code, saved := do("owner", http.MethodPatch, "/events/e9", `{"date":"2026-10-11","title":"캠핑 옮김","owner":"me","source":"me"}`)
	if code != http.StatusOK || saved.Title != "캠핑 옮김" || saved.Source != "jarvis" || saved.ID != "e9" {
		t.Fatalf("%d %+v", code, saved)
	}
	if code, _ := do("owner", http.MethodPatch, "/events/none", `{"date":"2026-10-11","title":"없는 일정"}`); code != http.StatusNotFound {
		t.Fatalf("없는 일정: %d", code)
	}
	if len(b.entries) != 1 {
		t.Fatalf("새 일정이 생기면 안 됩니다: %+v", b.entries)
	}
	if code, gone := do("guest", http.MethodDelete, "/events/e9", ""); code != http.StatusOK || gone.Owner != event.OwnerPartner {
		t.Fatalf("%d %+v", code, gone)
	}
}
