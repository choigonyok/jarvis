// Package module is the plug: what a feature must implement to be added to
// or removed from this assistant.
//
// There is deliberately no single fat Plugin interface. A calendar changes
// the world and answers questions about it; a KakaoTalk reader only watches
// one. Forcing both through one interface would give each of them empty
// methods, and an interface that is half no-ops stops being a boundary.
// Instead: three small interfaces, and the registry sorts by what a module
// actually implements.
package module

import (
	"context"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
)

// Module is the common denominator: a name and a lifecycle. Nothing else is
// shared by every feature.
type Module interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Source watches the world and emits events. KakaoTalk, mail, a calendar
// alarm. Not used by the calendar module - defined now so the first Source
// needs no change here.
type Source interface {
	Module
	Emit(ctx context.Context, out chan<- Event) error
}

// Event is something that happened, before anyone has decided what it means.
type Event struct {
	Kind string         `json:"kind"` // "kakao.message"
	At   string         `json:"at"`
	Data map[string]any `json:"data"`
}

// Actuator changes the world.
type Actuator interface {
	Module
	Specs() []action.Spec
	// Preview renders the approval card. It reads current state so an edit
	// or a delete can show what is there now, not only what is proposed.
	Preview(ctx context.Context, a action.Action) (action.Card, error)
	Execute(ctx context.Context, a action.Action) (action.Result, error)
}

// ContextSource supplies facts to reason with. The calendar is one (what is
// already booked); a knowledge graph would be another.
type ContextSource interface {
	Module
	Facts(ctx context.Context, query string) ([]Fact, error)
}

// Fact is one retrieved piece of context, ready to be put in a prompt.
type Fact struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}
