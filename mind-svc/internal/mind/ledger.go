package mind

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/engine"
	"github.com/choigonyok/jarvis/mind-svc/internal/ledger"
	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/slot"
)

// All of history, as far as the ledger is concerned.
const ledgerLimit = 1 << 20

// RebuildLedger makes the result ledger and the facts table again from what
// they are read from, and says how many rows each has. Nothing reads them to
// decide yet: this is the measuring stage of the to-be structure, and leaves
// everything else as it was.
func (m *Mind) RebuildLedger(ctx context.Context) (outcomes, facts int, err error) {
	st := m.Opts.Store
	now := m.Opts.Now()
	intents, err := st.Intents(ctx, ledgerLimit)
	if err != nil {
		return 0, 0, err
	}
	from := now.AddDate(-5, 0, 0)
	predictions, err := st.Predictions(ctx, from, now.AddDate(1, 0, 0))
	if err != nil {
		return 0, 0, err
	}
	// To a year ahead: a calendar entry is an occurrence on its own day.
	occurrences, err := st.Occurrences(ctx, from, now.AddDate(1, 0, 0))
	if err != nil {
		return 0, 0, err
	}
	rows := ledger.Build(ledger.Inputs{Intents: intents, Predictions: predictions, Occurrences: occurrences,
		Thing: func(name string) string {
			class, _ := st.ThingClass(ctx, name)
			return class
		},
		Now: now, Loc: m.Opts.Loc})
	if err := st.ReplaceOutcomes(ctx, rows); err != nil {
		return 0, 0, err
	}
	m.Gate.SetBook(engine.NewBook(rows))
	all, err := st.Facts(ctx, ledgerLimit)
	if err != nil {
		return len(rows), 0, err
	}
	factRows := ledger.Intervals(all, m.Opts.Loc, m.Registry().Single)
	if err := st.ReplaceFactRows(ctx, factRows); err != nil {
		return len(rows), 0, err
	}
	return len(rows), len(factRows), nil
}

func (m *Mind) rebuildLedger(ctx context.Context) error {
	_, _, err := m.RebuildLedger(ctx)
	return err
}

// adoptPage is how many edges are read from memory at a time.
const adoptPage = 500

// AdoptFacts makes the facts table speak for all of memory's graph: an edge the
// table has no row for comes in as it stands, what the graph ended is noted on
// the fact, and a fact whose edge is no longer there stays out of the graph
// when it is made again. Done once, and safe to do again. After it the graph
// can be made from the table.
func (m *Mind) AdoptFacts(ctx context.Context) (int, error) {
	from, ok := m.Opts.Memory.(memory.Exporter)
	if !ok {
		return 0, errors.New("기억을 읽을 수 없습니다")
	}
	st := m.Opts.Store
	live := map[string]memory.Edge{}
	for after := ""; ; {
		edges, err := from.Export(ctx, after, adoptPage)
		if err != nil {
			return 0, err
		}
		for _, e := range edges {
			live[e.ID] = e
		}
		if len(edges) < adoptPage {
			break
		}
		after = edges[len(edges)-1].ID
	}
	all, err := st.Facts(ctx, ledgerLimit)
	if err != nil {
		return 0, err
	}
	for _, f := range all {
		if f.Memory == nil || f.Memory.Edge == "" {
			continue
		}
		e, there := live[f.Memory.Edge]
		if f.Memory.Gone == !there && f.Memory.InvalidAt == e.InvalidAt {
			continue
		}
		f.Memory.Gone, f.Memory.InvalidAt = !there, e.InvalidAt
		if err := st.PutFact(ctx, f); err != nil {
			return 0, err
		}
	}
	if err := m.rebuildLedger(ctx); err != nil {
		return 0, err
	}
	rows, err := st.FactRows(ctx)
	if err != nil {
		return 0, err
	}
	spoken := map[string]bool{}
	for _, r := range rows {
		var body struct {
			Edge string `json:"edge"`
		}
		if json.Unmarshal(r.Body, &body) == nil {
			spoken[body.Edge] = true
		}
	}
	n := 0
	for id, e := range live {
		if spoken[id] {
			continue
		}
		if err := st.PutFactRow(ctx, legacyRow(e, m.Opts.Now())); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// legacyRow is an edge as a row of the facts table.
func legacyRow(e memory.Edge, now time.Time) model.FactRow {
	row := model.FactRow{ID: "edge:" + e.ID, Subject: e.Source.Name, Predicate: e.Relation, Object: e.Target.Name, Text: e.Fact,
		From: now, P: 1, Origin: model.FactLegacy}
	if at, err := time.Parse(time.RFC3339, e.ValidAt); err == nil {
		row.From = at
	}
	if at, err := time.Parse(time.RFC3339, e.InvalidAt); err == nil && at.After(row.From) {
		row.To = &at
	}
	body, err := json.Marshal(struct {
		memory.Edge
		ID     string `json:"id"`
		Origin string `json:"origin"`
		EdgeID string `json:"edge"`
	}{e, row.ID, model.FactLegacy, e.ID})
	if err == nil {
		row.Body = body
	}
	return row
}

// SlotReading is a slot as things stand: whether a value saved at saved still
// holds, read from the facts as they are now and not as the night left them -
// a move said this afternoon counts on this evening's order.
func (m *Mind) SlotReading(ctx context.Context, d slot.Def, saved time.Time) (slot.Reading, error) {
	facts, err := m.Opts.Store.FactsWith(ctx, d.Evidence...)
	if err != nil {
		return slot.Reading{}, err
	}
	return d.Read(ledger.Intervals(facts, m.Opts.Loc, m.Registry().Single), saved, m.Gate.Book()), nil
}

// loadBook gives the gate the ledger as it was last made, so a restart does
// not forget how things have gone until the night comes round.
func (m *Mind) loadBook(ctx context.Context) {
	rows, err := m.Opts.Store.Outcomes(ctx)
	if err != nil {
		m.Opts.Logger.Warn("결과 장부를 읽지 못했습니다", "err", err)
		return
	}
	m.Gate.SetBook(engine.NewBook(rows))
}
