package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/context-svc/internal/store"
	"github.com/choigonyok/jarvis/gocommon/notify"
)

// heard is what reaches input-svc, read as the service said it: a record's
// payload with the wording beside it, under the old names the tests ask by.
func heard(r *http.Request) map[string]any {
	var rec map[string]any
	_ = json.NewDecoder(r.Body).Decode(&rec)
	payload, _ := rec["payload"].(map[string]any)
	e := map[string]any{"source": rec["source"], "type": rec["kind"], "kind": rec["kind"], "subject": rec["subject"], "key": rec["source_key"], "data": payload}
	if n, ok := payload["notify"].(map[string]any); ok {
		e["notify"] = n
		for k, v := range n {
			e[k] = v
		}
	}
	return e
}

// phoneTest is a server with both phones, a clock that can be moved, and what
// reaches input-svc.
type phoneTest struct {
	t      *testing.T
	st     *store.Store
	server *Server
	srv    *httptest.Server
	seoul  *time.Location
	today  atomic.Value
	events chan map[string]any
}

func newPhoneTest(t *testing.T) *phoneTest {
	dsn := os.Getenv("CONTEXT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CONTEXT_TEST_DATABASE_URL 이 없어 건너뜁니다")
	}
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	resetTables(t, dsn, `truncate context_raw, context_regions, context_visits, context_state restart identity`)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := &phoneTest{t: t, st: st, events: make(chan map[string]any, 32)}
	p.seoul, _ = time.LoadLocation("Asia/Seoul")
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := heard(r)
		p.events <- e
	}))
	t.Cleanup(sink.Close)
	server := New(st, Config{Token: "api", User: "phone", Pass: "pw", GuestUser: "guest-phone", GuestPass: "gpw"}, notify.New(sink.URL, "", "", quiet), quiet)
	p.today.Store(time.Date(2026, 10, 23, 20, 0, 0, 0, p.seoul))
	server.now = func() time.Time { return p.today.Load().(time.Time) }
	p.server = server
	p.srv = httptest.NewServer(server.Handler())
	t.Cleanup(p.srv.Close)
	return p
}

func (p *phoneTest) do(method, path, body string, set func(*http.Request)) (int, string) {
	req, _ := http.NewRequest(method, p.srv.URL+path, strings.NewReader(body))
	set(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func bearerAPI(r *http.Request) { r.Header.Set("Authorization", "Bearer api") }

// say posts a report from a phone: the operator's (guest false) or the guest's.
func (p *phoneTest) say(guest bool, body string) string {
	user, pass := "phone", "pw"
	if guest {
		user, pass = "guest-phone", "gpw"
	}
	code, reply := p.do("POST", "/owntracks", body, func(r *http.Request) { r.SetBasicAuth(user, pass) })
	if code != 200 {
		p.t.Fatalf("보고: %d %s", code, reply)
	}
	return reply
}

func (p *phoneTest) at(hm string) int64 {
	tm, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-23 "+hm, p.seoul)
	return tm.Unix()
}

// track is a person's /track of 2026-10-23: its watch and the regions on their way.
func (p *phoneTest) track(who string) (watching, []string) {
	code, body := p.do("GET", "/track?date=2026-10-23&who="+who, "", bearerAPI)
	var out struct {
		Watch   watching
		Pending []string
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || code != 200 {
		p.t.Fatalf("track: %d %s", code, body)
	}
	return out.Watch, out.Pending
}

// Leaving home for nowhere moves a phone to Move at once - and the fix that
// comes with the leave, a stone's throw from home, does not undo it: the
// hours at home are not "still in one spot". Only a quarter of an hour still
// outside makes it settled again. Both phones.
func TestLeavingHome(t *testing.T) {
	p := newPhoneTest(t)
	if code, _ := p.do("PUT", "/regions", `{"name":"집","kind":"home","lat":37.5559,"lon":126.9104,"radiusM":50}`, bearerAPI); code != 200 {
		t.Fatalf("영역: %d", code)
	}
	const home, out = `"lat":37.5559,"lon":126.9104`, `"lat":37.5559,"lon":126.9118` // 120 m east
	for _, guest := range []bool{false, true} {
		who := map[bool]string{false: "운영자", true: "상대"}[guest]
		if guest {
			p.say(true, fmt.Sprintf(`{"_type":"waypoint","rid":"g1","desc":"집",%s,"rad":50,"tst":1}`, home))
		}
		p.say(guest, fmt.Sprintf(`{"_type":"location",%s,"acc":10,"tst":%d,"m":1,"batt":80}`, home, p.at("07:00")))
		if r := p.say(guest, fmt.Sprintf(`{"_type":"location",%s,"acc":10,"tst":%d,"m":1,"batt":80,"inregions":["집"]}`, home, p.at("08:30"))); strings.Contains(r, "monitoring") {
			t.Fatalf("%s: 집 안은 Significant 그대로: %s", who, r)
		}
		if r := p.say(guest, fmt.Sprintf(`{"_type":"transition","event":"leave","desc":"집",%s,"acc":10,"tst":%d}`, out, p.at("09:00"))); !strings.Contains(r, `"monitoring":2`) || !strings.Contains(r, `"action":"reportLocation"`) {
			t.Fatalf("%s: 집을 나서면 바로 Move, 지금 위치도: %s", who, r)
		}
		if w, _ := p.track(map[bool]string{false: "owner", true: "guest"}[guest]); w.Why != "left" || w.Mode != 2 {
			t.Fatalf("%s: 나선 이유: %+v", who, w)
		}
		fix := func(hm string) string {
			return p.say(guest, fmt.Sprintf(`{"_type":"location",%s,"acc":10,"tst":%d,"m":2,"batt":80,"motionactivities":["stationary"]}`, out, p.at(hm)))
		}
		if r := fix("09:00"); strings.Contains(r, "monitoring") {
			t.Fatalf("%s: 나선 자리의 위치가 Significant 로 되돌리면 안 됨: %s", who, r)
		}
		if r := fix("09:10"); strings.Contains(r, "monitoring") {
			t.Fatalf("%s: 나와서 10분은 그대로 Move: %s", who, r)
		}
		if r := fix("09:16"); !strings.Contains(r, `"monitoring":1`) {
			t.Fatalf("%s: 나와서 한자리에 15분 → Significant: %s", who, r)
		}
		if w, _ := p.track(map[bool]string{false: "owner", true: "guest"}[guest]); w.Why != "still" || w.Mode != 1 {
			t.Fatalf("%s: 한자리: %+v", who, w)
		}
	}
}

// Below 20% and not charging, a phone that should be in Move is not: the
// screen is told why (downgraded). Charging, Move again. A transition, which
// says no battery, goes by the last fix's.
func TestBatteryDowngrade(t *testing.T) {
	p := newPhoneTest(t)
	for _, guest := range []bool{false, true} {
		who := map[bool]string{false: "owner", true: "guest"}[guest]
		car := func(hm string, m int, batt string) string {
			return p.say(guest, fmt.Sprintf(`{"_type":"location","lat":37.5,"lon":127.0,"acc":10,"tst":%d,"m":%d,%s,"motionactivities":["automotive"]}`, p.at(hm), m, batt))
		}
		if r := car("10:00", 2, `"batt":15`); !strings.Contains(r, `"monitoring":1`) {
			t.Fatalf("%s: 배터리 15%% 면 Move 대신 Significant: %s", who, r)
		}
		if w, _ := p.track(who); !w.Downgraded || w.Why != "car" || w.Mode != 1 || w.Batt != 15 {
			t.Fatalf("%s: 다운그레이드 표시: %+v", who, w)
		}
		if r := car("10:05", 1, `"batt":16,"bs":2`); !strings.Contains(r, `"monitoring":2`) {
			t.Fatalf("%s: 충전 중이면 Move: %s", who, r)
		}
		if w, _ := p.track(who); w.Downgraded || w.Mode != 2 || !w.Charging {
			t.Fatalf("%s: 충전 중: %+v", who, w)
		}
	}
}

// Once a day each phone is put back: its settings, and every region to 50 m -
// here at once, on the phone when its whole list comes back. A point named on
// the phone (radius 0) is made a 50 m region at once; Here-… (not yet named)
// is no place. A radius changed on the phone holds until the next day.
func TestDailyUpkeep(t *testing.T) {
	p := newPhoneTest(t)
	if code, _ := p.do("PUT", "/regions", `{"name":"집","kind":"home","lat":37.5559,"lon":126.9104,"radiusM":120}`, bearerAPI); code != 200 {
		t.Fatalf("영역: %d", code)
	}
	fix := func(guest bool, hm string) string {
		return p.say(guest, fmt.Sprintf(`{"_type":"location","lat":37.4,"lon":127.2,"acc":10,"tst":%d,"m":2,"batt":80}`, p.at(hm)))
	}
	for _, guest := range []bool{false, true} {
		r := fix(guest, "10:00")
		for _, want := range []string{`"extendedData":true`, `"downgrade":20`, `"ignoreInaccurateLocations":100`, `"locatorInterval":60`, `"locatorDisplacement":50`, `"allowRemoteLocation":true`, `{"_type":"cmd","action":"waypoints"}`} {
			if !strings.Contains(r, want) {
				t.Fatalf("하루 첫 보고에 %s: %s", want, r)
			}
		}
		if r := fix(guest, "10:10"); strings.Contains(r, "setConfiguration") || strings.Contains(r, "waypoints") {
			t.Fatalf("같은 날 두 번은 아님: %s", r)
		}
	}
	regions, _ := p.st.Regions(context.Background())
	if len(regions) != 1 || regions[0].RadiusM != 50 {
		t.Fatalf("여기 영역은 바로 50m: %+v", regions)
	}
	// Made on the phone: a named point becomes a region at once; Here-… is
	// nothing; a radius set by hand stays until tomorrow.
	if r := p.say(false, `{"_type":"waypoint","rid":"c1","desc":"카페","lat":37.51,"lon":127.01,"rad":0,"tst":5}`); !strings.Contains(r, `"setWaypoints"`) || !strings.Contains(r, `"rad":50`) || !strings.Contains(r, `"rid":"c1"`) {
		t.Fatalf("점은 바로 50m 영역으로: %s", r)
	}
	if r := p.say(false, `{"_type":"waypoint","rid":"x1","desc":"Here-x1","lat":37.52,"lon":127.02,"rad":0,"tst":6}`); r != "[]\n" {
		t.Fatalf("이름 없는 점: %s", r)
	}
	if r := p.say(false, `{"_type":"waypoint","rid":"h1","desc":"집","lat":37.5559,"lon":126.9104,"rad":120,"tst":7}`); r != "[]\n" {
		t.Fatalf("손으로 바꾼 반경은 내일까지: %s", r)
	}
	regions, _ = p.st.Regions(context.Background())
	got := map[string]int{}
	for _, r := range regions {
		got[r.Name] = r.RadiusM
	}
	if got["카페"] != 50 || got["집"] != 120 || len(got) != 2 {
		t.Fatalf("영역: %+v", got)
	}
	// The next day: put back again - and the whole list, each by its id.
	p.today.Store(time.Date(2026, 10, 24, 9, 0, 0, 0, p.seoul))
	if r := fix(false, "23:00"); !strings.Contains(r, `"downgrade":20`) {
		t.Fatalf("다음 날 다시: %s", r)
	}
	if r := p.say(false, `{"_type":"waypoints","waypoints":[{"_type":"waypoint","rid":"h1","desc":"집","lat":37.5559,"lon":126.9104,"rad":120,"tst":7},{"_type":"waypoint","rid":"c1","desc":"카페","lat":37.51,"lon":127.01,"rad":50,"tst":5}]}`); !strings.Contains(r, `"rid":"h1"`) || strings.Contains(r, `"rid":"c1"`) {
		t.Fatalf("50m 가 아닌 것만 되돌림: %s", r)
	}
	regions, _ = p.st.Regions(context.Background())
	for _, r := range regions {
		if r.RadiusM != 50 {
			t.Fatalf("모두 50m: %+v", regions)
		}
	}
}

// The region that follows a phone (+follow): a whole list without one gets it,
// where that phone last was (none before its first fix), 50 m, by an id of its
// own. A list with one - any name the app takes for it, beginning with + -
// changes nothing: it is no place here, neither kept nor put back to 50 m, and
// its transitions move nothing. A phone put back the same day before the
// upkeep gave it is put back again, once. Both phones.
func TestFollowRegion(t *testing.T) {
	p := newPhoneTest(t)
	ctx := context.Background()
	const list = `{"_type":"waypoints","waypoints":[{"_type":"waypoint","rid":"h1","desc":"집","lat":37.5559,"lon":126.9104,"rad":50,"tst":7}%s]}`
	const own = `,{"_type":"waypoint","rid":"f9","desc":"+30","lat":37.6,"lon":127.1,"rad":420,"tst":8}`
	follow := func(r string) map[string]any {
		var cmds []struct {
			Waypoints struct{ Waypoints []map[string]any }
		}
		if err := json.Unmarshal([]byte(r), &cmds); err != nil {
			t.Fatalf("답: %s", r)
		}
		for _, c := range cmds {
			for _, wp := range c.Waypoints.Waypoints {
				if wp["desc"] == "+follow" {
					return wp
				}
			}
		}
		return nil
	}
	for _, guest := range []bool{false, true} {
		who, source := "운영자", ownerPhone
		if guest {
			who, source = "상대", guestPhone
		}
		if r := p.say(guest, fmt.Sprintf(list, "")); follow(r) != nil {
			t.Fatalf("%s: 어디 있는지 모르면 아직: %s", who, r)
		}
		p.say(guest, fmt.Sprintf(`{"_type":"location","lat":37.4,"lon":127.2,"acc":10,"tst":%d,"m":1,"batt":80}`, p.at("10:00")))
		r := p.say(guest, fmt.Sprintf(list, ""))
		if wp := follow(r); wp == nil || wp["rid"] != "follow" || wp["lat"] != 37.4 || wp["lon"] != 127.2 || wp["rad"] != 50.0 || strings.Contains(r, `"rid":"h1"`) {
			t.Fatalf("%s: 목록에 없으면 마지막 위치에 50m 로: %s", who, r)
		}
		if r := p.say(guest, fmt.Sprintf(list, own)); r != "[]\n" {
			t.Fatalf("%s: 있으면 그대로, 50m 로 되돌리지도 않음: %s", who, r)
		}
		if r := p.say(guest, `{"_type":"waypoint","rid":"f9","desc":"+30","lat":37.61,"lon":127.1,"rad":300,"tst":8}`); r != "[]\n" {
			t.Fatalf("%s: 하나만 와도: %s", who, r)
		}
		if r := p.say(guest, fmt.Sprintf(`{"_type":"transition","event":"enter","desc":"+30","lat":37.4,"lon":127.2,"acc":10,"tst":%d}`, p.at("10:05"))); r != "[]\n" {
			t.Fatalf("%s: 그 진입은 아무것도 바꾸지 않음: %s", who, r)
		}
		kept, err := p.server.phoneList(ctx, source)
		if err != nil || len(kept) != 1 || kept[0].Name != "집" {
			t.Fatalf("%s: 폰의 영역은 장소만: %+v %v", who, kept, err)
		}
	}
	if regions, _ := p.st.Regions(ctx); len(regions) != 1 || regions[0].Name != "집" {
		t.Fatalf("여기 영역에도 없음: %+v", regions)
	}
	if open, _ := p.st.Open(ctx); len(open) != 0 {
		t.Fatalf("체류도 없음: %+v", open)
	}
	// Put back today by the upkeep before it gave the region: again, once.
	if err := p.st.Set(ctx, "owntracks.daily", map[string]string{"day": "2026-10-23"}, p.today.Load().(time.Time)); err != nil {
		t.Fatal(err)
	}
	fix := func(hm string) string {
		return p.say(false, fmt.Sprintf(`{"_type":"location","lat":37.4,"lon":127.2,"acc":10,"tst":%d,"m":1,"batt":80}`, p.at(hm)))
	}
	if r := fix("10:10"); !strings.Contains(r, `"downgrade":20`) || !strings.Contains(r, `{"_type":"cmd","action":"waypoints"}`) {
		t.Fatalf("그날 한 번 더: %s", r)
	}
	if r := fix("10:20"); strings.Contains(r, "setConfiguration") || strings.Contains(r, "waypoints") {
		t.Fatalf("한 번만: %s", r)
	}
}

// 위치 등록: where a person is now becomes their phone's region (50 m) - here
// at once, without an arrival, and on the phone with its next reply. Until the
// phone's list has been heard, its list comes first: a region of the same name
// there is moved rather than doubled. Once it is known, the answer carries a
// link that puts it on the phone at once.
func TestRegisterRegion(t *testing.T) {
	p := newPhoneTest(t)
	register := func(guest bool, body string) (int, string) {
		path := "/owntracks/regions"
		if guest {
			path += "/guest"
		}
		return p.do("POST", path, body, bearerAPI)
	}
	if code, _ := p.do("POST", "/owntracks/regions", `{"name":"집","lat":37.5559,"lon":126.9104}`, func(*http.Request) {}); code != 401 {
		t.Fatalf("토큰 없이: %d", code)
	}
	for _, bad := range []string{`{"name":"","lat":37.5,"lon":127}`, `{"name":"집:1","lat":37.5,"lon":127}`, `{"name":"집|1|2","lat":37.5,"lon":127}`, `{"name":"+집","lat":37.5,"lon":127}`, `{"name":"집","lat":0,"lon":0}`, `{"name":"집","lat":91,"lon":127}`} {
		if code, _ := register(false, bad); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	const home = `"lat":37.5559,"lon":126.9104`
	p.say(false, fmt.Sprintf(`{"_type":"location",%s,"acc":10,"tst":%d,"m":1,"batt":80}`, home, p.at("12:00")))
	code, body := register(false, `{"name":"집","lat":37.5559,"lon":126.9104}`)
	if code != 200 || strings.Contains(body, "link") || !strings.Contains(body, `"radiusM":50`) {
		t.Fatalf("등록(폰 목록을 아직 모름): %d %s", code, body)
	}
	if open, _ := p.st.Open(context.Background()); len(open) != 1 || open[0].Region != "집" {
		t.Fatalf("있는 곳에 만든 영역은 조용히 체류로: %+v", open)
	}
	if _, pending := p.track("owner"); len(pending) != 1 || pending[0] != "집" {
		t.Fatalf("폰에 가는 중: %v", pending)
	}
	// Its list first - then the phone's own 집 moves here, by its id. (It
	// has the region that follows it: TestFollowRegion.)
	if r := p.say(false, fmt.Sprintf(`{"_type":"location",%s,"acc":10,"tst":%d,"m":1,"batt":80}`, home, p.at("12:05"))); !strings.Contains(r, `{"_type":"cmd","action":"waypoints"}`) || strings.Contains(r, "setWaypoints") {
		t.Fatalf("목록부터 물음: %s", r)
	}
	r := p.say(false, `{"_type":"waypoints","waypoints":[{"_type":"waypoint","rid":"old","desc":"집","lat":37.56,"lon":126.92,"rad":100,"tst":3},{"_type":"waypoint","rid":"follow","desc":"+follow","lat":37.5559,"lon":126.9104,"rad":50,"tst":4}]}`)
	var cmds []struct {
		Action    string
		Waypoints struct{ Waypoints []map[string]any }
	}
	if err := json.Unmarshal([]byte(r), &cmds); err != nil || len(cmds) != 1 || len(cmds[0].Waypoints.Waypoints) != 1 {
		t.Fatalf("폰의 집을 옮김: %s", r)
	}
	if wp := cmds[0].Waypoints.Waypoints[0]; wp["rid"] != "old" || wp["lat"] != 37.5559 || wp["rad"] != 50.0 || wp["tst"] != 3.0 {
		t.Fatalf("같은 id, 새 자리, 50m: %s", r)
	}
	if _, pending := p.track("owner"); len(pending) != 1 {
		t.Fatalf("폰 목록에 보일 때까지는 가는 중: %v", pending)
	}
	p.say(false, `{"_type":"waypoints","waypoints":[{"_type":"waypoint","rid":"old","desc":"집","lat":37.5559,"lon":126.9104,"rad":50,"tst":3}]}`)
	if _, pending := p.track("owner"); len(pending) != 0 {
		t.Fatalf("폰 목록에 있으면 끝: %v", pending)
	}
	// The list known: a link at once, and the region with the next reply.
	code, body = register(false, `{"name":"카페","lat":37.51,"lon":127.01}`)
	var reg struct{ Link string }
	_ = json.Unmarshal([]byte(body), &reg)
	u, err := url.Parse(reg.Link)
	if code != 200 || err != nil || u.Scheme != "owntracks" || u.Path != "/config" {
		t.Fatalf("등록(목록을 앎): %d %s", code, body)
	}
	raw, _ := base64.StdEncoding.DecodeString(u.Query().Get("inline"))
	var cfg struct {
		Type      string `json:"_type"`
		Waypoints []map[string]any
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.Type != "configuration" || len(cfg.Waypoints) != 1 || cfg.Waypoints[0]["desc"] != "카페" || cfg.Waypoints[0]["rad"] != 50.0 || cfg.Waypoints[0]["rid"] == "" {
		t.Fatalf("링크: %s", raw)
	}
	if open, _ := p.st.Open(context.Background()); len(open) != 1 {
		t.Fatalf("있지 않은 곳은 체류가 아님: %+v", open)
	}
	if r := p.say(false, fmt.Sprintf(`{"_type":"location",%s,"acc":10,"tst":%d,"m":1,"batt":80}`, home, p.at("12:20"))); !strings.Contains(r, `"desc":"카페"`) || !strings.Contains(r, `"action":"waypoints"`) {
		t.Fatalf("다음 답에 카페: %s", r)
	}

	// The guest's: its own, made where it is - no arrival in its inbox.
	const guestHome = `"lat":37.6,"lon":127.0`
	p.say(true, fmt.Sprintf(`{"_type":"location",%s,"acc":10,"tst":%d,"m":1,"batt":80}`, guestHome, p.at("12:00")))
	if code, body := register(true, `{"name":"집","lat":37.6,"lon":127.0}`); code != 200 || strings.Contains(body, "link") {
		t.Fatalf("상대 등록: %d %s", code, body)
	}
	p.say(true, fmt.Sprintf(`{"_type":"location",%s,"acc":10,"tst":%d,"m":1,"batt":80}`, guestHome, p.at("12:10")))
	p.say(true, fmt.Sprintf(`{"_type":"location","lat":37.61,"lon":127.0,"acc":10,"tst":%d,"m":1,"batt":80}`, p.at("12:30")))
	// Arrivals and departures only (the operator's state, context.state, is
	// no notification).
	var moves []string
	deadline := time.After(3 * time.Second)
	for len(moves) < 1 {
		select {
		case e := <-p.events:
			if typ, _ := e["type"].(string); strings.HasPrefix(typ, "location.") {
				moves = append(moves, typ)
			}
		case <-deadline:
			t.Fatal("나설 때는 알림")
		}
	}
	quiet := time.After(300 * time.Millisecond)
	for done := false; !done; {
		select {
		case e := <-p.events:
			if typ, _ := e["type"].(string); strings.HasPrefix(typ, "location.") {
				moves = append(moves, typ)
			}
		case <-quiet:
			done = true
		}
	}
	if len(moves) != 1 || moves[0] != "location.guest.leave" {
		t.Fatalf("등록한 곳에서 도착 알림이 나면 안 되고, 출발은 하나: %v", moves)
	}
	regions, _ := p.st.Regions(context.Background())
	if len(regions) != 2 {
		t.Fatalf("운영자 영역은 운영자 것만: %+v", regions)
	}
}
