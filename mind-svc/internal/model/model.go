// Package model holds the types every layer of the loop passes along: what
// was seen (Message), what the assistant believes (Entity, Change), what it
// wants to do about it (Intent) and when to look again (Timer).
package model

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrNotFound = errors.New("not found")

// ErrRefused is a request a service turned down as it was (400, 422): sent
// again unchanged, it is turned down again.
var ErrRefused = errors.New("refused")

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

// OwnerRoom is the operator's own conversation with the assistant
// (chat-svc), read as one more room.
const OwnerRoom = "chat:owner"

// AssistantsPromise is whether a loop is the assistant's own promise in its
// conversation with the operator ("알려 드릴게요"): what it promised there is
// a reminder or a job of its own, which tells it at its moment. Someone
// else's promise passed on there names them.
func (e Entity) AssistantsPromise() bool {
	if e.Kind != KindLoop || e.Room != OwnerRoom || e.State.Owner != "them" {
		return false
	}
	for _, p := range e.State.People {
		if !strings.Contains(p, "Jarvis") && !strings.Contains(p, "자비스") && p != "비서" {
			return false
		}
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
	// Type is what kind of plan it is (데이트, 캠핑, 회식, 여행 …): what
	// the patterns and the expected length of a plan are kept by.
	Type string `json:"type,omitempty"`
	// PlaceKind is what kind of place it is (집, 식당, 영화관, 캠핑장 …);
	// Area the neighbourhood it is in (망원동), when known - what a payment
	// or a photo's place is matched against.
	PlaceKind string `json:"placeKind,omitempty"`
	Area      string `json:"area,omitempty"`

	// Loops.
	Owner  string `json:"owner,omitempty"`  // me: I owe it | them: owed to me
	Due    string `json:"due,omitempty"`    // YYYY-MM-DD or YYYY-MM-DDTHH:MM
	PlanID string `json:"planId,omitempty"` // the plan this loop belongs to

	// Set by what was done about it, not by what was said.
	CalendarID string `json:"calendarId,omitempty"`

	Note string `json:"note,omitempty"`
}

// Plan types the interpreter labels plans with.
var PlanTypes = []string{"데이트", "캠핑", "여행", "회식", "식사", "모임", "운동", "병원", "업무", "경조사", "기념일", "쇼핑", "공연", "기타"}

// Occurrence is something that happened, read from a structured source by
// rule: a payment, an item in it, a photo, a workout, a calendar entry, a
// transfer. Each is an index onto its source, which keeps the original -
// Ref says where.
type Occurrence struct {
	ID     string     `json:"id"`   // spending:812, spending:812#1, photo:<asset>, workout:2026-10-14 …
	Kind   string     `json:"kind"` // purchase | purchase.item | photo | workout | calendar | transfer
	At     time.Time  `json:"at"`
	Until  *time.Time `json:"until,omitempty"`
	Title  string     `json:"title"`           // the merchant, the item, the entry's title
	Place  string     `json:"place,omitempty"` // where it was, as the source names it
	Area   string     `json:"area,omitempty"`  // the neighbourhood, when the source tells it
	People []string   `json:"people,omitempty"`
	Things []string   `json:"things,omitempty"`
	// Class is what kind of thing (an item's category, 연료/캠핑) or of
	// plan (a calendar entry's type).
	Class  string `json:"class,omitempty"`
	Amount int64  `json:"amount,omitempty"` // won
	Count  int    `json:"count,omitempty"`  // photos in a batch, sets in a workout
	Ref    string `json:"ref"`              // the source's own id
	Quote  string `json:"quote,omitempty"`  // a few words of the source, for a card's grounds
}

// Record is a structured record for long-term memory - a calendar entry, a
// charge, a holding, a workout, a job, a day's photos - as memory-svc writes
// it straight to its graph: one edge, keyed by the source's own id, from the
// operator (or Source) to Target.
type Record struct {
	Key      string `json:"key"`
	Relation string `json:"relation"`
	Target   Node   `json:"target"`
	Source   *Node  `json:"source,omitempty"`
	Fact     string `json:"fact"`
	ValidAt  string `json:"validAt,omitempty"` // RFC 3339
}

// Node is one end of a record: its kind, its key in the source, its name.
type Node struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

// Link is a relation between two things that happened or will: a payment
// during a date, firewood bought for a camping trip, a dinner with someone.
// From and To are occurrence or entity ids.
type Link struct {
	From       string    `json:"from"`
	To         string    `json:"to"`
	Rel        string    `json:"rel"` // during | prep_for | used_during | with | after | replaces
	Confidence float64   `json:"confidence"`
	State      string    `json:"state"` // estimated | confirmed | rejected
	Evidence   []string  `json:"evidence,omitempty"`
	Why        string    `json:"why,omitempty"`
	By         string    `json:"by"` // rule | judge | pattern | user
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Link states: Estimated, then Confirmed or Rejected (the same words as a
// plan's and an intent's). And the relations.
const (
	Estimated = "estimated"

	During     = "during"
	PrepFor    = "prep_for"
	UsedDuring = "used_during"
	With       = "with"
)

// Judgement is common sense about a kind of thing and a kind of plan, not
// about any one purchase: is firewood something bought for camping?
type Judgement struct {
	Class        string  `json:"class"`
	Rel          string  `json:"rel"` // prep_for | used_during | unrelated
	Plausibility float64 `json:"plausibility"`
	Why          string  `json:"why,omitempty"`
}

// Pattern is a repetition counted from what happened: of Total plans of
// kind A, Support had a thing of class B bought within Bucket before. The
// row with B empty holds Total alone.
type Pattern struct {
	KindA    string    `json:"kindA"`
	Rel      string    `json:"rel"`
	KindB    string    `json:"kindB"`
	Bucket   string    `json:"bucket"` // d1 | d3 | d7 | d14
	Support  int       `json:"support"`
	Total    int       `json:"total"`
	LastSeen time.Time `json:"lastSeen"`
}

// Note is something believed about the operator, in a sentence a person
// can read and correct ("캠핑 며칠 전에 장작을 산다"), with what it rests on
// and how sure. A hypothesis until the operator confirms it; every change
// is a new version with its reason.
type Note struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	Scope      string   `json:"scope,omitempty"` // 캠핑, 운동, 사람:지수 …
	Status     string   `json:"status"`          // hypothesis | confirmed | rejected
	Confidence float64  `json:"confidence"`
	Evidence   []string `json:"evidence,omitempty"` // refs: occurrences, plans, days
	Rule       *Rule    `json:"rule,omitempty"`     // the machine-readable form predictions use
	// Track is how the predictions made from it went, as of when it was
	// read: counted from the scored predictions, never kept apart from them.
	Track     *Track    `json:"track,omitempty"`
	Version   int       `json:"version"`
	Reason    string    `json:"reason,omitempty"` // why this version
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Note statuses.
const (
	Hypothesis = "hypothesis"
	// Candidate is a rule the engine found and is still testing: it
	// predicts, and its predictions are scored, but nobody is shown it or
	// asked about it until they have come true often enough.
	Candidate = "candidate"
	// Confirmed and Rejected as for links.
)

// Rule is a note as something a prediction can be made from.
//
//	prep: before a plan of PlanKind, a thing of Class is bought about
//	      Bucket before (Support of Total plans)
//	rate: on days Feature(Arg) holds, Target happens at Rate, against Base
//	      on other days (Days such days seen)
//	travel: from From to To takes about Minutes
//	salary: once pay arrives, Plan is bought within days
//
// The engine's own (predict.Mine), each about Key - what is expected, as
// predict.Keys names it:
//
//	daily: Key happens on a share of days that depends on the weekday
//	       (Rates, Sunday first; Base on any day; Days days seen)
//	cycle: Key comes round every Gap days, give or take Tol (Last the
//	       latest; Days how many times seen)
//	after: within Hours of Anchor, Key follows (Support of Total times,
//	       against Base by chance)
type Rule struct {
	Kind     string   `json:"kind"`
	PlanKind string   `json:"planKind,omitempty"`
	Class    string   `json:"class,omitempty"`
	Bucket   string   `json:"bucket,omitempty"`
	Support  int      `json:"support,omitempty"`
	Total    int      `json:"total,omitempty"`
	Target   string   `json:"target,omitempty"`
	Feature  string   `json:"feature,omitempty"`
	Arg      string   `json:"arg,omitempty"`
	Rate     float64  `json:"rate,omitempty"`
	Base     float64  `json:"base,omitempty"`
	Days     int      `json:"days,omitempty"`
	Except   []string `json:"except,omitempty"` // conditions the operator added ("직전 캠핑 후 남은 경우")
	// travel: from From to To usually takes Minutes.
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Minutes int    `json:"minutes,omitempty"`
	// salary: what is bought once pay arrives (a plan in words).
	Plan string `json:"plan,omitempty"`

	// The engine's rules.
	Key    string    `json:"key,omitempty"`
	Anchor string    `json:"anchor,omitempty"`
	Rates  []float64 `json:"rates,omitempty"`
	Gap    int       `json:"gap,omitempty"`
	Tol    int       `json:"tol,omitempty"`
	Hours  int       `json:"hours,omitempty"`
	Last   string    `json:"last,omitempty"`  // YYYY-MM-DD
	Usual  string    `json:"usual,omitempty"` // HH:MM it usually happens at
	// Off: no longer predicted from - its predictions kept missing, or what
	// it rested on is no longer there.
	Off bool `json:"off,omitempty"`
	// Since is when it was found, or taken up again after it was off: its
	// track counts the predictions made since.
	Since *time.Time `json:"since,omitempty"`
	// Test is how the rule would have done on the days before it was found,
	// each predicted from the days before it only: where its track starts.
	Test *Track `json:"test,omitempty"`
}

// Track is how a rule's predictions went: how many were scored, how many
// came true, how many it called right (50% or more and it happened, or less
// and it did not), and its summed Brier score next to the plain rate's - what
// would have been said knowing only how often the thing happens.
type Track struct {
	N     int     `json:"n"`
	Hits  int     `json:"hits"`
	Right int     `json:"right"`
	Brier float64 `json:"brier"`
	Base  float64 `json:"base"`
}

// Add is the track with one more prediction scored.
func (t Track) Add(prob, base float64, happened bool) Track {
	o := 0.0
	if happened {
		o = 1
		t.Hits++
	}
	if (prob >= 0.5) == happened {
		t.Right++
	}
	t.N++
	t.Brier += (prob - o) * (prob - o)
	t.Base += (base - o) * (base - o)
	return t
}

// Plus is two tracks as one.
func (t Track) Plus(o Track) Track {
	return Track{N: t.N + o.N, Hits: t.Hits + o.Hits, Right: t.Right + o.Right, Brier: t.Brier + o.Brier, Base: t.Base + o.Base}
}

// Skill is how much smaller the rule's errors were than the plain rate's
// (Brier skill score), pulled toward 0 while there are few: 0.3 is 30%
// smaller, below 0 the rule did worse than knowing only how often.
func (t Track) Skill() float64 {
	if t.N == 0 {
		return 0
	}
	var s float64
	switch {
	case t.Base > 0:
		s = 1 - t.Brier/t.Base
	case t.Brier > 0:
		s = -1 // the plain rate was never wrong, and the rule was
	}
	if s < -1 {
		s = -1
	}
	return s * float64(t.N) / float64(t.N+3)
}

// Accuracy is the share of calls it got right, pulled toward a coin's while
// there are few.
func (t Track) Accuracy() float64 {
	return (float64(t.Right) + 1.5) / (float64(t.N) + 3)
}

// Prediction is something expected, with a probability and a time by which
// it will be known: "오늘 운동 65%", "11/14까지 장작 구매 60%". Scored
// against what happened (Brier: (prob − outcome)²); a miss is a surprise,
// and the night reflects on surprises only.
type Prediction struct {
	ID     string    `json:"id"`
	Target string    `json:"target"` // workout:2026-10-15, buy:장작:plan_x
	Text   string    `json:"text"`
	Prob   float64   `json:"prob"`
	Due    time.Time `json:"due"`
	Basis  []string  `json:"basis,omitempty"` // note ids
	PlanID string    `json:"planId,omitempty"`
	// Key is what it waits for (predict.Keys), and From when that starts to
	// count; zero: the prediction's day.
	Key  string     `json:"key,omitempty"`
	From *time.Time `json:"from,omitempty"`
	// Base is what knowing only how often the thing happens would have said:
	// what the prediction's skill is measured against.
	Base      float64    `json:"base,omitempty"`
	Outcome   *bool      `json:"outcome,omitempty"`
	Score     *float64   `json:"score,omitempty"`
	Surprise  bool       `json:"surprise,omitempty"`
	Reflected bool       `json:"reflected,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	ScoredAt  *time.Time `json:"scoredAt,omitempty"`
}

// Expectation is a prediction waiting for an occurrence, found by key: a
// new occurrence is checked against the open expectations of its key only.
type Expectation struct {
	PredictionID string `json:"predictionId"`
	Key          string `json:"key"` // purchase.item:장작, workout, transfer:민수
	// From is when an occurrence starts to count (zero: any time before
	// Due).
	From  time.Time `json:"from,omitempty"`
	Due   time.Time `json:"due"`
	State string    `json:"state"` // open | met | missed
	MetBy string    `json:"metBy,omitempty"`
}

// ReplySituation is a kind of moment a reply was written in - someone
// writing after a long silence - and the operator's first reply to it.
type ReplySituation struct {
	Kind    string    `json:"kind"` // reconnect
	Room    string    `json:"room"`
	InRef   string    `json:"inRef"`
	OutRef  string    `json:"outRef,omitempty"`
	GapDays int       `json:"gapDays"`
	At      time.Time `json:"at"`
}

// Usage is a day's model calls for one purpose, and their cost.
type Usage struct {
	Day     string  `json:"day"`
	Purpose string  `json:"purpose"`
	Calls   int     `json:"calls"`
	USD     float64 `json:"usd"`
	In      int64   `json:"inTokens"`
	Out     int64   `json:"outTokens"`
}

// Observation is one answer of a review, as given: about a subject (a
// campsite, a restaurant), on a dimension (소음, 청결 …), which way, and in
// what company and season. Never interpreted, never deleted.
type Observation struct {
	ID          int64             `json:"id"`
	SubjectType string            `json:"subjectType"` // 캠핑장 | 식당 | 숙소 | 카페
	Subject     string            `json:"subject"`
	Dimension   string            `json:"dimension"` // '' for the overall score alone
	Polarity    int               `json:"polarity"`  // +1 | -1 | 0
	Score       int               `json:"score"`     // the visit's 1~5
	Detail      string            `json:"detail,omitempty"`
	Context     map[string]string `json:"context,omitempty"` // with, season, weekday
	Source      string            `json:"source"`            // the review intent
	At          time.Time         `json:"at"`
}

// Attribute is what a subject is like on a dimension, from its observations.
type Attribute struct {
	SubjectType string  `json:"subjectType"`
	Subject     string  `json:"subject"`
	Dimension   string  `json:"dimension"`
	Value       float64 `json:"value"` // mean polarity, −1~1
	Count       int     `json:"count"`
}

// Preference is what the operator is like, drawn from several subjects
// ("소음에 민감하다"): estimated until confirmed, never asked again once
// turned down.
type Preference struct {
	Dimension  string    `json:"dimension"`
	Polarity   int       `json:"polarity"` // −1: minds it when bad; +1: seeks it
	Scope      string    `json:"scope,omitempty"`
	Status     string    `json:"status"`   // estimated | confirmed | rejected
	Subjects   []string  `json:"subjects"` // the different places it rests on
	Counter    []string  `json:"counter,omitempty"`
	Confidence float64   `json:"confidence"`
	Why        string    `json:"why,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`
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
	// Topic is what the intent is about for the gate's ledger: an intent on
	// a topic turned down, approved or raised lately is not raised again.
	// Empty: the entity is the topic, and a newer intent about it replaces
	// an unanswered one instead.
	Topic string `json:"topic,omitempty"`
	// Context is the situation the gate learns approval rates under, with
	// the skill: (schedule, calendar.create) is one, (prep, 캠핑) another.
	// Empty: the kind.
	Context string `json:"context,omitempty"`
	// Value is how much it is worth to the operator to be told, 0~1 (0:
	// the default, 1). A card is raised only if confidence × approval rate
	// × value is worth the interruption.
	Value float64 `json:"value,omitempty"`
	// Card is whether a card went out for it (mind.card): what the agent
	// shows and the operator answers.
	Card bool `json:"card,omitempty"`
	// Fields make its card a form - a question to answer, not only approve.
	// The answers come back in the payload's "answers".
	Fields []Field `json:"fields,omitempty"`
	// Link is where a tap on its notice goes in the console: the calendar
	// entry a reminder is about, the 자산 screen's allocation. Empty: the
	// console's front (the gate's url).
	Link string `json:"link,omitempty"`
	// Moment is an urgent notice bound to a moment the operator's own
	// movement made - leaving home with rain ahead ("rain"): it goes out
	// as remind.<moment>, which rings then even in quiet hours (someone
	// walking out of the door is plainly not asleep), and the day's push
	// budget does not hold it for the evening, when it would be no use.
	Moment string `json:"moment,omitempty"`

	Decision  string `json:"decision"`            // execute | notify | ask | digest | drop
	Why       string `json:"why,omitempty"`       // why the gate decided so
	Tier      string `json:"tier,omitempty"`      // the notification it raised: now | digest
	Status    string `json:"status"`              // pending | executed | rejected | undone | dropped | superseded | failed
	DecidedBy string `json:"decidedBy,omitempty"` // policy | user | conversation

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
	Executing  = "executing" // claimed by an approval; never approved twice
	Rejected   = "rejected"
	Undone     = "undone"
	Dropped    = "dropped"
	Superseded = "superseded"
	Failed     = "failed"
)

// ByConversation decides an intent the conversation asked already: the
// assistant's own card for the same change was up when mind's came, and
// mind's was not shown (the agent's suggest engine).
const ByConversation = "conversation"

// Covered is whether the intent stood aside for the same question asked in
// the conversation.
func (in Intent) Covered() bool { return in.Status == Superseded && in.DecidedBy == ByConversation }

// Field is one question on a form card.
type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"` // select | multi | text | textarea | number
	Value    string   `json:"value,omitempty"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
	Hint     string   `json:"hint,omitempty"`
}

// ContextKey is the intent's situation for learning: Context, or the kind.
func (in Intent) ContextKey() string {
	if in.Context != "" {
		return in.Context
	}
	return in.Kind
}

// PolicyStat is how one (skill, context) has gone: cards shown, and what the
// operator did with them. Derived from the intents; kept so a decision does
// not count the whole history again.
type PolicyStat struct {
	Skill     string    `json:"skill"`
	Context   string    `json:"context"`
	Shown     int       `json:"shown"`
	Approved  int       `json:"approved"`
	Rejected  int       `json:"rejected"`
	Undone    int       `json:"undone"`
	Last      string    `json:"last,omitempty"` // approved | rejected | undone
	UpdatedAt time.Time `json:"updatedAt"`
}

// Rate is the approval rate with a uniform prior: (approved+1)/(decided+2),
// so a context nobody answered yet reads 0.5, not 0 or 1.
func (p PolicyStat) Rate() float64 {
	return float64(p.Approved+1) / float64(p.Approved+p.Rejected+p.Undone+2)
}

// Outcomes of an intent the stats count.
const (
	OutcomeShown    = "shown"
	OutcomeApproved = "approved"
	OutcomeRejected = "rejected"
	OutcomeUndone   = "undone"
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

// Fact is something worth remembering that is neither a plan nor a loop: what
// someone likes or cannot eat, who is whose friend, a job changed, a thing
// bought. It is read from a conversation like any change, resting on a quoted
// line, and goes to long-term memory (memory-svc), where Graphiti puts it on
// the people and places already known and retires what it contradicts. The
// fact itself is the evidence ledger's entry: what was said, where, and what
// memory made of it.
type Fact struct {
	ID         string     `json:"id"` // mf_…: also the ref memory-svc keys it by
	Room       string     `json:"room"`
	Subject    Party      `json:"subject"`
	Relation   string     `json:"relation"` // LIKES, FRIEND_OF, WORKS_AT …
	Object     Party      `json:"object"`
	Text       string     `json:"text"`              // the fact as one Korean sentence
	ValidAt    string     `json:"validAt,omitempty"` // YYYY-MM-DD it became true, when said
	Evidence   []Evidence `json:"evidence"`
	Confidence float64    `json:"confidence"`
	Why        string     `json:"why,omitempty"`
	// Ends is what memory already held that this fact, by the same words,
	// says is no longer so ("요즘은 매운 것도 먹어" ends "매운 걸 못 먹는다").
	Ends []Ended `json:"ends,omitempty"`

	Status  string    `json:"status"` // pending | stored | duplicate | failed
	Tries   int       `json:"tries"`
	NextTry time.Time `json:"nextTry"`
	Error   string    `json:"error,omitempty"`
	// Memory is memory-svc's answer: Graphiti's judgement, not the
	// conversation's - which node each end went on, whether it repeated a
	// fact already known, which facts it ended.
	Memory    *Stored    `json:"memory,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	StoredAt  *time.Time `json:"storedAt,omitempty"`
}

// Fact statuses.
const (
	FactPending   = "pending"   // waiting to go to memory (or to go again)
	FactStored    = "stored"    // memory has it as a new fact
	FactDuplicate = "duplicate" // memory already knew it
	FactFailed    = "failed"    // given up after too many tries
)

// Party is one end of a fact: the operator, or someone or something by name -
// with the memory node it is, once known.
type Party struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"` // Person | Place | Org | Thing | Event
	Self bool   `json:"self,omitempty"`
	Node string `json:"node,omitempty"`
}

// Stored is what memory made of a fact.
type Stored struct {
	Edge      string     `json:"edge"`
	EdgeFact  string     `json:"edgeFact"` // the fact as memory keeps it: the earlier wording if this repeated one
	Duplicate bool       `json:"duplicate"`
	Expired   bool       `json:"expired"` // memory reads it as already superseded by something later
	Source    MemoryNode `json:"source"`
	Target    MemoryNode `json:"target"`
	Ended     []Ended    `json:"ended,omitempty"`
}

type MemoryNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Ended is a fact memory retired because this one contradicts it.
type Ended struct {
	ID        string `json:"id"`
	Fact      string `json:"fact"`
	InvalidAt string `json:"invalidAt,omitempty"`
}

// Alias ties a name said in conversation to the memory node it is - the table
// that keeps "지수" in one room and "지수" in another the same person once
// memory has said so.
type Alias struct {
	Name string    `json:"name"`
	Kind string    `json:"kind,omitempty"`
	Node string    `json:"node"`
	Seen int       `json:"seen"`
	At   time.Time `json:"at"`
}
