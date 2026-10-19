package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

type console struct {
	t   *testing.T
	srv *httptest.Server
	log *obs.MemoryLog
	m   *mind.Mind
}

func (c *console) post(path string, body any) (int, map[string]any) {
	c.t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(c.srv.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	c.log.Drain()
	c.m.Attn.Settle(context.Background())
	c.log.Drain()
	c.m.Attn.Settle(context.Background())
	return resp.StatusCode, out
}

func (c *console) snapshot() mind.Snapshot {
	c.t.Helper()
	resp, err := http.Get(c.srv.URL + "/api/snapshot")
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var s mind.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		c.t.Fatal(err)
	}
	return s
}

// The console end to end: a simulated conversation is read, the plan it
// settles becomes a card, approving it puts it on the calendar, undoing
// takes it off - and the same card is not raised again.
func TestConsoleEndToEnd(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := clock.New()
	clk.Pin(time.Date(2026, 10, 12, 20, 0, 0, 0, seoul))
	st := store.NewMemory()
	log := obs.NewMemoryLog(quiet)
	cal := act.NewMemoryCalendar()
	answers := []string{
		`{"changes":[{"op":"create","kind":"plan","set":{"title":"지수와 강릉 여행","status":"confirmed","date":"2026-10-17","place":"강릉","people":["지수"]},"evidence":[{"line":2,"quote":"그러자"}],"confidence":0.9}]}`,
	}
	script := &llm.Script{Answer: func(_, _ string) (string, error) {
		a := answers[0]
		return a, nil
	}}
	m := mind.New(mind.Options{
		Store: st, Log: log, LLM: script, Calendar: cal, Notifier: &act.MemoryNotifier{},
		Loc: seoul, Now: clk.Now, Logger: quiet, Interpret: interpret.Config{Owner: "나"},
		Policy: gate.DefaultPolicy(), Mode: "memory",
	})
	go log.Run(context.Background(), m.Handle)
	srv := httptest.NewServer(New(m, "secret", "console-secret", true, clk, quiet).Handler())
	defer srv.Close()
	c := &console{t: t, srv: srv, log: log, m: m}

	if code, _ := c.post("/api/simulate", map[string]any{"room": "jisu", "roomName": "지수", "sender": "지수", "text": "이번 주말에 강릉 갈까?"}); code != http.StatusCreated {
		t.Fatalf("simulate: %d", code)
	}
	c.post("/api/simulate", map[string]any{"room": "jisu", "roomName": "지수", "mine": true, "text": "그러자!"})
	if code, out := c.post("/api/read", map[string]any{"room": "sim:jisu"}); code != http.StatusOK {
		t.Fatalf("read: %d %v", code, out)
	}

	s := c.snapshot()
	if len(s.Entities) != 1 || s.Entities[0].State.Place != "강릉" {
		t.Fatalf("세계 모델: %+v", s.Entities)
	}
	var card model.Intent
	for _, in := range s.Intents {
		if in.Kind == "calendar.create" && in.Status == model.Pending {
			card = in
		}
	}
	if card.ID == "" || !strings.Contains(card.Preview, "강릉") || len(s.Notices) == 0 {
		t.Fatalf("카드와 알림이 있어야 합니다: %+v / %+v", s.Intents, s.Notices)
	}

	if code, _ := c.post("/api/intents/"+card.ID+"/approve", nil); code != http.StatusOK {
		t.Fatalf("approve: %d", code)
	}
	s = c.snapshot()
	if len(s.Calendar) != 1 || s.Entities[0].State.CalendarID == "" || len(s.Timers) == 0 {
		t.Fatalf("캘린더·연결·타이머: %+v / %+v / %+v", s.Calendar, s.Entities, s.Timers)
	}

	if code, _ := c.post("/api/intents/"+card.ID+"/undo", nil); code != http.StatusOK {
		t.Fatalf("undo: %d", code)
	}
	s = c.snapshot()
	if len(s.Calendar) != 0 || s.Entities[0].State.CalendarID != "" {
		t.Fatalf("되돌리면 캘린더에서 빠지고 연결이 끊겨야 합니다: %+v / %+v", s.Calendar, s.Entities)
	}
	for _, in := range s.Intents {
		if in.Kind == "calendar.create" && in.Status == model.Pending {
			t.Fatalf("되돌린 것과 같은 카드를 다시 올리면 안 됩니다: %+v", in)
		}
	}

	// The clock moves forward on request in memory mode.
	if code, out := c.post("/api/clock", map[string]string{"advance": "24h"}); code != http.StatusOK || out["now"] == nil {
		t.Fatalf("clock: %d %v", code, out)
	}

	// /consume always needs the token.
	resp, _ := http.Post(srv.URL+"/consume", "application/json", strings.NewReader(`{}`))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("consume without token: %d", resp.StatusCode)
	}
}

// In the stack the console is never open: the services' token comes as a
// bearer header; a person brings the console's own token once in the address
// bar, traded for a cookie - which does not open the services' own routes.
func TestClosedConsole(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := mind.New(mind.Options{
		Store: store.NewMemory(), Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(),
		Notifier: &act.MemoryNotifier{}, Loc: seoul, Now: time.Now, Logger: quiet, Policy: gate.DefaultPolicy(), Mode: "stack",
	})
	srv := httptest.NewServer(New(m, "secret", "console-secret", false, nil, quiet).Handler())
	defer srv.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	get := func(path string, set func(*http.Request)) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if set != nil {
			set(req)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	for _, path := range []string{"/", "/api/snapshot", "/status"} {
		if code := get(path, nil).StatusCode; code != http.StatusUnauthorized {
			t.Fatalf("%s without token: %d", path, code)
		}
	}
	if code := get("/?token=wrong", nil).StatusCode; code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", code)
	}
	// The services' token does not sign a person in to the console.
	if code := get("/?token=secret", nil).StatusCode; code != http.StatusUnauthorized {
		t.Fatalf("services' token as a console login: %d", code)
	}
	if code := get("/api/snapshot", func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") }).StatusCode; code != http.StatusOK {
		t.Fatalf("bearer: %d", code)
	}
	login := get("/?token=console-secret", nil)
	if login.StatusCode != http.StatusSeeOther || len(login.Cookies()) != 1 || !login.Cookies()[0].HttpOnly {
		t.Fatalf("login: %d %v", login.StatusCode, login.Cookies())
	}
	cookie := login.Cookies()[0]
	if code := get("/api/snapshot", func(r *http.Request) { r.AddCookie(cookie) }).StatusCode; code != http.StatusOK {
		t.Fatalf("cookie: %d", code)
	}
	if code := get("/status", func(r *http.Request) { r.AddCookie(cookie) }).StatusCode; code != http.StatusUnauthorized {
		t.Fatalf("the console's cookie opens the services' routes: %d", code)
	}
	if code := get("/status", func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") }).StatusCode; code == http.StatusUnauthorized {
		t.Fatalf("services' token on /status: %d", code)
	}
	if code := get("/", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: tokenCookie, Value: "wrong"}) }).StatusCode; code != http.StatusUnauthorized {
		t.Fatalf("wrong cookie: %d", code)
	}
	// The cookie is not the token, and an old one no longer opens anything.
	if cookie.Value == "secret" || cookie.Value == "console-secret" || cookie.MaxAge > 24*3600 {
		t.Fatalf("cookie must not be the token or last past a day: %+v", cookie)
	}
	for _, v := range []string{"secret", "console-secret", "1." + strings.Repeat("0", 64)} {
		if code := get("/", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: tokenCookie, Value: v}) }).StatusCode; code != http.StatusUnauthorized {
			t.Fatalf("cookie %q: %d", v, code)
		}
	}
}

// The conversation's way to the messages themselves: the services' token
// only, a short stretch around the word for each match, and the messages
// around one it picked.
func TestMessagesForTheConversation(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	at := time.Date(2026, 10, 12, 21, 0, 0, 0, seoul)
	long := strings.Repeat("가", 100) + " 갓생은 부지런히 사는 것 " + strings.Repeat("나", 100)
	for i, text := range []string{"갓생이 뭐야?", long, "아하"} {
		_ = st.PutMessage(context.Background(), model.Message{Ref: fmt.Sprintf("kakao:%d", i+1), Source: "kakao", Room: "kakao:uni", RoomName: "대학 동기방", Sender: "지수", SentAt: at.Add(time.Duration(i) * time.Minute), Text: text})
	}
	m := mind.New(mind.Options{
		Store: st, Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(),
		Notifier: &act.MemoryNotifier{}, Loc: seoul, Now: time.Now, Logger: quiet, Policy: gate.DefaultPolicy(), Mode: "stack",
	})
	srv := httptest.NewServer(New(m, "secret", "console-secret", false, nil, quiet).Handler())
	defer srv.Close()
	get := func(path string, bearer bool, out any) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if bearer {
			req.Header.Set("Authorization", "Bearer secret")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			_ = json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}
	if code := get("/messages/search?q=갓생", false, nil); code != http.StatusUnauthorized {
		t.Fatalf("토큰 없이: %d", code)
	}
	var found struct {
		Messages []messageOut `json:"messages"`
	}
	if code := get("/messages/search?"+url.Values{"q": {"갓생"}, "room": {"동기"}, "since": {"2026-10-12"}}.Encode(), true, &found); code != http.StatusOK || len(found.Messages) != 2 {
		t.Fatalf("검색: %d %+v", code, found)
	}
	snip := found.Messages[0]
	if snip.Ref != "kakao:2" || !strings.Contains(snip.Text, "갓생은 부지런히") || len([]rune(snip.Text)) > 125 || !strings.HasPrefix(snip.Text, "…") || !strings.HasSuffix(snip.Text, "…") {
		t.Fatalf("찾은 낱말 둘레만: %q", snip.Text)
	}
	if code := get("/messages/search?q=%20", true, nil); code != http.StatusBadRequest {
		t.Fatalf("빈 검색어: %d", code)
	}
	if code := get("/messages/search?q=x&since=어제", true, nil); code != http.StatusBadRequest {
		t.Fatalf("읽을 수 없는 날짜: %d", code)
	}
	var around struct {
		Messages []messageOut `json:"messages"`
	}
	if code := get("/messages/context?ref=kakao:2&before=1&after=1", true, &around); code != http.StatusOK || len(around.Messages) != 3 || around.Messages[1].Ref != "kakao:2" {
		t.Fatalf("앞뒤: %d %+v", code, around)
	}
	if n := len([]rune(around.Messages[1].Text)); n > 301 {
		t.Fatalf("한 메시지는 짧게: %d", n)
	}
	if code := get("/messages/context?ref=kakao:9", true, nil); code != http.StatusNotFound {
		t.Fatalf("없는 메시지: %d", code)
	}
}

// The 생활 screen's notes: every status, the services' token only in the
// stack, and the operator's word on one comes back as its next version.
func TestNotesForTheConsole(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	m := mind.New(mind.Options{
		Store: st, Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(),
		Notifier: &act.MemoryNotifier{}, Loc: seoul, Now: time.Now, Logger: quiet, Policy: gate.DefaultPolicy(), Mode: "stack",
	})
	srv := httptest.NewServer(New(m, "secret", "", false, nil, quiet).Handler())
	defer srv.Close()
	call := func(method, path, body string, token bool) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if token {
			req.Header.Set("Authorization", "Bearer secret")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, out := call(http.MethodGet, "/api/notes", "", true); code != http.StatusOK || len(out["notes"].([]any)) != 0 {
		t.Fatalf("no notes is an empty list: %d %v", code, out)
	}
	now := time.Now()
	for _, n := range []model.Note{
		{ID: "note_a", Text: "캠핑 며칠 전에 장작을 산다", Scope: "캠핑", Status: model.Hypothesis, Confidence: 0.6, Version: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "note_b", Text: "비 오는 날은 운동을 거의 안 한다", Scope: "운동", Status: model.Rejected, Confidence: 0.4, Version: 1, CreatedAt: now, UpdatedAt: now},
	} {
		if err := st.PutNote(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	if code, _ := call(http.MethodGet, "/api/notes", "", false); code != http.StatusUnauthorized {
		t.Fatalf("without the token: %d", code)
	}
	code, out := call(http.MethodGet, "/api/notes", "", true)
	if code != http.StatusOK || len(out["notes"].([]any)) != 2 {
		t.Fatalf("every status: %d %v", code, out)
	}
	code, out = call(http.MethodPost, "/api/notes/note_a", `{"status":"confirmed"}`, true)
	if code != http.StatusOK || out["status"] != "confirmed" || out["version"].(float64) != 2 || out["confidence"].(float64) < 0.9 {
		t.Fatalf("confirmed: %d %v", code, out)
	}
	code, out = call(http.MethodGet, "/api/notes/note_a/versions", "", true)
	if code != http.StatusOK || len(out["versions"].([]any)) != 2 {
		t.Fatalf("versions: %d %v", code, out)
	}
}
