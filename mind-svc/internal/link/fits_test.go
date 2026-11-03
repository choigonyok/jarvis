package link

import (
	"testing"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// What was bought fits a plan by the plan's kind: a meal at a dinner, food
// delivered to a date at home - 쿠팡이츠 too, which is a delivery app and not
// the online store its name holds.
func TestWhatWasBoughtFitsThePlan(t *testing.T) {
	buy := func(title, class string) model.Occurrence {
		return model.Occurrence{Kind: "purchase", Title: title, Class: class}
	}
	home := model.State{Type: "데이트", PlaceKind: "집"}
	if !fits(buy("쿠팡이츠", "식비"), home) || !fits(buy("배달의민족", "식비"), model.State{Type: "데이트"}) {
		t.Fatal("집 데이트에 배달 음식")
	}
	if fits(buy("쿠팡", "쇼핑"), home) || fits(buy("합정식당", "식비"), model.State{Type: "데이트", PlaceKind: "식당"}) {
		t.Fatal("쇼핑은 음식이 아니고, 집 밖 데이트의 식당 결제는 장소로 잇습니다")
	}
	if !fits(buy("합정식당", "식비"), model.State{Type: "식사"}) || fits(buy("CGV", "문화·여가"), model.State{Type: "식사"}) {
		t.Fatal("식사에는 음식")
	}
}
