package read

import (
	"context"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/phonenote-svc/internal/note"
)

type script string

func (s script) Complete(context.Context, string, string) (string, error) { return string(s), nil }
func (script) Ready() bool                                                { return true }

func TestRulesNeedNoModel(t *testing.T) {
	for _, c := range []struct {
		n    note.Note
		kind string
	}{
		{note.Note{Title: "(광고) 오늘만 50% 할인"}, note.Promotion},
		{note.Note{Title: "[광고] 쿠폰 도착"}, note.Promotion},
		{note.Note{Title: "토스", Body: "인증번호 [••••] 를 입력해 주세요"}, note.Security},
		{note.Note{}, note.Other},
	} {
		r, ok := Rule(c.n)
		if !ok || r.Kind != c.kind || r.Meaningful {
			t.Fatalf("%+v: %+v %v", c.n, r, ok)
		}
	}
	if _, ok := Rule(note.Note{Title: "배달의민족", Body: "교촌치킨에서 주문을 접수했어요"}); ok {
		t.Fatal("주문 알림은 모델이 읽어야 합니다")
	}
}

func TestQuietAfterEightNoises(t *testing.T) {
	noise := []string{"noise", "noise", "noise", "noise", "noise", "noise", "noise", "noise"}
	if !Quiet(noise) || Quiet(noise[:7]) || Quiet(append([]string{"deposit"}, noise...)) {
		t.Fatal("여덟 번 모두 의미 없을 때만 모델을 건너뜁니다")
	}
}

// The model's answer is checked: an unknown kind is other, a nameless item
// is dropped, a category not on the list is 기타.
func TestModelAnswerIsChecked(t *testing.T) {
	r := Reader{Model: script(`해석입니다: {"kind":"delivery_order","meaningful":true,"summary":"교촌치킨 주문 접수","merchant":"교촌치킨",
		"items":[{"name":"허니콤보","quantity":1,"priceKrw":23000,"category":"식비"},{"name":"","quantity":1},{"name":"콜라 1.25L","category":"음료"}],
		"amountKrw":25000,"stage":"accepted"}`)}
	got, err := r.Read(context.Background(), note.Note{App: "com.jawebs.baedal", Title: "주문 접수", Body: "교촌치킨 허니콤보 외 1개", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != note.DeliveryOrder || got.Merchant != "교촌치킨" || got.AmountKrw != 25000 || len(got.Items) != 2 {
		t.Fatalf("%+v", got)
	}
	if got.Items[1].Category != "기타" || got.Items[1].Quantity != 1 {
		t.Fatalf("목록에 없는 분류는 기타: %+v", got.Items[1])
	}
	odd, _ := Reader{Model: script(`{"kind":"lottery","meaningful":true}`)}.Read(context.Background(), note.Note{Title: "x"})
	if odd.Kind != note.Other {
		t.Fatalf("모르는 종류: %q", odd.Kind)
	}
}
