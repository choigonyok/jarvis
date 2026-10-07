// Package event is the wire shape of a calendar entry.
//
// Identical to what the agent's calendar module already used, field names
// included. The module keeps its approval card, its MCP tool schema and its
// "is this reversible" declaration - those are about the conversation and the
// gate, and belong with the agent. What moved here is the data.
package event

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Event is one entry. Times are local wall-clock strings on purpose: this is a
// personal calendar in one timezone, and storing "19:00" keeps what the
// operator typed identical to what they later read.
//
// The overlap-checking range is derived from these by the database, not stored
// here - see db/migrations/004_commitments.sql.
type Event struct {
	ID    string `json:"id"`
	Date  string `json:"date"`            // YYYY-MM-DD
	Start string `json:"start,omitempty"` // HH:MM
	End   string `json:"end,omitempty"`   // HH:MM
	Title string `json:"title"`
	Place string `json:"place,omitempty"`
	Memo  string `json:"memo,omitempty"`
	// Source records who put it here. The UI reads it; nothing branches on it,
	// because an approved AI change and a hand-typed one are equally the
	// operator's decision.
	Source    string `json:"source"` // jarvis | me | partner
	UpdatedAt string `json:"updatedAt"`

	// The fields below come from the uniple backend, a calendar shared by two
	// people. The Postgres backend leaves them empty and ignores them.

	// EndDate is the last day of a multi-day entry, YYYY-MM-DD. Empty for a
	// single day.
	EndDate string `json:"endDate,omitempty"`
	// Owner is whose entry it is: me | partner | shared. Both people see all
	// three - it labels, it does not hide.
	Owner string `json:"owner,omitempty"`
	// Recurrence is daily | weekly | biweekly | monthly | yearly on a series.
	// An occurrence of one carries an id of the form "<series id>#<date>".
	Recurrence string `json:"recurrence,omitempty"`
}

const (
	SourceAgent   = "jarvis"
	SourceHuman   = "me"
	SourcePartner = "partner"
)

const (
	OwnerMe      = "me"
	OwnerPartner = "partner"
	OwnerShared  = "shared"
)

var (
	datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	timePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

	ErrNotFound = errors.New("그런 일정이 없습니다")
)

// ValidationError is input the caller should fix, as opposed to something
// being broken here. The HTTP layer turns it into a 400 with this message
// intact, because the reader is often the model - it has to be told what was
// wrong with its tool call to send a better one.
//
// A distinct type rather than a sentinel or a message prefix: matching on the
// text of a Korean sentence is a test that passes until someone rewords it.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Validate keeps malformed dates out of storage. The model is told to send
// absolute dates; this is what makes that instruction enforceable.
//
// The agent validates too, so the model gets a fast error on its own tool
// call. This copy is the one that actually guards the table - a second writer
// (the web's direct-edit path, or a future importer) does not go through the
// agent at all.
func (e *Event) Validate() error {
	e.Title = strings.TrimSpace(e.Title)
	e.Date = strings.TrimSpace(e.Date)
	e.Start = strings.TrimSpace(e.Start)
	e.End = strings.TrimSpace(e.End)
	e.Place = strings.TrimSpace(e.Place)

	if e.Title == "" {
		return invalid("제목이 비어 있습니다")
	}
	if !datePattern.MatchString(e.Date) {
		return invalid("날짜는 YYYY-MM-DD 형식이어야 합니다: %q", e.Date)
	}
	if _, err := time.Parse("2006-01-02", e.Date); err != nil {
		return invalid("없는 날짜입니다: %q", e.Date)
	}
	for _, t := range []string{e.Start, e.End} {
		if t != "" && !timePattern.MatchString(t) {
			return invalid("시각은 HH:MM 형식이어야 합니다: %q", t)
		}
	}
	// 끝시각만 있고 시작이 없는 일정은 구간을 만들 수 없다. 데이터베이스의
	// check 제약도 같은 것을 막지만, 여기서 걸러야 사람이 읽을 메시지가 된다.
	if e.End != "" && e.Start == "" {
		return invalid("끝나는 시각만 있고 시작 시각이 없습니다")
	}
	e.EndDate = strings.TrimSpace(e.EndDate)
	if e.EndDate == e.Date {
		e.EndDate = ""
	}
	// 여러 날에 걸친 일정은 끝시각이 다음 날의 것이라 시작보다 이를 수 있다.
	if e.Start != "" && e.End != "" && e.End < e.Start && e.EndDate == "" {
		return invalid("끝나는 시각이 시작보다 빠릅니다: %s → %s", e.Start, e.End)
	}
	if e.EndDate != "" {
		if _, err := time.Parse("2006-01-02", e.EndDate); err != nil || !datePattern.MatchString(e.EndDate) {
			return invalid("끝나는 날짜는 YYYY-MM-DD 형식이어야 합니다: %q", e.EndDate)
		}
		if e.EndDate < e.Date {
			return invalid("끝나는 날짜가 시작보다 빠릅니다: %s → %s", e.Date, e.EndDate)
		}
	}
	switch e.Owner {
	case "", OwnerMe, OwnerPartner, OwnerShared:
	default:
		return invalid("owner 는 me, partner, shared 중 하나여야 합니다: %q", e.Owner)
	}
	if e.Source != SourceAgent && e.Source != SourceHuman {
		e.Source = SourceHuman
	}
	return nil
}

// Conflict is an overlap the operator may want to know about.
//
// Overlaps are not prevented: sometimes two things are deliberately booked at
// once, and the path where a person edits their own calendar does not go
// through approval. An overlap is something to notice and raise, not something
// to refuse - so this is a query result, not a constraint violation.
type Conflict struct {
	With Event `json:"with"`
}
