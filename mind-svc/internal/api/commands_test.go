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
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/gocommon/queue"
	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/checklist"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	jobs "github.com/choigonyok/jarvis/mind-svc/internal/jobs/api"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

func commandServer(t *testing.T) (*Server, http.Handler, *mind.Mind) {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	seoul, _ := time.LoadLocation("Asia/Seoul")
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, seoul)
	m := mind.New(mind.Options{
		Store: store.NewMemory(), Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(),
		Notifier: &act.MemoryNotifier{}, Loc: seoul, Now: func() time.Time { return now }, Logger: quiet, Policy: gate.DefaultPolicy(), Mode: "stack",
		Reminders: moments.NewMemory(), Checklists: checklist.NewMemory(),
	})
	s := New(m, "tok", "", false, nil, quiet)
	s.SetGuestToken("guest-tok")
	return s, s.Handler(), m
}

func command(kind, key, actor, method, path, body string) queue.Message {
	raw, _ := json.Marshal(map[string]any{"method": method, "path": path, "body": json.RawMessage(body)})
	return queue.Message{Kind: kind, Source: "agent", SourceKey: key, Actor: actor, Payload: raw}
}

func read(t *testing.T, h http.Handler, token, key string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/commands/"+key, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// A reminder asked for through the stream is kept by the same handler, and
// the answer is waiting at the window, for the one who asked.
func TestACommandDoesItAndLeavesTheAnswer(t *testing.T) {
	s, h, m := commandServer(t)
	ctx := context.Background()
	msg := command("reminder.set", "cmd-1", "operator", "POST", "/api/reminders", `{"text":"약 먹기","at":"2026-10-31T20:00:00+09:00","source":"chat"}`)
	if code, _ := read(t, h, "tok", "cmd-1"); code != http.StatusNotFound {
		t.Fatalf("처리 전: %d", code)
	}
	if err := s.Command(ctx, msg); err != nil {
		t.Fatal(err)
	}
	code, out := read(t, h, "tok", "cmd-1")
	if code != http.StatusOK || out["status"] != float64(200) {
		t.Fatalf("%d %v", code, out)
	}
	raw, _ := json.Marshal(out["body"])
	if !strings.Contains(string(raw), "약 먹기") {
		t.Fatalf("%s", raw)
	}
	rs, _ := m.Moments.ListOf(ctx, moments.Owner, false)
	if len(rs) != 1 {
		t.Fatalf("%+v", rs)
	}
	// Delivered again it is not done again.
	if err := s.Command(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if rs, _ := m.Moments.ListOf(ctx, moments.Owner, false); len(rs) != 1 {
		t.Fatalf("두 번 했습니다: %+v", rs)
	}
}

// What it refuses is an answer too, in its words.
func TestARefusedCommandIsAnAnswer(t *testing.T) {
	s, h, _ := commandServer(t)
	if err := s.Command(context.Background(), command("reminder.set", "cmd-2", "operator", "POST", "/api/reminders", `{"text":""}`)); err != nil {
		t.Fatal(err)
	}
	code, out := read(t, h, "tok", "cmd-2")
	raw, _ := json.Marshal(out["body"])
	if code != http.StatusOK || out["status"] != float64(http.StatusBadRequest) || !strings.Contains(string(raw), "error") {
		t.Fatalf("%d %v", code, out)
	}
}

// The guest's are the guest's: kept as the guest's, and read only by the guest.
func TestAGuestsCommandIsTheGuests(t *testing.T) {
	s, h, m := commandServer(t)
	ctx := context.Background()
	if err := s.Command(ctx, command("reminder.set", "cmd-3", "guest", "POST", "/api/reminders", `{"text":"손님 약","at":"2026-10-31T20:00:00+09:00","source":"chat"}`)); err != nil {
		t.Fatal(err)
	}
	if rs, _ := m.Moments.ListOf(ctx, moments.Guest, false); len(rs) != 1 {
		t.Fatalf("손님 것: %+v", rs)
	}
	if rs, _ := m.Moments.ListOf(ctx, moments.Owner, false); len(rs) != 0 {
		t.Fatalf("운영자 것에 들어갔습니다: %+v", rs)
	}
	if code, _ := read(t, h, "guest-tok", "cmd-3"); code != http.StatusOK {
		t.Fatalf("손님이 자기 답을: %d", code)
	}
	if code, _ := read(t, h, "tok", "cmd-3"); code != http.StatusNotFound {
		t.Fatalf("운영자 토큰으로 손님 답을: %d", code)
	}
}

// An answer to a card the agent holds is not a command.
func TestARecordWithNoPathIsNotACommand(t *testing.T) {
	s, _, _ := commandServer(t)
	raw, _ := json.Marshal(map[string]any{"card": "p-1", "decision": "approve"})
	if err := s.Command(context.Background(), queue.Message{Kind: "intent.answer", SourceKey: "answer:p-1", Payload: raw}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.Opts.Store.Command(context.Background(), "answer:p-1"); err == nil {
		t.Fatal("남길 것이 없습니다")
	}
}

// fakeStream is the stream from the door's side: what it is given, the lane
// does at once.
type fakeStream struct {
	mu      sync.Mutex
	got     []string
	lists   []string
	entries []string
	s       *Server
	done    bool
}

func (f *fakeStream) CalendarChanged(_ context.Context, guest bool, op string, event json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, fmt.Sprintf("%s:%v:%s", op, guest, event))
	return nil
}

func (f *fakeStream) ListChanged(_ context.Context, guest bool, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists = append(f.lists, fmt.Sprintf("%s:%v", id, guest))
	return nil
}

func (f *fakeStream) Command(ctx context.Context, key, kind, actor, method, path string, body json.RawMessage) error {
	f.mu.Lock()
	f.got = append(f.got, kind+" "+actor+" "+method+" "+path)
	f.mu.Unlock()
	if !f.done {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"method": method, "path": path, "body": body})
	return f.s.Command(ctx, queue.Message{Kind: kind, SourceKey: key, Actor: actor, Payload: raw})
}

// At the door nothing changes but through the stream: the console and the
// screen get the lane's answer as if the route had done it.
func TestTheDoorGoesThroughTheStream(t *testing.T) {
	s, h, m := commandServer(t)
	stream := &fakeStream{s: s, done: true}
	s.SetStream(stream)
	req := httptest.NewRequest(http.MethodPost, "/api/reminders", strings.NewReader(`{"text":"약","at":"2026-10-31T20:00:00+09:00","source":"console"}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "약") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(stream.got) != 1 || stream.got[0] != "reminder.set operator POST /api/reminders" {
		t.Fatalf("%v", stream.got)
	}
	if rs, _ := m.Moments.ListOf(context.Background(), moments.Owner, false); len(rs) != 1 {
		t.Fatalf("%+v", rs)
	}
}

// A card answered through the stream runs, and its result is the answer.
func TestAnAnswerRunsTheCard(t *testing.T) {
	s, h, m := commandServer(t)
	ctx := context.Background()
	in, err := m.Gate.Submit(ctx, model.Intent{Skill: "t", Kind: "note.add", Title: "메모", Body: "내용", Confidence: 0.5, Topic: "x", Reversible: true})
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != model.Pending {
		t.Skipf("이 종류는 물어보지 않습니다: %s", in.Status)
	}
	if err := s.Command(ctx, command("intent.answer", "cmd-4", "operator", "POST", "/api/intents/"+in.ID+"/reject", `{"reason":"아니요"}`)); err != nil {
		t.Fatal(err)
	}
	if code, out := read(t, h, "tok", "cmd-4"); code != http.StatusOK || out["status"] != float64(200) {
		t.Fatalf("%d %v", code, out)
	}
	got, _ := m.Opts.Store.Intent(ctx, in.ID)
	if got.Status != model.Rejected {
		t.Fatalf("%s", got.Status)
	}
}

// The jobs are for those who hold a token; the guest's opens their side.
func TestTheJobsWantAToken(t *testing.T) {
	m := mind.New(mind.Options{Store: store.NewMemory(), Log: obs.NewMemoryLog(slog.New(slog.NewTextHandler(io.Discard, nil))),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now, Loc: time.UTC})
	s := New(m, "svc", "", false, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.SetGuestToken("guest")
	s.SetJobs(jobs.New(nil, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil))))
	h := s.Handler()
	for path, want := range map[string]int{"/api/jobs/jobs": http.StatusUnauthorized, "/api/jobs/due": http.StatusUnauthorized} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/jobs/jobs", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("쓰기: %d", rec.Code)
	}
}

// A list changed from any screen or tool tells the screens showing it - the
// guest's on the guest's side.
func TestAChangedListTellsTheScreens(t *testing.T) {
	s, h, _ := commandServer(t)
	stream := &fakeStream{s: s, done: true}
	s.SetStream(stream)
	do := func(token, method, path, body string) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	var made struct {
		Checklist struct {
			ID string `json:"id"`
		} `json:"checklist"`
	}
	if err := json.Unmarshal([]byte(do("tok", http.MethodPost, "/api/checklists", `{"title":"캠핑","items":["장갑","베개"]}`)), &made); err != nil {
		t.Fatal(err)
	}
	if len(stream.lists) != 0 {
		t.Fatalf("만든 목록은 카드가 함께 갑니다: %v", stream.lists)
	}
	id := made.Checklist.ID
	do("tok", http.MethodPost, "/api/checklists/"+id+"/items/i1", `{"done":true}`)
	do("tok", http.MethodPatch, "/api/checklists/"+id, `{"remove":["i2"],"by":"card"}`)
	do("tok", http.MethodDelete, "/api/checklists/"+id, "")
	if want := id + ":false"; strings.Join(stream.lists, " ") != want+" "+want+" "+want {
		t.Fatalf("%v", stream.lists)
	}
}

type carried func(*http.Request) (*http.Response, error)

func (c carried) RoundTrip(r *http.Request) (*http.Response, error) { return c(r) }

// What a person changes on the calendar goes to the adapter's fixed API as they
// left it, from their side, and the screens hear what it became.
func TestAScreenEditGoesByTheHand(t *testing.T) {
	s, h, _ := commandServer(t)
	s.SetGuestToken("guest")
	stream := &fakeStream{s: s, done: true}
	s.SetStream(stream)
	var asked []string
	s.SetCalendar(CalendarDoor{URL: "http://calendar:8093", Token: "cal", HTTP: &http.Client{Transport: carried(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		asked = append(asked, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("X-Jarvis-Side")+" "+r.Header.Get("Authorization")+" "+string(body)))
		status := http.StatusOK
		if strings.Contains(r.URL.Path, "none") {
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"e1","title":"옮김"}`))}, nil
	})}})
	do := func(token, method, path, body string) int {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := do("tok", http.MethodPatch, "/api/calendar/s1%232026-10-31", `{"title":"옮김"}`); code != http.StatusOK {
		t.Fatal(code)
	}
	if code := do("guest", http.MethodDelete, "/api/calendar/e2", ""); code != http.StatusOK {
		t.Fatal(code)
	}
	if code := do("tok", http.MethodPatch, "/api/calendar/none", `{}`); code != http.StatusNotFound {
		t.Fatal(code)
	}
	want := []string{
		`PATCH /events/s1%232026-10-31  Bearer cal {"title":"옮김"}`,
		`DELETE /events/e2 partner Bearer cal`,
		`PATCH /events/none  Bearer cal {}`,
	}
	if strings.Join(asked, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", asked)
	}
	if len(stream.got) != 3 || !strings.HasPrefix(stream.got[0], "manual.edit operator PATCH") || !strings.HasPrefix(stream.got[1], "manual.edit guest DELETE") {
		t.Fatalf("%v", stream.got)
	}
	if len(stream.entries) != 2 || !strings.HasPrefix(stream.entries[0], "upsert:false:") || !strings.HasPrefix(stream.entries[1], "delete:true:") {
		t.Fatalf("화면에 알린 것: %v", stream.entries)
	}
}
