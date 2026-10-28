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
	"github.com/choigonyok/jarvis/mind-svc/internal/checklist"
	"github.com/choigonyok/jarvis/mind-svc/internal/clock"
	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/interpret"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
	"github.com/choigonyok/jarvis/mind-svc/internal/mind"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/moments"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
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

// guestGraph is the guest's memory as memory-svc would answer it.
type guestGraph struct{ searches int }

func (g *guestGraph) Put(context.Context, []model.Fact) ([]memory.Result, error) { return nil, nil }
func (g *guestGraph) Profile(context.Context, string) (memory.Profile, error) {
	return memory.Profile{}, nil
}
func (g *guestGraph) Find(_ context.Context, name string, _ int) ([]memory.Node, error) {
	return []memory.Node{{ID: "n-other", Name: "손님"}, {ID: "n-self", Name: name}}, nil
}
func (g *guestGraph) Search(_ context.Context, _, center string, _ int) ([]memory.Hit, error) {
	g.searches++
	self := model.MemoryNode{ID: center, Name: "손님(나)"}
	jarvis := model.MemoryNode{ID: "n-jarvis", Name: "Jarvis(비서)"}
	return []memory.Hit{
		{Fact: "손님(나)은 Jarvis에게 답에서 이모티콘을 빼 달라고 요청했다.", ValidAt: "2026-10-08T15:30:17+09:00", Source: self, Target: jarvis},
		{Fact: "손님(나)은 Jarvis에게 답을 영어로 해 달라고 요청했다.", ValidAt: "2026-10-08T11:54:58+09:00", InvalidAt: "2026-10-09T00:00:00+09:00", Source: self, Target: jarvis},
		{Fact: "지수는 Jarvis의 답이 길다고 느꼈다.", ValidAt: "2026-10-08T15:30:17+09:00", Source: model.MemoryNode{ID: "n-op", Name: "지수"}, Target: jarvis},
		{Fact: "Jarvis(비서)는 손님(나)을 '민지 님'이라고 부르기로 했다.", ValidAt: "2026-10-26T01:00:00+09:00", Source: jarvis, Target: self},
	}, nil
}

// The guest's token reads the guest's own situation and taste at /now -
// their plans, their phone, their reminders and lists, what they said of
// themselves - and nothing of the operator's; the operator's is as it was.
func TestGuestNow(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Date(2026, 10, 26, 21, 30, 0, 0, seoul)
	graph := &guestGraph{}
	m := mind.New(mind.Options{
		Store: store.NewMemory(), Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(),
		Notifier: &act.MemoryNotifier{}, Loc: seoul, Now: func() time.Time { return now }, Logger: quiet, Policy: gate.DefaultPolicy(), Mode: "stack",
		Reminders: moments.NewMemory(), Checklists: checklist.NewMemory(), GuestMemory: graph,
		Here: func(context.Context) ([]moments.Visit, error) { return []moments.Visit{{Region: "회사", Kind: "work"}}, nil },
		GuestHere: func(context.Context) ([]moments.Visit, error) {
			return []moments.Visit{{Region: "집", Kind: "home", EnteredAt: time.Date(2026, 10, 26, 21, 3, 0, 0, seoul)}}, nil
		},
	})
	ctx := context.Background()
	for _, p := range []model.State{
		{Title: "운영자 회식", Start: "19:00", End: "23:00", Place: "성수"},
		{Title: "상대 합주", Start: "20:00", End: "23:00", Place: "합정 연습실", Owner: "partner"},
	} {
		p.Status, p.Date = model.Confirmed, "2026-10-26"
		if _, err := m.World.Create(ctx, model.KindPlan, "calendar", p, world.Meta{Actor: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	api := New(m, "secret", "", false, nil, quiet)
	api.SetGuestToken("guest-secret")
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()
	do := func(token, method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	at := now.Add(2 * time.Hour).Format("2006-01-02T15:04")
	do("guest-secret", "POST", "/api/reminders", `{"text":"학원 가방 챙기기","at":"`+at+`"}`)
	do("guest-secret", "POST", "/api/checklists", `{"title":"학원 준비물","items":["필통"]}`)
	do("secret", "POST", "/api/reminders", `{"text":"우산 말리기","at":"`+at+`"}`)
	do("secret", "POST", "/api/checklists", `{"title":"캠핑 준비물","items":["장갑"]}`)

	type situation struct {
		Text  string
		Prefs *string
	}
	read := func(token string) situation {
		code, body := do(token, "GET", "/now", "")
		var s situation
		if err := json.Unmarshal([]byte(body), &s); err != nil || code != 200 {
			t.Fatalf("/now (%s): %d %s", token, code, body)
		}
		return s
	}
	// The taste is read in the background: there by the next turn.
	var g situation
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if g = read("guest-secret"); g.Prefs != nil || time.Now().After(deadline) {
			break
		}
	}
	for _, want := range []string{"상대 합주", "- 위치: 21:03부터 집", "학원 가방 챙기기", "학원 준비물"} {
		if !strings.Contains(g.Text, want) {
			t.Fatalf("손님 상황에 %q 없음:\n%s", want, g.Text)
		}
	}
	for _, not := range []string{"운영자 회식", "우산 말리기", "캠핑 준비물", "회사"} {
		if strings.Contains(g.Text, not) {
			t.Fatalf("손님 상황에 운영자의 %q:\n%s", not, g.Text)
		}
	}
	wantTaste := "- 손님(나)은 Jarvis에게 답에서 이모티콘을 빼 달라고 요청했다. (확정 · 대화 10/8)\n" +
		"- Jarvis(비서)는 손님(나)을 '민지 님'이라고 부르기로 했다. (확정 · 대화 10/26)"
	if g.Prefs == nil || *g.Prefs != wantTaste {
		t.Fatalf("손님 취향: %v", g.Prefs)
	}
	// Read again within the half hour: no new search.
	searches := graph.searches
	read("guest-secret")
	if graph.searches != searches {
		t.Fatalf("30분 안에 다시 검색함: %d → %d", searches, graph.searches)
	}

	o := read("secret")
	for _, want := range []string{"운영자 회식", "우산 말리기", "캠핑 준비물"} {
		if !strings.Contains(o.Text, want) {
			t.Fatalf("운영자 상황에 %q 없음:\n%s", want, o.Text)
		}
	}
	for _, not := range []string{"상대 합주", "학원", "이모티콘"} {
		if strings.Contains(o.Text, not) || o.Prefs != nil && strings.Contains(*o.Prefs, not) {
			t.Fatalf("운영자 상황에 손님의 %q: %+v", not, o)
		}
	}
	if code, _ := do("nope", "GET", "/now", ""); code != http.StatusUnauthorized {
		t.Fatalf("토큰 없이 /now: %d", code)
	}
}

// The guest's token opens the guest's own reminders and checklists and
// nothing else of mind; each sees and changes only their own - another's is
// not found.
func TestGuestOwnRemindersAndLists(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := mind.New(mind.Options{
		Store: store.NewMemory(), Log: obs.NewMemoryLog(quiet), LLM: &llm.Script{}, Calendar: act.NewMemoryCalendar(),
		Notifier: &act.MemoryNotifier{}, Loc: seoul, Now: time.Now, Logger: quiet, Policy: gate.DefaultPolicy(), Mode: "stack",
		Reminders: moments.NewMemory(), Checklists: checklist.NewMemory(),
	})
	api := New(m, "secret", "", false, nil, quiet)
	api.SetGuestToken("guest-secret")
	srv := httptest.NewServer(api.Handler())
	defer srv.Close()
	do := func(token, method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	id := func(body, kind string) string {
		var v map[string]map[string]any
		if json.Unmarshal([]byte(body), &v) != nil {
			t.Fatalf("%s: %s", kind, body)
		}
		s, _ := v[kind]["id"].(string)
		return s
	}
	at := time.Now().Add(2 * time.Hour).In(seoul).Format("2006-01-02T15:04")

	code, body := do("guest-secret", "POST", "/api/reminders", `{"text":"학원 가방 챙기기","at":"`+at+`"}`)
	if code != 200 || !strings.Contains(body, `"who":"guest"`) {
		t.Fatalf("손님 알림 만들기: %d %s", code, body)
	}
	guestRem := id(body, "reminder")
	_, body = do("secret", "POST", "/api/reminders", `{"text":"우산 말리기","at":"`+at+`"}`)
	ownerRem := id(body, "reminder")
	if _, body := do("guest-secret", "GET", "/api/reminders", ""); !strings.Contains(body, "학원 가방") || strings.Contains(body, "우산 말리기") {
		t.Fatalf("손님 목록: %s", body)
	}
	if _, body := do("secret", "GET", "/api/reminders", ""); strings.Contains(body, "학원 가방") || !strings.Contains(body, "우산 말리기") {
		t.Fatalf("운영자 목록: %s", body)
	}
	// The 작업 tab's history: a cancelled one is in the last days' list, and
	// only there - still each person's own.
	_, body = do("secret", "POST", "/api/reminders", `{"text":"택배 받기","at":"`+at+`"}`)
	if code, _ := do("secret", "DELETE", "/api/reminders/"+id(body, "reminder"), ""); code != 200 {
		t.Fatalf("알림 취소: %d", code)
	}
	if _, body := do("secret", "GET", "/api/reminders", ""); strings.Contains(body, "택배 받기") {
		t.Fatalf("기다리는 목록에 취소한 알림: %s", body)
	}
	if _, body := do("secret", "GET", "/api/reminders?days=30", ""); !strings.Contains(body, "택배 받기") || !strings.Contains(body, `"status":"cancelled"`) || strings.Contains(body, "학원 가방") {
		t.Fatalf("내역: %s", body)
	}
	if _, body := do("guest-secret", "GET", "/api/reminders?days=30", ""); strings.Contains(body, "택배 받기") || !strings.Contains(body, "학원 가방") {
		t.Fatalf("손님 내역: %s", body)
	}
	for _, c := range []struct{ token, method, path string }{
		{"guest-secret", "PATCH", "/api/reminders/" + ownerRem},
		{"guest-secret", "DELETE", "/api/reminders/" + ownerRem},
		{"secret", "DELETE", "/api/reminders/" + guestRem},
	} {
		if code, _ := do(c.token, c.method, c.path, `{"text":"바꿈"}`); code != http.StatusNotFound {
			t.Fatalf("남의 알림 %s %s: %d", c.method, c.path, code)
		}
	}

	code, body = do("guest-secret", "POST", "/api/checklists", `{"title":"학원 준비물","items":["필통","교재"]}`)
	if code != 200 || !strings.Contains(body, `"who":"guest"`) {
		t.Fatalf("손님 체크리스트: %d %s", code, body)
	}
	guestList := id(body, "checklist")
	_, body = do("secret", "POST", "/api/checklists", `{"title":"캠핑 준비물","items":["장갑"]}`)
	ownerList := id(body, "checklist")
	if _, body := do("secret", "GET", "/api/checklists", ""); strings.Contains(body, "학원 준비물") || !strings.Contains(body, "캠핑 준비물") {
		t.Fatalf("운영자 체크리스트 목록: %s", body)
	}
	if _, body := do("guest-secret", "GET", "/api/checklists?ids="+ownerList+","+guestList, ""); strings.Contains(body, "캠핑") || !strings.Contains(body, "학원 준비물") {
		t.Fatalf("손님이 카드로 읽는 목록: %s", body)
	}
	for _, c := range []struct{ token, method, path string }{
		{"secret", "GET", "/api/checklists/" + guestList},
		{"guest-secret", "POST", "/api/checklists/" + ownerList + "/items/i1"},
		{"guest-secret", "DELETE", "/api/checklists/" + ownerList},
	} {
		if code, _ := do(c.token, c.method, c.path, `{"done":true}`); code != http.StatusNotFound {
			t.Fatalf("남의 체크리스트 %s %s: %d", c.method, c.path, code)
		}
	}
	if code, _ := do("guest-secret", "POST", "/api/reminders", `{"text":"남의 목록으로","at":"`+at+`","checklist":"`+ownerList+`"}`); code != http.StatusNotFound {
		t.Fatalf("손님 알림에 운영자 목록: %d", code)
	}
	if code, _ := do("guest-secret", "POST", "/api/checklists/"+guestList+"/items/i1", `{"done":true}`); code != 200 {
		t.Fatalf("손님이 자기 목록 체크: %d", code)
	}
	// Nothing else of mind opens to the guest's token.
	for _, path := range []string{"/api/snapshot", "/status", "/api/recall?q=x"} {
		if code, _ := do("guest-secret", "GET", path, ""); code != http.StatusUnauthorized {
			t.Fatalf("손님 토큰으로 %s: %d", path, code)
		}
	}
}
