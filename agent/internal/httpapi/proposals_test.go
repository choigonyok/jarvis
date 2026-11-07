package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/jsonfile"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// A page of the thread carries the cards it shows - those its turns point
// at, however old - and the live ones: every card still waiting (drawn apart
// from any turn) and those decided lately (the record, the pages above). A
// card decided long ago that no turn on the page points at stays out.
func TestThreadCarriesTheCardsItShows(t *testing.T) {
	now := time.Now()
	weeks := now.Add(-30 * 24 * time.Hour)
	id := func(at time.Time, seq int) string { return fmt.Sprintf("p-%d-%d", at.UnixMilli(), seq) }
	onPage, offPage, waiting, lately := id(weeks, 1), id(weeks, 2), id(weeks, 3), id(now.Add(-time.Hour), 4)
	path := filepath.Join(t.TempDir(), "proposals.json")
	err := jsonfile.Save(path, map[string]any{"seq": 4, "proposals": []proposal.Proposal{
		{ID: onPage, Origin: proposal.OriginChat, State: proposal.Executed, Card: action.Card{Title: "한 달 전 일정"}},
		{ID: offPage, Origin: proposal.OriginChat, State: proposal.Rejected, Card: action.Card{Title: "한 달 전 반려"}},
		{ID: waiting, Origin: proposal.OriginNotice, State: proposal.Pending, Card: action.Card{Title: "로그인해 주세요"}},
		{ID: lately, Origin: proposal.OriginSuggest, State: proposal.Approved, Card: action.Card{Title: "방금 승인"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	props := proposal.NewStore(bus.New())
	if err := props.Persist(path, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}

	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"turns": []thread.Turn{
			{ID: "u-1", Role: thread.RoleUser, Text: "일정 넣어줘"},
			{ID: "a-2", Role: thread.RoleAgent, ProposalID: onPage},
			{ID: "a-3", Role: thread.RoleAgent, Paragraphs: []string{"넣었어요"}},
		}, "hasMore": true})
	}))
	defer chat.Close()
	s := New(Deps{Thread: thread.NewStore(chat.URL, "", bus.New()), Proposals: props, Runner: bgRunner{}, APIToken: "svc"})
	req := httptest.NewRequest(http.MethodGet, "/thread?limit=60", nil)
	req.Header.Set("Authorization", "Bearer svc")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var page struct {
		Proposals []proposal.Proposal `json:"proposals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got []string
	for _, p := range page.Proposals {
		got = append(got, p.ID)
	}
	if !slices.Equal(got, []string{onPage, waiting, lately}) {
		t.Fatalf("페이지가 보여 줄 카드: %v (보여 줄 것 %v, 빠질 것 %s)", got, []string{onPage, waiting, lately}, offPage)
	}
	if page.Proposals[0].Card.Title != "한 달 전 일정" || page.Proposals[0].State != proposal.Executed {
		t.Fatalf("보관한 카드는 그대로: %+v", page.Proposals[0])
	}
	if p, ok := props.Get(offPage); !ok || p.State != proposal.Rejected {
		t.Fatalf("빠진 카드도 id 로는 찾습니다: %+v %v", p, ok)
	}
}

// The record reads the archive on its own: ?scope=archive is every card the
// archive holds, while GET /proposals stays the live ones. A scope it does not
// know is refused rather than read as either.
func TestTheArchiveIsListedApart(t *testing.T) {
	now := time.Now()
	weeks := now.Add(-30 * 24 * time.Hour)
	id := func(at time.Time, seq int) string { return fmt.Sprintf("p-%d-%d", at.UnixMilli(), seq) }
	old, waiting := id(weeks, 1), id(weeks, 2)
	path := filepath.Join(t.TempDir(), "proposals.json")
	err := jsonfile.Save(path, map[string]any{"seq": 2, "proposals": []proposal.Proposal{
		{ID: old, Origin: proposal.OriginChat, State: proposal.Executed, Card: action.Card{Title: "한 달 전 일정"}},
		{ID: waiting, Origin: proposal.OriginNotice, State: proposal.Pending, Card: action.Card{Title: "로그인해 주세요"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	props := proposal.NewStore(bus.New())
	if err := props.Persist(path, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	s := New(Deps{Thread: thread.NewStore("http://127.0.0.1:1", "", bus.New()), Proposals: props, Runner: bgRunner{}, APIToken: "svc"})
	get := func(target string) (int, []string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Authorization", "Bearer svc")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		var body struct {
			Proposals []proposal.Proposal `json:"proposals"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		ids := []string{}
		for _, p := range body.Proposals {
			ids = append(ids, p.ID)
		}
		return rec.Code, ids
	}
	if code, ids := get("/proposals"); code != http.StatusOK || !slices.Equal(ids, []string{waiting}) {
		t.Fatalf("살아 있는 카드: %d %v", code, ids)
	}
	if code, ids := get("/proposals?scope=archive"); code != http.StatusOK || !slices.Equal(ids, []string{old}) {
		t.Fatalf("보관한 카드: %d %v", code, ids)
	}
	if code, _ := get("/proposals?scope=everything"); code != http.StatusBadRequest {
		t.Fatalf("모르는 scope: %d", code)
	}

	// An archive that cannot be read is said as a failure, not as no cards.
	broken := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(filepath.Join(filepath.Dir(broken), "proposals-archive.json"), []byte("{깨진"), 0o600); err != nil {
		t.Fatal(err)
	}
	props = proposal.NewStore(bus.New())
	if err := props.Persist(broken, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	s = New(Deps{Thread: thread.NewStore("http://127.0.0.1:1", "", bus.New()), Proposals: props, Runner: bgRunner{}, APIToken: "svc"})
	if code, _ := get("/proposals?scope=archive"); code != http.StatusInternalServerError {
		t.Fatalf("읽지 못한 보관 파일: %d", code)
	}
}
