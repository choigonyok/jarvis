// Package slot is what the assistant has to fill in to do something for the
// operator, and how long what it knows stays good. A shipping address once
// given holds until a move; what a merchant is holds until someone says
// otherwise. Keeping that here means the question is asked once, and asked
// again only when there is a reason - not every order, and not never.
//
// The definitions are rows of the registry.
package slot

import (
	"fmt"
	"slices"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/engine"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/registry"
)

// Life is how long a slot's value stays good.
type Life string

const (
	UntilEvent Life = "until-event" // until something happens: a move
	Standing   Life = "standing"    // until someone says otherwise
)

// Def is one slot.
type Def struct {
	Name string
	// Kind is the action it fills, and Context what its answers are kept
	// under on the ledger.
	Kind    string
	Skill   string
	Context string
	// Evidence is the facts that can end a value.
	Evidence []string
	Life     Life
	// Secret: the value is sealed elsewhere and never passes through here.
	Secret bool
	Spec   engine.Spec
}

// The slots the code asks for by name.
const (
	Shipping         = "shipping.address"
	MerchantCategory = "merchant.category"
)

// Get finds a slot by name in the registry.
func Get(r *registry.Snapshot, name string) (Def, bool) {
	v, ok := r.Slot(name)
	if !ok {
		return Def{}, false
	}
	return Def{Name: name, Kind: v.Kind, Skill: v.Skill, Context: v.Context, Evidence: v.Evidence, Life: Life(v.Life), Secret: v.Secret,
		Spec: engine.Spec{Reversible: v.Reversible, Cap: engine.Grade(v.Cap)}}, true
}

// Key is the slot's place in the engine's book.
func (d Def) Key() engine.Key {
	return engine.Key{Source: "card", Kind: d.Kind, Skill: d.Skill, Context: d.Context}
}

// Holds says whether a value saved at saved is still good, and if not, why.
// Fact rows are the operator's, with the time each held. What was true when
// the value was saved and has since ended is the event: the address was given
// at the old home. A first fact arriving later ends nothing, and neither does
// one that ended before the value was saved.
func (d Def) Holds(rows []model.FactRow, saved time.Time) (bool, string) {
	for _, r := range rows {
		if r.Subject != "self" || !slices.Contains(d.Evidence, r.Predicate) || r.To == nil {
			continue
		}
		if r.To.After(saved) {
			return false, fmt.Sprintf("저장한 뒤 %s에 사는 곳이 바뀐 기록이 있어요", r.To.Local().Format("1월 2일"))
		}
	}
	return true, ""
}

// heldP is how sure a value still is with nothing against it, and movedP with
// the event it ends on: the word for it may have been loose.
const (
	heldP  = 0.95
	movedP = 0.3
)

// Reading is a slot as things stand.
type Reading struct {
	Name  string       `json:"name"`
	Holds bool         `json:"holds"`
	Why   string       `json:"why,omitempty"`
	P     float64      `json:"p"`
	Grade engine.Grade `json:"grade"`
}

// Read weighs a value saved at saved against the facts and the record.
func (d Def) Read(rows []model.FactRow, saved time.Time, book *engine.Book) Reading {
	holds, why := d.Holds(rows, saved)
	stated := heldP
	if !holds {
		stated = movedP
	}
	r := book.Read(d.Key(), stated)
	return Reading{Name: d.Name, Holds: holds, Why: why, P: r.P, Grade: engine.Decide(d.Spec, r, false)}
}
