package gate

import (
	"testing"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/skill"
)

// A reminder's notice opens its day on the console's calendar, its entry
// marked; one with nowhere to point, and mind's own page, keep the old link.
func TestNoticeGoesWhereItIsAbout(t *testing.T) {
	console := &Gate{url: "/"}
	link := skill.CalendarLink(model.State{Date: "2026-10-20", CalendarID: "ev-1"})
	if got := console.link(model.Intent{ID: "i1", Link: link}); got != "/calendar?date=2026-10-20&focus=event%3Aev-1" {
		t.Fatalf("일정 알림: %q", got)
	}
	if got := console.link(model.Intent{ID: "i2"}); got != "/#i2" {
		t.Fatalf("갈 곳이 없는 알림: %q", got)
	}
	standalone := &Gate{url: "http://localhost:8102/"}
	if got := standalone.link(model.Intent{ID: "i3", Link: link}); got != "http://localhost:8102/#i3" {
		t.Fatalf("mind 단독 화면: %q", got)
	}
	if got := skill.CalendarLink(model.State{Date: "2026-10-21"}); got != "/calendar?date=2026-10-21&focus=day" {
		t.Fatalf("아직 캘린더에 없는 일정: %q", got)
	}
}
