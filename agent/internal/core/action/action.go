// Package action defines what a module can be asked to do, and how that ask
// is shown to a person before it runs.
//
// Everything downstream keys off Kind: the approval card, the confidence
// gate, and the module registry all read the same string. A module declares
// its actions once and those three places are fed from that one declaration.
package action

import (
	"encoding/json"
	"strings"
)

// Action is one concrete request: a kind plus its arguments, still unparsed.
// The raw JSON survives from the tool call to the executor untouched, so the
// operator approves the same bytes that eventually run.
type Action struct {
	Kind  string          `json:"kind"`  // "calendar.create_event"
	Input json.RawMessage `json:"input"` // 도구가 받은 인자 원본
}

// Module returns the registry key: the part before the first dot.
func (a Action) Module() string {
	name, _, _ := strings.Cut(a.Kind, ".")
	return name
}

// Card is what a person actually judges. Body is the whole point: approving a
// change while seeing only its title is approving blind, so a card that edits
// or removes something renders both sides of the change.
type Card struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	Consequence string `json:"consequence"`
}

// Spec is a module's declaration of one action it accepts.
type Spec struct {
	Kind    string
	Summary string // 모델이 읽는 한 줄. MCP 도구 설명으로 그대로 나간다.
	// Reversible decides whether this action may ever run without a card.
	// The confidence gate can raise its own bar but never lower this one:
	// an irreversible action is a card no matter how sure the model is.
	Reversible bool
}

// Result is what the operator is told after an action ran.
type Result struct {
	Note string `json:"note"`
}
