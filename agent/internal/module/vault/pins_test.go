package vault

import (
	"reflect"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/gocommon/pay"
)

// A tool argument's own words cannot be built from the table (a struct tag is a
// literal): they still name every keypad payment by one of its names, and the
// names the model gives are the table's.
func TestTheToolsNameTheTablesPayments(t *testing.T) {
	for _, v := range []any{PinInput{}} {
		f, _ := reflect.TypeOf(v).FieldByName("Pay")
		tag := f.Tag.Get("jsonschema")
		for _, p := range pay.Pins() {
			named := false
			for _, a := range p.Aliases {
				named = named || strings.Contains(tag, "'"+a+"'")
			}
			if !named {
				t.Errorf("%T 의 설명 %q 에 %s 가 없습니다", v, tag, p.Name)
			}
		}
	}
	for said, host := range map[string]string{"네이버페이": "pay.naver.com", "쿠페이": "coupang.com"} {
		raw := []byte(`{"pay":"` + said + `"}`)
		if got, _, err := payOf(raw); err != nil || got != host {
			t.Fatalf("%s: %s %v", said, got, err)
		}
	}
	if _, _, err := payOf([]byte(`{"pay":"토스페이"}`)); err == nil || !strings.Contains(err.Error(), pay.Names()) {
		t.Fatalf("%v", err)
	}
}
