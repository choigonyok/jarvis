// Package book is the wire shape of the training log.
//
// It is deliberately identical to what web/src/lib/workout.ts already sends
// and reads. Splitting the service out and changing the contract in the same
// step would mean two suspects when something stops working, so the JSON here
// is the JSON that was there before - the storage underneath is what changed.
package book

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// Set is one set. Weight is kg; bodyweight movements are logged as 0 and
// still count their reps.
type Set struct {
	Weight float64 `json:"weight"`
	Reps   int     `json:"reps"`
	Done   bool    `json:"done"`
	// Warmup is work you did but not work that counts. Counting empty-bar
	// sets into weekly volume makes the number useless for deciding whether
	// to add weight.
	Warmup bool `json:"warmup,omitempty"`
}

type Exercise struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Sets []Set  `json:"sets"`
	Note string `json:"note,omitempty"`
}

type Session struct {
	Date        string     `json:"date"` // YYYY-MM-DD, local
	ID          string     `json:"id"`
	RoutineID   string     `json:"routineId,omitempty"`
	RoutineName string     `json:"routineName,omitempty"`
	Exercises   []Exercise `json:"exercises"`
	StartedAt   int64      `json:"startedAt"`         // epoch ms
	EndedAt     *int64     `json:"endedAt,omitempty"` // nil = 진행 중
	Note        string     `json:"note,omitempty"`
	BodyWeight  *float64   `json:"bodyWeight,omitempty"`
}

type Routine struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Exercises []string `json:"exercises"`
}

// Book is the whole log. RestTarget and Routines are pointers/slices that may
// be absent: an absent Routines means "the client's defaults apply", and
// persisting those defaults would turn a fallback into a stored decision the
// operator never made.
type Book struct {
	Sessions   []Session `json:"sessions"`
	RestTarget *int      `json:"restTarget,omitempty"` // seconds
	Routines   []Routine `json:"routines,omitempty"`
}

// Fingerprint is what decides whether a session needs rewriting. It covers
// only the parts that live in child rows - exercises and their sets - because
// the session row itself is upserted every time regardless.
//
// Field order is fixed by the struct, and sets keep the order they were sent
// in, so the same log always hashes the same. Sorting here would be wrong:
// reordering exercises IS a change.
func (s Session) Fingerprint() string {
	body, err := json.Marshal(s.Exercises)
	if err != nil {
		// A log that cannot be marshalled cannot be compared; returning an
		// empty hash forces a rewrite, which is the safe direction.
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// Normalize sorts sessions the way every reader expects - newest last, which
// is the order the file used to be written in - and drops the obviously
// broken. A session with no id cannot be addressed later, so keeping it would
// create a row nothing can ever update.
func Normalize(b *Book) {
	kept := b.Sessions[:0]
	for _, s := range b.Sessions {
		if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Date) == "" {
			continue
		}
		kept = append(kept, s)
	}
	b.Sessions = kept
	sort.SliceStable(b.Sessions, func(i, j int) bool {
		return b.Sessions[i].StartedAt < b.Sessions[j].StartedAt
	})
}
