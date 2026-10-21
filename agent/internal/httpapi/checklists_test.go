package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// lists is the checklist module as the console's routes see it.
type lists struct {
	mu     sync.Mutex
	asked  []string
	ticked []string
}

func (l *lists) Find(_ context.Context, ids []string) (map[string]any, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, ids...)
	out := map[string]any{}
	for _, id := range ids {
		out[id] = map[string]any{"id": id, "title": "목록 " + id}
	}
	return out, nil
}

func (l *lists) Open(context.Context) (any, error) {
	return []map[string]any{{"id": "chk_open"}}, nil
}

func (l *lists) Drop(_ context.Context, id, item string) (any, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ticked = append(l.ticked, id+"/"+item+"x")
	return map[string]any{"id": id}, nil
}

func (l *lists) Close(_ context.Context, id string) (any, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ticked = append(l.ticked, id+" closed")
	return map[string]any{"id": id, "status": "archived"}, nil
}

func (l *lists) Check(_ context.Context, id, item string, done bool) (any, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ticked = append(l.ticked, id+"/"+item+map[bool]string{true: "+", false: "-"}[done])
	return map[string]any{"id": id}, nil
}

// chatSvc answers the thread's page with a checklist card, a notice that
// shows a list, and a typed message.
func chatSvc(t *testing.T) *thread.Store {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"turns": []thread.Turn{
			{ID: "a-1", Role: thread.RoleAgent, Origin: &thread.Origin{Kind: thread.OriginChecklist, Checklist: "chk_1", Title: "캠핑"}},
			{ID: "a-2", Role: thread.RoleAgent, Paragraphs: []string{"집에 왔어요"}, Origin: &thread.Origin{Kind: thread.OriginNotice, Checklist: "chk_2"}},
			{ID: "a-3", Role: thread.RoleAgent, Origin: &thread.Origin{Kind: thread.OriginChecklist, Checklist: "chk_1"}},
			{ID: "u-4", Role: thread.RoleUser, Text: "고마워"},
		}, "hasMore": false})
	}))
	t.Cleanup(srv.Close)
	return thread.NewStore(srv.URL, "", bus.New())
}

// A page of the thread comes with the lists its cards show, each once; a
// tap on an item goes to the list; jobs-svc's knock wakes the scheduler,
// with the services' token only.
func TestThreadCarriesItsChecklists(t *testing.T) {
	l := &lists{}
	woke := 0
	s := New(Deps{Thread: chatSvc(t), Proposals: proposal.NewStore(bus.New()), Runner: bgRunner{}, Checklists: l,
		WakeJobs: func() { woke++ }, APIToken: "svc"})
	h := s.Handler()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer svc")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := do(http.MethodGet, "/thread?limit=60", "")
	var page struct {
		Turns      []thread.Turn  `json:"turns"`
		Checklists map[string]any `json:"checklists"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(page.Turns) != 4 || len(page.Checklists) != 2 || page.Checklists["chk_1"] == nil || page.Checklists["chk_2"] == nil {
		t.Fatalf("카드가 보여 줄 목록이 함께 와야 합니다: %+v", page.Checklists)
	}
	if strings.Join(l.asked, ",") != "chk_1,chk_2" {
		t.Fatalf("목록마다 한 번: %v", l.asked)
	}

	rec = do(http.MethodPost, "/thread/checklists/chk_1/items/i2", `{"done":true}`)
	if rec.Code != http.StatusOK || len(l.ticked) != 1 || l.ticked[0] != "chk_1/i2+" {
		t.Fatalf("체크: %d %v", rec.Code, l.ticked)
	}
	rec = do(http.MethodGet, "/thread/checklists?ids=chk_9,%20chk_8", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "chk_8") {
		t.Fatalf("목록 읽기: %d %s", rec.Code, rec.Body)
	}
	// The 작업 tab: the lists still open; an item swiped away; a list closed.
	rec = do(http.MethodGet, "/thread/checklists", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"open":[{"id":"chk_open"}]`) {
		t.Fatalf("열린 목록: %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodDelete, "/thread/checklists/chk_1/items/i3", ""); rec.Code != http.StatusOK {
		t.Fatalf("빼기: %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodDelete, "/thread/checklists/chk_1", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "archived") {
		t.Fatalf("닫기: %d %s", rec.Code, rec.Body)
	}
	if got := strings.Join(l.ticked, ","); got != "chk_1/i2+,chk_1/i3x,chk_1 closed" {
		t.Fatalf("체크·빼기·닫기: %s", got)
	}

	if rec := do(http.MethodPost, "/jobs/wake", ""); rec.Code != http.StatusNoContent || woke != 1 {
		t.Fatalf("깨우기: %d %d", rec.Code, woke)
	}
	req := httptest.NewRequest(http.MethodPost, "/jobs/wake", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || woke != 1 {
		t.Fatalf("토큰 없는 깨우기는 막아야 합니다: %d", rec.Code)
	}
}
