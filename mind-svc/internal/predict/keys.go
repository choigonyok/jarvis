package predict

import (
	"strings"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/spending/category"
)

// Keys are the names an occurrence happens under, from the most general to
// the most particular - what a rule waits for. A card payment at 배민 is
// "purchase" (a payment), "purchase/식비" (one of its class),
// "purchase/식비@주식회사 우아한형제" (that merchant) and "delivery" (food
// delivered, whatever the app). A source that adds a new kind of occurrence
// takes part with no code here: its kind, class and title are keys like any
// other. thing is an item's class by common sense, "" when not judged yet.
func Keys(o model.Occurrence, thing func(name string) string) []string {
	kind := strings.TrimSpace(o.Kind)
	class, title := clean(o.Class), clean(o.Title)
	switch kind {
	case "", model.Job:
		return nil // the assistant's own errands, not the operator's life
	case model.Asset:
		if class == "" || class == "보유" {
			return nil // a holding is a state, not something that happened
		}
	case model.PurchaseItem:
		if thing != nil {
			if c := clean(thing(o.Title)); c != "" {
				class = c // what it is, over the shop's category
			}
		}
		title = "" // an item's own name rarely comes round; its class does
	case model.Photo:
		title = ""
	case model.Workout:
		if title == "운동" {
			title = ""
		}
	case model.Transfer:
		title = ""
		if len(o.People) > 0 {
			title = clean(o.People[0])
		}
	}
	keys := []string{kind}
	if class != "" {
		keys = append(keys, kind+"/"+class)
	}
	if title != "" && len([]rune(title)) <= 30 {
		if class != "" {
			keys = append(keys, kind+"/"+class+"@"+title)
		} else {
			keys = append(keys, kind+"@"+title)
		}
	}
	if kind == model.Purchase && IsDelivery(o.Title) {
		keys = append(keys, Delivery)
	}
	return keys
}

// Delivery is food delivered, read from a delivery app's card payment.
const Delivery = "delivery"

// IsDelivery is whether a merchant is a food delivery app (category.Delivery).
func IsDelivery(merchant string) bool { return category.IsDelivery(merchant) }

// Parts is a key taken apart: its kind, class and title ("" when absent).
func Parts(key string) (kind, class, title string) {
	rest := key
	if i := strings.Index(rest, "@"); i >= 0 {
		rest, title = rest[:i], rest[i+1:]
	}
	kind = rest
	if i := strings.Index(rest, "/"); i >= 0 {
		kind, class = rest[:i], rest[i+1:]
	}
	return kind, class, title
}

// Source is the kind of occurrence a key is read from - what says since
// when it could have been seen at all. Delivery is seen in the payments.
func Source(key string) string {
	if key == Delivery {
		return model.Purchase
	}
	kind, _, _ := Parts(key)
	return kind
}

// Targetable is whether a key is something to expect. The calendar's
// entries are known ahead - they are what other things follow, never a
// thing to predict.
func Targetable(key string) bool {
	switch Source(key) {
	case model.Calendar, model.Job, "":
		return false
	}
	return true
}

// Label is a key in words: "배달 주문", "식비 결제", "우아한형제 결제",
// "회사 도착", "운동".
func Label(key string) string {
	if key == Delivery {
		return "배달 주문"
	}
	kind, class, title := Parts(key)
	name := shortName(title)
	switch kind {
	case model.Purchase:
		switch {
		case name != "":
			return name + " 결제"
		case class != "":
			return class + " 결제"
		}
		return "카드 결제"
	case model.PurchaseItem:
		if class != "" {
			return class + " 구매"
		}
		return "상품 구매"
	case model.Visit:
		if name != "" {
			return name + " 도착"
		}
		if place := map[string]string{"home": "집", "work": "회사", "camp": "캠핑장", "shop": "마트", "food": "식당"}[class]; place != "" {
			return place + " 도착"
		}
		return "자주 가는 곳 도착"
	case model.Workout:
		if name != "" {
			return name + " 운동"
		}
		return "운동"
	case model.Photo:
		return "사진"
	case model.Transfer:
		if name != "" {
			return name + "님 송금"
		}
		return "송금 받음"
	case model.Asset:
		if name != "" {
			return strings.TrimSpace(name + " " + class)
		}
		return "자산 " + class
	case model.Calendar:
		if name != "" {
			return name
		}
		if class != "" {
			return class + " 일정"
		}
		return "일정"
	}
	for _, s := range []string{name, class} {
		if s != "" {
			return s
		}
	}
	return kind
}

// shortName drops a company's legal form from a merchant's name: "주식회사
// 우아한형제" is said "우아한형제".
func shortName(s string) string {
	for _, w := range []string{"주식회사", "(주)", "㈜"} {
		s = strings.ReplaceAll(s, w, "")
	}
	return strings.TrimSpace(s)
}

func clean(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
