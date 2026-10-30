package mind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/choigonyok/jarvis/mind-svc/internal/gate"
	"github.com/choigonyok/jarvis/mind-svc/internal/registry"
)

// Registry is the table of kinds, actions, slots and policy as it stands.
func (m *Mind) Registry() *registry.Snapshot { return m.reg.Now() }

// LoadRegistry brings the table up to what ships with the code and reads it.
// Without the table the seed stands: what ships is what it says anyway.
func (m *Mind) LoadRegistry(ctx context.Context) error {
	if err := m.Opts.Store.SeedRegistry(ctx, registry.Seed()); err != nil {
		return err
	}
	return m.readRegistry(ctx)
}

func (m *Mind) readRegistry(ctx context.Context) error {
	rows, err := m.Opts.Store.RegistryRows(ctx)
	if err != nil {
		return err
	}
	r, err := registry.Parse(rows)
	if err != nil {
		return err
	}
	m.reg.Set(r)
	m.Gate.SetPolicy(gate.PolicyFrom(r))
	if missing := m.Gate.Unlisted(); len(missing) > 0 {
		m.Opts.Logger.Error("레지스트리에 없는 행동은 하지 않습니다", "kinds", missing)
	}
	if m.OnRegistry != nil {
		m.OnRegistry(ctx, r)
	}
	return nil
}

// WriteRegistry changes one row, as the operator's, and everything that reads
// the table reads the new one. It arrives as a command: nothing else writes
// here, and the model has no way to.
func (m *Mind) WriteRegistry(ctx context.Context, section, key string, body json.RawMessage) error {
	if err := registry.Check(section, key, body); err != nil {
		return fmt.Errorf("%w: %w", errBadRow, err)
	}
	if err := m.Opts.Store.PutRegistryRow(ctx, registry.Row{Section: section, Key: key, Body: body, By: registry.ByOperator, At: m.Opts.Now()}); err != nil {
		return err
	}
	return m.readRegistry(ctx)
}

var errBadRow = errors.New("레지스트리에 쓸 수 없습니다")

// BadRow is whether a write was turned down for what it said.
func BadRow(err error) bool { return errors.Is(err, errBadRow) }

// NoteKind says once of a kind that has no row: it was let in, and nothing
// reads it yet.
func (m *Mind) NoteKind(kind string) {
	if m.Registry().Known(kind) {
		return
	}
	m.unknownMu.Lock()
	defer m.unknownMu.Unlock()
	if m.unknown[kind] {
		return
	}
	if m.unknown == nil {
		m.unknown = map[string]bool{}
	}
	m.unknown[kind] = true
	m.Opts.Logger.Warn("레지스트리에 없는 종류입니다", "kind", kind)
}

// UnknownKinds is the kinds seen with no row since the start.
func (m *Mind) UnknownKinds() []string {
	m.unknownMu.Lock()
	defer m.unknownMu.Unlock()
	out := make([]string, 0, len(m.unknown))
	for k := range m.unknown {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
