package mind

import (
	"context"

	"github.com/choigonyok/jarvis/mind-svc/internal/ledger"
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
	all, err := st.Facts(ctx, ledgerLimit)
	if err != nil {
		return len(rows), 0, err
	}
	factRows := ledger.Intervals(all, m.Opts.Loc)
	if err := st.ReplaceFactRows(ctx, factRows); err != nil {
		return len(rows), 0, err
	}
	return len(rows), len(factRows), nil
}

func (m *Mind) rebuildLedger(ctx context.Context) error {
	_, _, err := m.RebuildLedger(ctx)
	return err
}
