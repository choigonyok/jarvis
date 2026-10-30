package ledger

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// indexEnd is one end of a row in memory's graph.
type indexEnd struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Kind string `json:"kind,omitempty"`
	Self bool   `json:"self,omitempty"`
}

// indexRow is how a checked fact goes into memory's graph, which is made from
// this table: on the edge and the nodes memory put it on, when it has.
type indexRow struct {
	ID        string   `json:"id"`
	Origin    string   `json:"origin"`
	Edge      string   `json:"edge,omitempty"`
	Relation  string   `json:"relation"`
	Fact      string   `json:"fact"`
	ValidAt   string   `json:"validAt,omitempty"`
	InvalidAt string   `json:"invalidAt,omitempty"`
	Source    indexEnd `json:"source"`
	Target    indexEnd `json:"target"`
}

func indexEndOf(p model.Party, placed model.MemoryNode) indexEnd {
	if p.Self {
		return indexEnd{Self: true}
	}
	e := indexEnd{ID: p.Node, Name: p.Name, Kind: p.Kind}
	if placed.ID != "" {
		e.ID, e.Name = placed.ID, placed.Name
	}
	return e
}

// bodyOf is a row as its fact goes into memory's graph. A fact memory turned
// down, or one no longer in the graph, stays a fact and stays out of it.
func bodyOf(r model.FactRow, f model.Fact) json.RawMessage {
	if f.Status == model.FactHeld || (f.Memory != nil && f.Memory.Gone) {
		return nil
	}
	in := indexRow{ID: r.ID, Origin: model.FactChecked, Relation: f.Relation, Fact: f.Text, ValidAt: r.From.Format(time.RFC3339)}
	if r.To != nil {
		in.InvalidAt = r.To.Format(time.RFC3339)
	}
	var source, target model.MemoryNode
	if f.Memory != nil {
		in.Edge, source, target = f.Memory.Edge, f.Memory.Source, f.Memory.Target
		if f.Memory.EdgeFact != "" {
			in.Fact = f.Memory.EdgeFact
		}
	}
	in.Source, in.Target = indexEndOf(f.Subject, source), indexEndOf(f.Object, target)
	raw, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	return raw
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

// endOf reads when something ended: a day, or a time as memory says it.
func endOf(v string, loc *time.Location) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	t, err := time.ParseInLocation("2006-01-02", v, loc)
	return t, err == nil
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
// fact (one answer at a time, as the registry says) holds until the next
// different one; the same thing said again keeps
// the first time it was said and the surest word. What memory said a later
// fact ended is closed there. A fact a person dropped is not one.
func Intervals(facts []model.Fact, loc *time.Location, single func(string) bool) []model.FactRow {
	if loc == nil {
		loc = time.UTC
	}
	sorted := slices.Clone(facts)
	slices.SortStableFunc(sorted, func(a, b model.Fact) int {
		return cmp.Or(validFrom(a, loc).Compare(validFrom(b, loc)), a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	var rows []model.FactRow
	// The fact each row stands on.
	stands := map[int]model.Fact{}
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
			From: validFrom(f, loc), P: f.Confidence, Single: single(f.Relation)}
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
			stands[i] = f
		}
		if f.Memory != nil && f.Memory.Edge != "" {
			edges[f.Memory.Edge] = i
		}
		retired := f.Ends
		if f.Memory != nil {
			retired = append(slices.Clone(retired), f.Memory.Ended...)
			if at, ok := endOf(f.Memory.InvalidAt, loc); ok {
				ended[i] = at
			}
		}
		for _, e := range retired {
			j, ok := edges[e.ID]
			if !ok {
				continue
			}
			at := row.From
			if t, ok := endOf(e.InvalidAt, loc); ok {
				at = t
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
		r.Origin, r.Body = model.FactChecked, bodyOf(r, stands[i])
		out = append(out, r)
	}
	return out
}
