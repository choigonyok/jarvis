package api

import (
	"context"
	"encoding/json"
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
	mu   sync.Mutex
	got  []string
	s    *Server
	done bool
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
