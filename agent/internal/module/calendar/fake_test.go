package calendar

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

// fakeService stands in for calendar-svc.
//
// The tests here are about the module - its cards, its patch semantics, its
// ordering contract - and those should not need Postgres running to pass. What
// this does have to be faithful about is the behaviour the module depends on:
// ids minted by the service, all-day entries sorting to the top of their day,
// and 404 for a missing id.
//
// The real service's own storage behaviour (the derived range, the timezone of
// it) is verified against a real database, not here.
type fakeService struct {
	mu     sync.Mutex
	events map[string]Event
	seq    int
}

func newFakeService(t *testing.T) *Store {
	t.Helper()
	f := &fakeService{events: map[string]Event{}}
	server := httptest.NewServer(f.handler())
	t.Cleanup(server.Close)

	store := NewStore(server.URL, "", bus.New())
	if err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return store
}

func (f *fakeService) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
		f.mu.Lock()
		defer f.mu.Unlock()

		out := make([]Event, 0, len(f.events))
		for _, e := range f.events {
			if from != "" && e.Date < from {
				continue
			}
			if to != "" && e.Date > to {
				continue
			}
			out = append(out, e)
		}
		sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
		writeTestJSON(w, http.StatusOK, map[string]any{"events": out})
	})

	mux.HandleFunc("GET /events/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		e, ok := f.events[r.PathValue("id")]
		if !ok {
			writeTestJSON(w, http.StatusNotFound, map[string]string{"error": "그런 일정이 없습니다"})
			return
		}
		writeTestJSON(w, http.StatusOK, e)
	})

	mux.HandleFunc("PUT /events", func(w http.ResponseWriter, r *http.Request) {
		var in Event
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeTestJSON(w, http.StatusBadRequest, map[string]string{"error": "본문을 읽을 수 없습니다."})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if in.ID == "" {
			// 서비스가 id 를 발급한다는 점이 파일 저장소와의 차이이고, 이
			// 테스트가 재현해야 하는 부분이다.
			f.seq++
			in.ID = fmt.Sprintf("e-%d-%d", time.Now().UnixMilli(), f.seq)
		}
		in.UpdatedAt = time.Now().Format(time.RFC3339)
		f.events[in.ID] = in
		writeTestJSON(w, http.StatusOK, in)
	})

	mux.HandleFunc("DELETE /events/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.PathValue("id")
		gone, ok := f.events[id]
		if !ok {
			writeTestJSON(w, http.StatusNotFound, map[string]string{"error": "그런 일정이 없습니다"})
			return
		}
		delete(f.events, id)
		writeTestJSON(w, http.StatusOK, gone)
	})

	return mux
}

// less is the ordering the month grid expects: by date, then by start time,
// with an all-day entry at the top of its day.
func less(a, b Event) bool {
	if a.Date != b.Date {
		return a.Date < b.Date
	}
	if a.Start != b.Start {
		if a.Start == "" || b.Start == "" {
			return a.Start == ""
		}
		return a.Start < b.Start
	}
	return a.ID < b.ID
}

func writeTestJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
