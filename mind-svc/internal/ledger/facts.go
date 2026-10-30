package ledger

import (
	"cmp"
	"slices"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// singleFacts are the relations with one answer at a time: a move ends the
// old address, and the table refuses two at once. They become slots of the
// registry when it comes.
var singleFacts = map[string]bool{
	"LIVES_IN":    true,
	"HAS_ADDRESS": true,
	"WORKS_AT":    true,
	"STUDIES_AT":  true,
	"BIRTHDAY_ON": true,
}

// party is one end of a fact as a key: memory's node once it has placed the
// name, the name until then.
func party(p model.Party) string {
	switch {
	case p.Self:
		return "self"
	case p.Node != "":
		return "node:" + p.Node
	}
	return p.Name
}

func validFrom(f model.Fact, loc *time.Location) time.Time {
	if f.ValidAt != "" {
		if t, err := time.ParseInLocation("2006-01-02", f.ValidAt, loc); err == nil {
			return t
		}
	}
	return f.CreatedAt
}

// Intervals is the facts ledger as facts with the time they hold. A single
// fact holds until the next different one; the same thing said again keeps
// the first time it was said and the surest word. What memory said a later
// fact ended is closed there. A fact a person dropped is not one.
func Intervals(facts []model.Fact, loc *time.Location) []model.FactRow {
	if loc == nil {
		loc = time.UTC
	}
	sorted := slices.Clone(facts)
	slices.SortStableFunc(sorted, func(a, b model.Fact) int {
		return cmp.Or(validFrom(a, loc).Compare(validFrom(b, loc)), a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	var rows []model.FactRow
	// The row now standing for a (subject, predicate, object); for a single
	// fact, the one for its (subject, predicate).
	open := map[string]int{}
	edges := map[string]int{} // memory's edge → row
	ended := map[int]time.Time{}
	for _, f := range sorted {
		if f.Status == model.FactDropped {
			continue
		}
		row := model.FactRow{ID: f.ID, Subject: party(f.Subject), Predicate: f.Relation, Object: party(f.Object), Text: f.Text,
			From: validFrom(f, loc), P: f.Confidence, Single: singleFacts[f.Relation]}
		key := row.Subject + "\x00" + row.Predicate
		if !row.Single {
			key += "\x00" + row.Object
		}
		i, seen := open[key]
		if seen && rows[i].Object == row.Object {
			rows[i].P = max(rows[i].P, row.P)
		} else {
			if seen {
				if !row.From.After(rows[i].From) {
					rows[i].ID = "" // said at the same moment: the later word stands
				} else {
					to := row.From
					rows[i].To = &to
				}
			}
			rows = append(rows, row)
			i = len(rows) - 1
			open[key] = i
		}
		if f.Memory != nil && f.Memory.Edge != "" {
			edges[f.Memory.Edge] = i
		}
		for _, e := range f.Ends {
			j, ok := edges[e.ID]
			if !ok {
				continue
			}
			at := row.From
			if e.InvalidAt != "" {
				if t, err := time.ParseInLocation("2006-01-02", e.InvalidAt, loc); err == nil {
					at = t
				}
			}
			ended[j] = at
		}
	}
	out := make([]model.FactRow, 0, len(rows))
	for i, r := range rows {
		if r.ID == "" {
			continue
		}
		if at, ok := ended[i]; ok && (r.To == nil || at.Before(*r.To)) {
			if !at.After(r.From) {
				continue // ended before it began: it never held
			}
			r.To = &at
		}
		out = append(out, r)
	}
	return out
}
