package holiday

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// feed is the shape of Google's Korean holiday calendar: folded lines, a
// description that says 공휴일 or 기념일, one all-day event per day.
const feed = "BEGIN:VCALENDAR\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20280126\r\nDTEND;VALUE=DATE:20280127\r\nDESCRIPTION:공휴일\r\nSUMMARY:설날\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20280405\r\nDTEND;VALUE=DATE:20280406\r\nDESCRIPTION:기념일\\n기념일을 숨기려면 Google Calendar 설정 >\r\n  대한민국의 휴일 캘린더로 이동하세요.\r\nSUMMARY:식목일\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20281003\r\nDTEND;VALUE=DATE:20281004\r\nDESCRIPTION:공휴일\r\nSUMMARY:개천절\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20281003\r\nDTEND;VALUE=DATE:20281004\r\nDESCRIPTION:공휴일\r\nSUMMARY:추석\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20270209\r\nDTEND;VALUE=DATE:20270210\r\nDESCRIPTION:공휴일\r\nSUMMARY:쉬는 날 설날\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20290101\r\nDTEND;VALUE=DATE:20290102\r\nDESCRIPTION:공휴일\r\nSUMMARY:새해\r\n 첫날\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParseKeepsPublicHolidays(t *testing.T) {
	got, err := Parse(strings.NewReader(feed))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"2028-01-26": "설날",
		"2028-10-03": "개천절·추석",
		"2027-02-09": "대체공휴일",
		"2029-01-01": "신정", // folded "새해" + "첫날"
	}
	if len(got) != len(want) {
		t.Fatalf("공휴일 %d개를 기대했는데 %v", len(want), got)
	}
	for d, n := range want {
		if got[d] != n {
			t.Errorf("%s: %q, want %q", d, got[d], n)
		}
	}
	if _, ok := got["2028-04-05"]; ok {
		t.Error("기념일(식목일)은 공휴일이 아닙니다")
	}
}

func TestBetweenJoinsTheFeedAndTheKeptList(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, feed)
	}))
	defer srv.Close()
	s := New(srv.URL, slog.New(slog.DiscardHandler))

	got, err := s.Between(context.Background(), "2027-02-01", "2028-12-31")
	if err != nil {
		t.Fatal(err)
	}
	// The feed's day and the kept list's day the feed leaves out.
	if got["2028-01-26"] != "설날" || got["2027-02-06"] != "설날 연휴" || got["2027-05-01"] != "노동절" {
		t.Fatalf("피드와 목록이 합쳐지지 않았습니다: %v", got)
	}
	if _, ok := got["2026-12-25"]; ok {
		t.Fatal("범위 밖 날이 들어왔습니다")
	}
	// Read once a day, not on every question.
	if _, err := s.Between(context.Background(), "2028-01-01", "2028-01-31"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("피드를 %d번 읽었습니다", hits.Load())
	}
}

func TestBetweenWithoutTheFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	s := New(srv.URL, slog.New(slog.DiscardHandler))
	got, err := s.Between(context.Background(), "2026-05-01", "2026-05-31")
	if err != nil {
		t.Fatal(err)
	}
	if got["2026-05-01"] != "노동절" || got["2026-05-05"] != "어린이날" {
		t.Fatalf("피드가 없으면 목록으로: %v", got)
	}
	if _, err := s.Between(context.Background(), "2026-5-1", "2026-05-31"); err == nil {
		t.Fatal("날짜 형식이 틀리면 거절해야 합니다")
	}
}
