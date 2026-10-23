package transit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

func at(hm string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-26 "+hm, seoul) // a Monday
	return t
}

// The vehicle to make is the first one can still reach - leaving for the walk
// and the minutes to spare, or up to two minutes late, hurrying - and what is
// said follows how long until one must leave.
func TestMake(t *testing.T) {
	route := Route{ID: "r", Line: Line{ID: "7770"}, Walk: 10, Spare: 1}
	bus := func(hm string) Arrival { return Arrival{Line: Line{ID: "7770"}, At: at(hm)} }
	other := Arrival{Line: Line{ID: "3007"}, At: at("18:05")}
	for _, c := range []struct {
		now   string
		catch string
		stage string
		say   string
	}{
		{"17:30", "18:09", Relax, "여유 있어요"},
		{"17:46", "18:09", Pack, "슬슬 짐 싸야 해요"},
		{"17:52", "18:09", Soon, "곧 나가야 해요"},
		{"17:57", "18:09", Go, "지금 나가요!"},
		{"17:59", "18:09", Run, "뛰면 탈 수 있어요"},  // a minute late: hurry
		{"18:01", "18:30", Pack, "슬슬 짐 싸야 해요"}, // three late: the next one, 18 minutes off
	} {
		p := Make(route, []Arrival{bus("18:30"), other, bus("18:09")}, at(c.now))
		if p.Catch == nil || !p.Catch.At.Equal(at(c.catch)) || p.Stage != c.stage || p.Say != c.say {
			t.Errorf("%s: %+v", c.now, p)
		}
		if !p.Leave.Equal(p.Catch.At.Add(-11 * time.Minute)) {
			t.Errorf("%s: 걸음 10분과 여유 1분 앞에 나서야 함: %v", c.now, p.Leave)
		}
	}
	if p := Make(route, []Arrival{other}, at("17:00")); p.Catch != nil || p.Stage != None {
		t.Errorf("다른 노선만: %+v", p)
	}
	if p := Make(route, []Arrival{bus("18:09"), bus("18:30")}, at("17:30")); len(p.Next) != 1 || !p.Next[0].At.Equal(at("18:30")) {
		t.Errorf("다음 차: %+v", p.Next)
	}
}

// A route is gone on its days, inside its hours - past midnight too.
func TestActive(t *testing.T) {
	weekdays := Route{Days: []int{1, 2, 3, 4, 5}, From: "17:30", To: "19:00"}
	for _, c := range []struct {
		r    Route
		when time.Time
		want bool
	}{
		{weekdays, at("18:00"), true},
		{weekdays, at("19:00"), false},
		{weekdays, at("17:29"), false},
		{weekdays, at("18:00").AddDate(0, 0, 5), false}, // Saturday
		{Route{From: "23:30", To: "00:30", Days: []int{1}}, at("23:45"), true},
		{Route{From: "23:30", To: "00:30", Days: []int{1}}, at("00:10").AddDate(0, 0, 1), true}, // Tuesday 00:10: Monday's window
		{Route{From: "23:30", To: "00:30", Days: []int{1}}, at("00:10"), false},                 // Monday 00:10: Sunday's
		{Route{}, at("12:00"), false},
	} {
		if got := c.r.Active(c.when, seoul); got != c.want {
			t.Errorf("%+v at %v: %v", c.r, c.when, got)
		}
	}
	if Walk(400) != 8 || Walk(10) != 1 {
		t.Errorf("걸음: %d %d", Walk(400), Walk(10))
	}
}

func feeds(t *testing.T, handler http.HandlerFunc) *Feeds {
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	f := New("key%2Bwith%2Fslash%3D%3D", "subway-key")
	f.GyeonggiArrivals, f.GyeonggiStations, f.SeoulStations, f.SubwayBase = srv.URL+"/ga", srv.URL+"/gs", srv.URL+"/seoul", srv.URL+"/subway"
	f.Overpass = []string{srv.URL + "/overpass"}
	f.Now = func() time.Time { return at("18:00") }
	return f
}

// 경기's buses: seconds until each of the two coming, the list as a list or as
// one object; no result is nothing, and a key not yet registered says so. The
// key, given escaped, goes out escaped once.
func TestGyeonggi(t *testing.T) {
	var gotKey string
	f := feeds(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("serviceKey")
		switch r.URL.Query().Get("stationId") {
		case "200000078":
			w.Write([]byte(`{"response":{"comMsgHeader":{},"msgHeader":{"queryTime":"2026-10-26 18:00:00.0","resultCode":0,"resultMessage":"정상적으로 처리되었습니다."},
				"msgBody":{"busArrivalList":[{"flag":"PASS","locationNo1":3,"locationNo2":9,"plateNo1":"경기70사1234","plateNo2":"경기70사5678",
				"predictTime1":4,"predictTime2":17,"predictTimeSec1":250,"predictTimeSec2":1010,"remainSeatCnt1":12,"remainSeatCnt2":-1,
				"routeDestName":"강남역","routeId":234000026,"routeName":"7770","stationId":200000078,"vehId1":1,"vehId2":2},
				{"flag":"STOP","routeId":1,"routeName":"끝난노선","predictTimeSec1":60}]}}}`))
		case "single":
			w.Write([]byte(`{"response":{"msgHeader":{"resultCode":0},"msgBody":{"busArrivalList":{"routeId":5,"routeName":"5","predictTime1":3,"flag":"RUN"}}}}`))
		case "none":
			w.Write([]byte(`{"response":{"msgHeader":{"resultCode":4,"resultMessage":"결과가 존재하지 않습니다."}}}`))
		default:
			w.Write([]byte(`<OpenAPI_ServiceResponse><cmmMsgHeader><returnAuthMsg>SERVICE_KEY_IS_NOT_REGISTERED_ERROR</returnAuthMsg></cmmMsgHeader></OpenAPI_ServiceResponse>`))
		}
	})
	ctx := context.Background()
	arr, err := f.Arrivals(ctx, Stop{Kind: Bus, Region: Gyeonggi, ID: "200000078"})
	if err != nil || len(arr) != 2 || arr[0].Line.Name != "7770" || arr[0].Line.Toward != "강남역 방면" ||
		!arr[0].At.Equal(at("18:00").Add(250*time.Second)) || arr[0].Note != "3번째 전 · 빈자리 12" || arr[1].Note != "9번째 전" || arr[0].Vehicle != "1" {
		t.Fatalf("도착: %+v %v", arr, err)
	}
	if gotKey != "key+with/slash==" {
		t.Fatalf("키는 한 번만 이스케이프: %q", gotKey)
	}
	if arr, err := f.Arrivals(ctx, Stop{Kind: Bus, Region: Gyeonggi, ID: "single"}); err != nil || len(arr) != 1 || !arr[0].At.Equal(at("18:03")) {
		t.Fatalf("하나만 온 목록(분 단위만): %+v %v", arr, err)
	}
	if arr, err := f.Arrivals(ctx, Stop{Kind: Bus, Region: Gyeonggi, ID: "none"}); err != nil || len(arr) != 0 {
		t.Fatalf("결과 없음: %+v %v", arr, err)
	}
	if _, err := f.Arrivals(ctx, Stop{Kind: Bus, Region: Gyeonggi, ID: "x"}); err == nil || !strings.Contains(err.Error(), "등록되지 않았어요") {
		t.Fatalf("키 미등록: %v", err)
	}
	nokey := New("", "")
	if _, err := nokey.Arrivals(ctx, Stop{Kind: Bus, Region: Gyeonggi, ID: "1"}); err != ErrNoKey {
		t.Fatalf("키 없음: %v", err)
	}
}

// 서울's buses: the routes at a stop and the time in each message, from JSON -
// or the XML it answers by default.
func TestSeoulBus(t *testing.T) {
	xmlAnswer := false
	f := feeds(t, func(w http.ResponseWriter, r *http.Request) {
		if xmlAnswer {
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><ServiceResult><comMsgHeader/><msgHeader><headerCd>0</headerCd><headerMsg>정상적으로 처리되었습니다.</headerMsg></msgHeader>
				<msgBody><itemList><busRouteId>100100118</busRouteId><rtNm>402</rtNm><adirection>광화문</adirection><arrmsg1>곧 도착</arrmsg1><arrmsg2>12분3초후[6번째 전]</arrmsg2><vehId1>9</vehId1></itemList></msgBody></ServiceResult>`))
			return
		}
		w.Write([]byte(`{"comMsgHeader":{},"msgHeader":{"headerCd":"0","headerMsg":"정상적으로 처리되었습니다.","itemCount":0},"msgBody":{"itemList":[
			{"busRouteId":"100100118","busRouteAbrv":"402","rtNm":"402","adirection":"광화문","arrmsg1":"5분12초후[3번째 전]","arrmsg2":"출발대기","vehId1":"111","isLast1":"0"},
			{"busRouteId":"100100500","busRouteAbrv":"740","rtNm":"740","adirection":"덕은동","arrmsg1":"운행종료","arrmsg2":"운행종료"}]}}`))
	})
	ctx := context.Background()
	stop := Stop{Kind: Bus, Region: Seoul, ID: "23285"}
	arr, err := f.Arrivals(ctx, stop)
	if err != nil || len(arr) != 1 || !arr[0].At.Equal(at("18:05").Add(12*time.Second)) || arr[0].Note != "3번째 전" || arr[0].Line.Toward != "광화문 방면" {
		t.Fatalf("도착: %+v %v", arr, err)
	}
	lines, err := f.Lines(ctx, stop)
	if err != nil || len(lines) != 2 || lines[0].Name != "402" || lines[1].Name != "740" {
		t.Fatalf("노선(운행 끝난 것도): %+v %v", lines, err)
	}
	xmlAnswer = true
	arr, err = f.Arrivals(ctx, Stop{Kind: Bus, Region: Seoul, ID: "other"})
	if err != nil || len(arr) != 2 || !arr[0].At.Equal(at("18:00").Add(30*time.Second)) || !arr[1].At.Equal(at("18:12").Add(3*time.Second)) {
		t.Fatalf("XML 답: %+v %v", arr, err)
	}
}

// 서울's subway: the seconds the feed gives, from when it was taken; else a
// guess from the message; a train that left is gone; no data is nothing.
func TestSubway(t *testing.T) {
	f := feeds(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/subway-key/json/realtimeStationArrival/0/40/강남"):
			w.Write([]byte(`{"errorMessage":{"status":200,"code":"INFO-000","message":"정상 처리되었습니다.","total":4},"realtimeArrivalList":[
				{"subwayId":"1002","updnLine":"외선","trainLineNm":"성수행 - 역삼방면","statnNm":"강남","barvlDt":"120","recptnDt":"2026-10-26 17:59:30","arvlMsg2":"2분 후 (교대)","arvlCd":"99","btrainNo":"2213","btrainSttus":"일반","lstcarAt":"0"},
				{"subwayId":"1002","updnLine":"내선","trainLineNm":"신도림행 - 교대방면","barvlDt":"0","recptnDt":"2026-10-26 17:59:50","arvlMsg2":"[2]번째 전역 (선릉)","arvlCd":"99","btrainNo":"2190"},
				{"subwayId":"1077","updnLine":"하행","trainLineNm":"광교행 - 양재방면","barvlDt":"0","recptnDt":"2026-10-26 17:59:50","arvlMsg2":"강남 출발","arvlCd":"2","btrainNo":"7001"},
				{"subwayId":"1077","updnLine":"하행","trainLineNm":"광교행 - 양재방면","barvlDt":"0","recptnDt":"2026-10-26 17:59:50","arvlMsg2":"전역 도착","arvlCd":"5","btrainNo":"7003","btrainSttus":"급행","lstcarAt":"1"}]}`))
		default:
			w.Write([]byte(`{"status":500,"code":"INFO-200","message":"해당하는 데이터가 없습니다.","total":0}`))
		}
	})
	ctx := context.Background()
	arr, err := f.Arrivals(ctx, Stop{Kind: Subway, ID: "강남"})
	if err != nil || len(arr) != 3 {
		t.Fatalf("도착(떠난 열차 빼고 셋): %+v %v", arr, err)
	}
	if a := arr[0]; a.Line.ID != "1002|외선" || a.Line.Name != "2호선" || !a.At.Equal(at("17:59").Add(150*time.Second)) || a.Rough {
		t.Fatalf("받은 시각 + 초: %+v", a)
	}
	if a := arr[1]; !a.Rough || !a.At.Equal(at("17:59").Add(50*time.Second+240*time.Second)) {
		t.Fatalf("두 역 전은 짐작: %+v", a)
	}
	if a := arr[2]; a.Line.Name != "신분당선" || a.Note != "급행 막차" || !a.At.Equal(at("17:59").Add(50*time.Second+120*time.Second)) {
		t.Fatalf("전역 도착: %+v", a)
	}
	lines, err := f.Lines(ctx, Stop{Kind: Subway, ID: "강남"})
	if err != nil || len(lines) != 3 {
		t.Fatalf("노선·방향: %+v %v", lines, err)
	}
	if _, err := f.Lines(ctx, Stop{Kind: Subway, ID: "수원"}); err == nil || !strings.Contains(err.Error(), "서울 밖") {
		t.Fatalf("서울 밖 역: %v", err)
	}
	stops, err := f.Search(ctx, "강남역")
	if err != nil || len(stops) == 0 || stops[0].Kind != Subway || stops[0].ID != "강남" || stops[0].Name != "강남역" {
		t.Fatalf("역 이름으로 찾기: %+v %v", stops, err)
	}
	if stationName("서울역") != "서울" || stationName("총신대입구(이수)") != "총신대입구" || stationName("역삼") != "역삼" {
		t.Fatal("역 이름")
	}
}

// Stops near a point: both bus feeds and OpenStreetMap's stations, nearest
// first; one feed failing leaves the others.
func TestNear(t *testing.T) {
	f := feeds(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/gs/getBusStationAroundListv2"):
			w.Write([]byte(`{"response":{"msgHeader":{"resultCode":0},"msgBody":{"busStationAroundList":[
				{"stationId":200000078,"stationName":"강남역","mobileNo":22009,"x":127.0281,"y":37.4985,"distance":70,"regionName":"서울"}]}}}`))
		case strings.HasPrefix(r.URL.Path, "/seoul/getStationByPos"):
			w.WriteHeader(http.StatusInternalServerError)
		case r.URL.Path == "/overpass":
			w.Write([]byte(`{"elements":[{"lat":37.4979,"lon":127.0276,"tags":{"name":"강남","railway":"station","subway":"yes"}},
				{"lat":37.5006,"lon":127.0364,"tags":{"name":"역삼","railway":"station","station":"subway"}},
				{"lat":37.4979,"lon":127.0277,"tags":{"name":"강남","railway":"station","station":"train"}}]}`))
		}
	})
	stops, err := f.Near(context.Background(), 37.4979, 127.0276)
	if err != nil || len(stops) != 3 || stops[0].ID != "강남" || stops[0].Kind != Subway || stops[1].Region != Gyeonggi || stops[1].No != "22009" || stops[2].ID != "역삼" {
		t.Fatalf("근처: %+v %v", stops, err)
	}
}
