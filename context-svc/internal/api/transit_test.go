package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/context-svc/internal/transit"
)

// fakeFeed is the live feeds as a test sets them.
type fakeFeed struct {
	arrivals map[string][]transit.Arrival // by stop id
	err      error
	asked    int
}

func (f *fakeFeed) Arrivals(_ context.Context, s transit.Stop) ([]transit.Arrival, error) {
	f.asked++
	if f.err != nil {
		return nil, f.err
	}
	return f.arrivals[s.ID], nil
}
func (f *fakeFeed) Lines(_ context.Context, s transit.Stop) ([]transit.Line, error) {
	return []transit.Line{{ID: "7770", Name: "7770", Toward: "강남역 방면"}}, nil
}
func (f *fakeFeed) Near(_ context.Context, lat, lon float64) ([]transit.Stop, error) {
	return []transit.Stop{{Kind: transit.Bus, Region: transit.Gyeonggi, ID: "200", Name: "성균관대역", Lat: lat + 0.001, Lon: lon}}, nil
}
func (f *fakeFeed) Search(_ context.Context, q string) ([]transit.Stop, error) {
	return []transit.Stop{{Kind: transit.Subway, ID: q, Name: q + "역"}}, nil
}
func (f *fakeFeed) Configured() (bool, bool) { return true, false }

// Each person keeps and sees only their own routes, each with when to leave
// now; a feed's failure is said on its route, not as a broken screen.
func TestTransitRoutes(t *testing.T) {
	p := newPhoneTest(t)
	feed := &fakeFeed{arrivals: map[string][]transit.Arrival{}}
	p.server.feed = feed
	now := p.today.Load().(time.Time) // 2026-10-23 20:00, a Friday
	feed.arrivals["200"] = []transit.Arrival{
		{Line: transit.Line{ID: "7770", Name: "7770"}, At: now.Add(25 * time.Minute), Vehicle: "v1"},
		{Line: transit.Line{ID: "7770", Name: "7770"}, At: now.Add(45 * time.Minute), Vehicle: "v2"},
	}
	route := `{"routes":[{"name":"퇴근","stop":{"kind":"bus","region":"gyeonggi","id":"200","name":"성균관대역"},"line":{"id":"7770","name":"7770"},"walk":10,"spare":1,"days":[5,1,5],"from":"19:30","to":"21:00","push":true}]}`
	if code, body := p.do("PUT", "/transit/routes", route, bearerAPI); code != 200 || !strings.Contains(body, `"days":[1,5]`) || !strings.Contains(body, `"id":"`) {
		t.Fatalf("운영자 경로: %d %s", code, body)
	}
	for _, bad := range []string{
		`{"routes":[{"name":"","stop":{"kind":"bus","region":"gyeonggi","id":"1"},"line":{"id":"1"}}]}`,
		`{"routes":[{"name":"x","stop":{"kind":"tram","id":"1"},"line":{"id":"1"}}]}`,
		`{"routes":[{"name":"x","stop":{"kind":"bus","region":"busan","id":"1"},"line":{"id":"1"}}]}`,
		`{"routes":[{"name":"x","stop":{"kind":"subway","id":"강남"},"line":{"id":""}}]}`,
		`{"routes":[{"name":"x","stop":{"kind":"subway","id":"강남"},"line":{"id":"1"},"from":"7:30","to":"09:00"}]}`,
		`{"routes":[{"name":"x","stop":{"kind":"subway","id":"강남"},"line":{"id":"1"},"days":[8]}]}`,
	} {
		if code, _ := p.do("PUT", "/transit/routes", bad, bearerAPI); code != 400 {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	code, body := p.do("GET", "/transit", "", bearerAPI)
	var got struct {
		Routes []transit.Route
		Plans  []transit.Plan
		Feeds  map[string]bool
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != 200 || len(got.Routes) != 1 || len(got.Plans) != 1 || !got.Feeds["buses"] || got.Feeds["subway"] {
		t.Fatalf("운영자의 출발: %d %s", code, body)
	}
	if pl := got.Plans[0]; pl.Stage != transit.Pack || pl.Catch == nil || pl.Catch.Vehicle != "v1" || !pl.Leave.Equal(now.Add(14*time.Minute)) {
		t.Fatalf("25분 뒤 버스, 걸음 10·여유 1: 14분 뒤 나감: %+v", pl)
	}
	// The guest has none, and the operator's are not theirs to see.
	if code, body := p.do("GET", "/transit/guest", "", bearerAPI); code != 200 || !strings.Contains(body, `"routes":[]`) {
		t.Fatalf("손님은 비어 있음: %d %s", code, body)
	}
	// A feed failing: said on the route.
	feed.err = &transit.FeedError{Msg: "공공데이터포털 키가 이 정보에 아직 등록되지 않았어요"}
	feed.arrivals = nil
	p.server.feed = feed
	if code, body := p.do("GET", "/transit", "", bearerAPI); code != 200 || !strings.Contains(body, `"stage":"off"`) || !strings.Contains(body, "등록되지 않았어요") {
		t.Fatalf("피드 실패: %d %s", code, body)
	}
	// Stops near where the phone last was, or by name.
	p.say(true, fmt.Sprintf(`{"_type":"location","lat":37.21,"lon":126.96,"acc":10,"tst":%d,"m":1}`, p.at("19:00")))
	if code, body := p.do("GET", "/transit/guest/stops?near=me", "", bearerAPI); code != 200 || !strings.Contains(body, "성균관대역") || !strings.Contains(body, `"dist":`) {
		t.Fatalf("손님 근처: %d %s", code, body)
	}
	if code, _ := p.do("GET", "/transit/stops?near=me", "", bearerAPI); code != 404 {
		t.Fatalf("운영자 폰 위치가 없으면: %d", code)
	}
	if code, body := p.do("GET", "/transit/stops?q=강남", "", bearerAPI); code != 200 || !strings.Contains(body, "강남역") {
		t.Fatalf("이름으로: %d %s", code, body)
	}
	if code, _ := p.do("GET", "/transit/stops?near=1,2", "", bearerAPI); code != 400 {
		t.Fatalf("한국 밖 좌표: %d", code)
	}
	if code, body := p.do("GET", "/transit/lines?kind=bus&region=gyeonggi&id=200", "", bearerAPI); code != 200 || !strings.Contains(body, "강남역 방면") {
		t.Fatalf("노선: %d %s", code, body)
	}
	if code, _ := p.do("GET", "/transit/lines?kind=bus&id=200", "", bearerAPI); code != 400 {
		t.Fatalf("지역 없는 버스: %d", code)
	}
}

// While a route's hours last, its person is told when to leave - once each:
// 슬슬 짐 싸야 해요 within a quarter of an hour, 지금 나가요 two minutes
// before. Outside its hours, or without push, nothing - and the feed is not
// asked.
func TestTransitNudge(t *testing.T) {
	p := newPhoneTest(t)
	feed := &fakeFeed{arrivals: map[string][]transit.Arrival{}}
	p.server.feed = feed
	base := p.today.Load().(time.Time) // Friday 20:00
	feed.arrivals["300"] = []transit.Arrival{{Line: transit.Line{ID: "2", Name: "2호선", Toward: "성수행 - 역삼방면"}, At: base.Add(20 * time.Minute), Vehicle: "2213"}}
	routes := `{"routes":[
		{"name":"퇴근","stop":{"kind":"subway","id":"300","name":"강남역"},"line":{"id":"2"},"walk":8,"spare":0,"days":[5],"from":"19:30","to":"21:00","push":true},
		{"name":"주말","stop":{"kind":"subway","id":"300","name":"강남역"},"line":{"id":"2"},"walk":8,"days":[6],"from":"19:30","to":"21:00","push":true},
		{"name":"알림 없음","stop":{"kind":"subway","id":"300","name":"강남역"},"line":{"id":"2"},"walk":8,"days":[5],"from":"19:30","to":"21:00","push":false}]}`
	if code, body := p.do("PUT", "/transit/guest/routes", routes, bearerAPI); code != 200 {
		t.Fatalf("경로: %d %s", code, body)
	}
	tick := func(after time.Duration) {
		p.today.Store(base.Add(after))
		p.server.nudge(context.Background())
	}
	tick(0)           // leave in 12: pack
	tick(time.Minute) // again: said already
	tick(10 * time.Minute)
	tick(11 * time.Minute) // leave in 1: go
	tick(12 * time.Minute)
	var said []string
	deadline := time.After(3 * time.Second)
	for len(said) < 2 {
		select {
		case e := <-p.events:
			if typ, _ := e["type"].(string); strings.HasPrefix(typ, "transit.") {
				n, _ := e["notify"].(map[string]any)
				said = append(said, fmt.Sprintf("%s|%s|%s|%s|%s", typ, n["title"], n["body"], n["audience"], n["tier"]))
			}
		case <-deadline:
			t.Fatalf("알림이 모자람: %q", said)
		}
	}
	want := []string{
		"transit.pack|슬슬 짐 싸야 해요|20:20 2호선(성수행) 타기 · 강남역까지 도보 8분 · 12분 뒤 나가요|guest|now",
		"transit.go|지금 나가요|20:20 2호선(성수행) 타기 · 강남역까지 도보 8분|guest|now",
	}
	if strings.Join(said, "\n") != strings.Join(want, "\n") {
		t.Fatalf("알림:\n%s", strings.Join(said, "\n"))
	}
	select {
	case e := <-p.events:
		if typ, _ := e["type"].(string); strings.HasPrefix(typ, "transit.") {
			t.Fatalf("한 번씩만: %v", e)
		}
	case <-time.After(300 * time.Millisecond):
	}
	// Outside the hours: not even asked.
	asked := feed.asked
	p.today.Store(base.Add(2 * time.Hour))
	p.server.nudge(context.Background())
	if feed.asked != asked {
		t.Fatalf("시간대 밖에는 피드를 묻지 않음: %d → %d", asked, feed.asked)
	}
	if code, _ := p.do("GET", "/transit/guest", "", func(*http.Request) {}); code != 401 {
		t.Fatalf("토큰 없이: %d", code)
	}
}
