// Package model holds the types every layer of the loop passes along: what
// was seen (Message), what the assistant believes (Entity, Change), what it
// wants to do about it (Intent) and when to look again (Timer).
package model

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

// ErrConflict is an entity written by someone else since it was read: read
// it again and re-decide.
var ErrConflict = errors.New("conflict")

// Kinds of entity in the world model.
const (
	KindPlan = "plan" // something that will happen: a dinner, a trip
	KindLoop = "loop" // something not yet done: a promise, a reply owed, a booking
)

// Statuses. A plan is proposed until someone agrees, then confirmed; a loop
// is open until closed.
const (
	Proposed  = "proposed"
	Confirmed = "confirmed"
	Cancelled = "cancelled"
	Done      = "done"
	Open      = "open"
	Closed    = "closed"
)

// Entity is one thing in the world model.
type Entity struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Room      string    `json:"room"` // the conversation it came from; "" if none
	State     State     `json:"state"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Live is whether the entity still asks for anything.
func (e Entity) Live() bool {
	switch e.State.Status {
	case Cancelled, Done, Closed:
		return false
	}
	return true
}

// State is what is believed about an entity now. Plans and loops share it;
// each uses the fields that mean something for it.
type State struct {
	Title   string   `json:"title"`
	Status  string   `json:"status"`
	Date    string   `json:"date,omitempty"`    // YYYY-MM-DD
	EndDate string   `json:"endDate,omitempty"` // last day of a multi-day plan
	Start   string   `json:"start,omitempty"`   // HH:MM
	End     string   `json:"end,omitempty"`     // HH:MM
	Place   string   `json:"place,omitempty"`
	People  []string `json:"people,omitempty"`
	Shared  bool     `json:"shared,omitempty"` // on the couple's shared calendar

	// Loops.
	Owner  string `json:"owner,omitempty"`  // me: I owe it | them: owed to me
	Due    string `json:"due,omitempty"`    // YYYY-MM-DD or YYYY-MM-DDTHH:MM
	PlanID string `json:"planId,omitempty"` // the plan this loop belongs to

	// Set by what was done about it, not by what was said.
	CalendarID string `json:"calendarId,omitempty"`

	Note string `json:"note,omitempty"`
}

// Evidence is the line of an observation a belief rests on. Ref names the
// observed message; Quote is the words, which must appear in it.
type Evidence struct {
	Ref   string `json:"ref"`
	Quote string `json:"quote"`
}

// Change is one step in an entity's history: who changed it, from what, to
// what, and on what grounds.
type Change struct {
	ID            int64      `json:"id"`
	EntityID      string     `json:"entityId"`
	Version       int        `json:"version"`
	Actor         string     `json:"actor"` // interpreter | observer | skill:<name> | user
	Before        *State     `json:"before,omitempty"`
	After         State      `json:"after"`
	Evidence      []Evidence `json:"evidence"`
	Confidence    float64    `json:"confidence"`
	ObservationID int64      `json:"observationId,omitempty"`
	Why           string     `json:"why,omitempty"`
	At            time.Time  `json:"at"`
}

// Intent is something a skill wants done or said. The gate decides whether
// it is done quietly, done and told, asked first, kept for the evening, or
// dropped - and records what came of it, which is what the gate learns from.
type Intent struct {
	ID         string          `json:"id"`
	Skill      string          `json:"skill"`
	Kind       string          `json:"kind"` // calendar.create | calendar.update | calendar.delete | info
	EntityID   string          `json:"entityId,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Title      string          `json:"title"`
	Body       string          `json:"body,omitempty"`
	Preview    string          `json:"preview,omitempty"` // what the action will do, as its actuator says it
	Reason     string          `json:"reason"`
	Confidence float64         `json:"confidence"`
	Evidence   []Evidence      `json:"evidence,omitempty"`
	Reversible bool            `json:"reversible"`
	Urgent     bool            `json:"urgent,omitempty"`

	Decision  string `json:"decision"`            // execute | notify | ask | digest | drop
	Why       string `json:"why,omitempty"`       // why the gate decided so
	Tier      string `json:"tier,omitempty"`      // the notification it raised: now | digest
	Status    string `json:"status"`              // pending | executed | rejected | undone | dropped | superseded | failed
	DecidedBy string `json:"decidedBy,omitempty"` // policy | user

	Result json.RawMessage `json:"result,omitempty"`
	Undo   json.RawMessage `json:"undo,omitempty"`
	Error  string          `json:"error,omitempty"`

	CreatedAt  time.Time  `json:"createdAt"`
	DecidedAt  *time.Time `json:"decidedAt,omitempty"`
	ExecutedAt *time.Time `json:"executedAt,omitempty"`
}

// Decisions and statuses of an intent.
const (
	Execute = "execute" // done quietly
	Notify  = "notify"  // done, then told, with a way to undo
	Ask     = "ask"     // a card: done only if approved
	Digest  = "digest"  // kept for the evening summary
	Drop    = "drop"

	Pending    = "pending"
	Executed   = "executed"
	Rejected   = "rejected"
	Undone     = "undone"
	Dropped    = "dropped"
	Superseded = "superseded"
	Failed     = "failed"
)

// Timer is a skill asking to look at an entity again at a time.
type Timer struct {
	ID       int64      `json:"id"`
	Skill    string     `json:"skill"`
	EntityID string     `json:"entityId"`
	Name     string     `json:"name"`
	FireAt   time.Time  `json:"fireAt"`
	FiredAt  *time.Time `json:"firedAt,omitempty"`
}

// Message is one observed chat message. Its text lives in the content store;
// the observation log carries only Ref.
type Message struct {
	Ref      string    `json:"ref"`    // kakao:3215, imessage:11, sim:42
	Source   string    `json:"source"` // kakao | imessage | sim
	Room     string    `json:"room"`   // kakao:164003400075345
	RoomName string    `json:"roomName"`
	Sender   string    `json:"sender"`
	IsMine   bool      `json:"isMine"`
	SentAt   time.Time `json:"sentAt"`
	Text     string    `json:"text"`
}

// CalendarEvent is an entry as the calendar actions carry it.
type CalendarEvent struct {
	ID      string `json:"id,omitempty"`
	Title   string `json:"title"`
	Date    string `json:"date"`
	EndDate string `json:"endDate,omitempty"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
	Place   string `json:"place,omitempty"`
	Memo    string `json:"memo,omitempty"`
	Owner   string `json:"owner,omitempty"` // me | shared
	Source  string `json:"source,omitempty"`
}

// CalendarAction is the payload of calendar.create (Event), calendar.update
// (EventID and the new Event) and calendar.delete (EventID; Event for showing
// what goes).
type CalendarAction struct {
	EventID string        `json:"eventId,omitempty"`
	Event   CalendarEvent `json:"event"`
}
