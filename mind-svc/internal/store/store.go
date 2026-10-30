// Package store keeps everything the loop remembers: the world model and its
// history, the text of observed messages, how far each conversation has been
// read, the skills' timers, the intents and what came of them, the sensors'
// cursors, and the facts sent to long-term memory with what they rested on.
//
// Two implementations behind one interface: Memory, for running the whole
// loop on a laptop with nothing else (and for tests), and Postgres (migrations
// 035, 038) for the stack.
package store

import (
	"context"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

type Store interface {
	// World model. Put writes an entity whose Version is the one it was read
	// at (0 for a new one) and records the change; a stale version is
	// model.ErrConflict.
	Entity(ctx context.Context, id string) (model.Entity, error)
	Entities(ctx context.Context, kind, room string, liveOnly bool) ([]model.Entity, error)
	Put(ctx context.Context, e model.Entity, c model.Change) (model.Entity, model.Change, error)
	Changes(ctx context.Context, entityID string, limit int) ([]model.Change, error)
	// PlansOn is every plan whose days (date to endDate) touch [from, to],
	// both YYYY-MM-DD: what an occurrence on a day can belong to, found
	// without reading every plan there ever was.
	PlansOn(ctx context.Context, from, to string) ([]model.Entity, error)

	// Content: observed messages, idempotent by Ref.
	PutMessage(ctx context.Context, m model.Message) error
	// PruneMessages deletes messages sent before t. It returns how many.
	PruneMessages(ctx context.Context, before time.Time) (int64, error)
	Message(ctx context.Context, ref string) (model.Message, error)
	// RoomMessages is a room's messages sent after `after` and before
	// `before` (zero: no bound), oldest first, at most limit of the newest.
	// Both bounds are exclusive: "after what was already read" must not
	// depend on how finely a database keeps time.
	RoomMessages(ctx context.Context, room string, after, before time.Time, limit int) ([]model.Message, error)
	// RoomMessagesFrom is the same, but at most limit of the oldest: a long
	// past read from its start.
	RoomMessagesFrom(ctx context.Context, room string, after, before time.Time, limit int) ([]model.Message, error)
	// SearchMessages is up to q.Limit messages whose text holds every one of
	// q.Terms, newest first. Text is sealed, so it is matched here rather
	// than in the database: at most q.Scan messages are read, newest first.
	SearchMessages(ctx context.Context, q MessageQuery) ([]model.Message, error)
	// MessageContext is the message ref with up to before messages ahead of
	// it and after behind it in its room, oldest first.
	MessageContext(ctx context.Context, ref string, before, after int) ([]model.Message, error)
	Rooms(ctx context.Context) ([]Room, error)

	// How far each conversation has been interpreted.
	MarkRoom(ctx context.Context, room string, through time.Time) error

	// Timers: one per (skill, entity, name); setting it again moves it.
	SetTimer(ctx context.Context, t model.Timer) error
	CancelTimers(ctx context.Context, skill, entityID string) error
	DueTimers(ctx context.Context, now time.Time) ([]model.Timer, error)
	FireTimer(ctx context.Context, id int64, at time.Time) error
	Timers(ctx context.Context) ([]model.Timer, error)

	// Intents.
	PutIntent(ctx context.Context, in model.Intent) error
	Intent(ctx context.Context, id string) (model.Intent, error)
	Intents(ctx context.Context, limit int) ([]model.Intent, error)
	IntentsFor(ctx context.Context, entityID string) ([]model.Intent, error)
	// IntentsSince is every intent created since t, newest first - what the
	// gate learns from and counts against the day's budget.
	IntentsSince(ctx context.Context, t time.Time) ([]model.Intent, error)

	// Sensor cursors.
	Cursor(ctx context.Context, name string) (int64, error)
	SetCursor(ctx context.Context, name string, v int64) error

	// Facts: the evidence ledger of what goes to long-term memory, and its
	// outbox - a pending fact is sent until memory answers.
	PutFact(ctx context.Context, f model.Fact) error
	Fact(ctx context.Context, id string) (model.Fact, error)
	Facts(ctx context.Context, limit int) ([]model.Fact, error) // newest first
	// DueFacts is pending facts whose next try has come, oldest first.
	DueFacts(ctx context.Context, now time.Time, limit int) ([]model.Fact, error)
	// HeldFacts is every fact set aside after failing on its own, oldest
	// first.
	HeldFacts(ctx context.Context) ([]model.Fact, error)
	// FactsOn is the facts with a relation created in [from, to), oldest
	// first.
	FactsOn(ctx context.Context, relation string, from, to time.Time) ([]model.Fact, error)
	// FactsWith is every fact with one of these relations (any case), newest
	// first, with no limit - ledgers must not lose old entries.
	FactsWith(ctx context.Context, relations ...string) ([]model.Fact, error)

	// Records sent to long-term memory: the hash of what was last sent
	// under a key ("" when nothing was).
	RecordHash(ctx context.Context, key string) (string, error)
	PutRecordHash(ctx context.Context, key, hash string, at time.Time) error

	// Policy: approval counts per (skill, context). Count adds one outcome;
	// ResetPolicy empties the table before a rebuild from the intents.
	Policy(ctx context.Context, skill, context string) (model.PolicyStat, error)
	Policies(ctx context.Context) ([]model.PolicyStat, error)
	CountPolicy(ctx context.Context, skill, context, outcome string, at time.Time) error
	ResetPolicy(ctx context.Context) error

	// Occurrences: what happened, read from structured sources; idempotent
	// by ID. Between is [from, to), kinds empty for all, oldest first.
	PutOccurrence(ctx context.Context, o model.Occurrence) error
	Occurrence(ctx context.Context, id string) (model.Occurrence, error)
	Occurrences(ctx context.Context, from, to time.Time, kinds ...string) ([]model.Occurrence, error)

	// Links between occurrences and entities, one per (from, to, rel).
	PutLink(ctx context.Context, l model.Link) error
	// LinksOf is every link from or to id.
	LinksOf(ctx context.Context, id string) ([]model.Link, error)
	// LinksSince is every link changed at or after t, oldest first.
	LinksSince(ctx context.Context, t time.Time) ([]model.Link, error)

	// The judge's caches: a thing's class by its name, and common sense
	// about (class, plan kind). ErrNotFound when not judged yet.
	ThingClass(ctx context.Context, name string) (string, error)
	PutThingClass(ctx context.Context, name, class string) error
	LinkCache(ctx context.Context, class, planKind string) (model.Judgement, error)
	PutLinkCache(ctx context.Context, planKind string, j model.Judgement) error

	// Patterns: counts of repetition. CountPattern adds to a row (support
	// and total by the deltas); Patterns is every row for a plan kind.
	CountPattern(ctx context.Context, p model.Pattern) error
	Patterns(ctx context.Context, kindA string) ([]model.Pattern, error)
	ResetPatterns(ctx context.Context) error

	// Notes about the operator, with their versions. PutNote writes the
	// note and records its version (Version must be the next one).
	PutNote(ctx context.Context, n model.Note) error
	Note(ctx context.Context, id string) (model.Note, error)
	Notes(ctx context.Context) ([]model.Note, error)
	NoteVersions(ctx context.Context, id string) ([]model.Note, error)

	// Predictions, and the expectations they wait on.
	PutPrediction(ctx context.Context, p model.Prediction) error
	Prediction(ctx context.Context, id string) (model.Prediction, error)
	// Predictions is every prediction due in [from, to).
	Predictions(ctx context.Context, from, to time.Time) ([]model.Prediction, error)
	// DuePredictions is the unscored predictions due by now.
	DuePredictions(ctx context.Context, now time.Time) ([]model.Prediction, error)
	PutExpectation(ctx context.Context, e model.Expectation) error
	// OpenExpectations is the open expectations of a key.
	OpenExpectations(ctx context.Context, key string) ([]model.Expectation, error)
	Expectations(ctx context.Context, predictionID string) ([]model.Expectation, error)

	// Reply situations: AddReplySituation opens one (idempotent by kind
	// and in-ref); AnswerReplySituations gives the open ones of a room their
	// reply; ReplyExamples is the latest answered ones of a kind.
	AddReplySituation(ctx context.Context, r model.ReplySituation) error
	AnswerReplySituations(ctx context.Context, room, outRef string) error
	ReplyExamples(ctx context.Context, kind string, limit int) ([]model.ReplySituation, error)
	ResetReplySituations(ctx context.Context) error

	// LLM usage by day and purpose. AddUsage adds one call; Usages is every
	// day from `from` (YYYY-MM-DD) on.
	AddUsage(ctx context.Context, u model.Usage) error
	Usages(ctx context.Context, from string) ([]model.Usage, error)

	// Reviews: observations as given, attributes computed from them, and
	// preferences. ForgetObservations drops a review's answers (undone).
	AddObservations(ctx context.Context, obs []model.Observation) error
	ForgetObservations(ctx context.Context, source string) error
	ObservationsOn(ctx context.Context, dimension string) ([]model.Observation, error)
	Attributes(ctx context.Context, subjectType, subject string) ([]model.Attribute, error)
	PutPreference(ctx context.Context, p model.Preference) error
	Preferences(ctx context.Context) ([]model.Preference, error)

	// The result ledger and the facts with the time they hold: both are made
	// again whole from what they are read from (internal/ledger), so a
	// rebuild replaces them.
	ReplaceOutcomes(ctx context.Context, rows []model.Outcome) error
	Outcomes(ctx context.Context) ([]model.Outcome, error)
	ReplaceFactRows(ctx context.Context, rows []model.FactRow) error
	FactRows(ctx context.Context) ([]model.FactRow, error)

	// Aliases: names in conversation → memory nodes. Putting a name again
	// moves it to the node given and counts it seen once more.
	PutAlias(ctx context.Context, a model.Alias) error
	Aliases(ctx context.Context) ([]model.Alias, error)
}

// Room is a conversation as the store knows it.
// MessageQuery is a search over message text: what the conversation asks
// when long-term memory did not keep what it needs (a word's meaning, who
// said exactly what).
type MessageQuery struct {
	// Terms must all be in the text, in any case. Plain substrings: Korean
	// has no spaces between a word and its particle, so a word is found
	// inside a longer one ("갓생" in "갓생이라").
	Terms []string
	// Room narrows to a room whose id is Room or whose name contains it.
	Room string
	// Since narrows to messages sent after it (zero: any time).
	Since time.Time
	Limit int
	// Scan is how many messages are read at most, newest first.
	Scan int
}

// normalized lowers and trims the terms and drops empty ones.
func (q MessageQuery) normalized() MessageQuery {
	terms := make([]string, 0, len(q.Terms))
	for _, t := range q.Terms {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			terms = append(terms, t)
		}
	}
	q.Terms = terms
	q.Room = strings.TrimSpace(q.Room)
	return q
}

// matches is whether text holds every (normalized) term.
func (q MessageQuery) matches(text string) bool {
	text = strings.ToLower(text)
	for _, t := range q.Terms {
		if !strings.Contains(text, t) {
			return false
		}
	}
	return true
}

// inRoom is whether a message is in the room the query names, if any.
func (q MessageQuery) inRoom(m model.Message) bool {
	return q.Room == "" || m.Room == q.Room || strings.Contains(strings.ToLower(m.RoomName), strings.ToLower(q.Room))
}

type Room struct {
	Room          string    `json:"room"`
	Name          string    `json:"name"`
	Source        string    `json:"source"`
	LastAt        time.Time `json:"lastAt"`
	Interpreted   time.Time `json:"interpreted"` // messages up to here have been read
	Messages      int       `json:"messages"`
	OnlyMine      bool      `json:"onlyMine"` // every message is mine: a notes-to-self room
	Uninterpreted int       `json:"uninterpreted"`
}
