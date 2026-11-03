package jobs

import (
	"reflect"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/gocommon/pay"
)

// The argument's words are a literal: they still name every keypad payment by
// one of the table's names.
func TestThePinRequestNamesTheTablesPayments(t *testing.T) {
	f, _ := reflect.TypeOf(PinRequestInput{}).FieldByName("Pay")
	tag := f.Tag.Get("jsonschema")
	for _, p := range pay.Pins() {
		named := false
		for _, a := range p.Aliases {
			named = named || strings.Contains(tag, "'"+a+"'")
		}
		if !named {
			t.Errorf("설명 %q 에 %s 가 없습니다", tag, p.Name)
		}
	}
}
