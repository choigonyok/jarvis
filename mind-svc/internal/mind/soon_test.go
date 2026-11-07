package mind

import (
	"testing"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// The life ledgers read a wish off the ledger whatever memory made of it: a
// fact held back from memory still counts, and only one a person dropped does
// not.
func TestHeldFactsStayInTheLifeLedgers(t *testing.T) {
	chair := model.Fact{Relation: "WANTS", Object: model.Party{Name: "캠핑 의자"}, Text: "캠핑 의자를 사고 싶다"}
	for status, want := range map[string]bool{
		model.FactPending: true, model.FactStored: true, model.FactHeld: true, model.FactDropped: false,
	} {
		f := chair
		f.Status = status
		if wish(f) != want {
			t.Errorf("%s: 생활 장부에 %v 이어야 합니다", status, want)
		}
	}
}
