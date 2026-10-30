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
	mu    sync.Mutex
	asked []string
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

// A page of the thread comes with the lists its cards show, each once.
func TestThreadCarriesItsChecklists(t *testing.T) {
	l := &lists{}
	s := New(Deps{Thread: chatSvc(t), Proposals: proposal.NewStore(bus.New()), Runner: bgRunner{}, Checklists: l, APIToken: "svc"})
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
}
