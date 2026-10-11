package mind

import (
	"context"
	"log/slog"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// Remember sends the ledger's pending facts to long-term memory and writes
// back what memory made of them. The ledger is the outbox: a fact read while
// memory-svc is down or out of budget goes when it can, and a fact sent twice
// is answered from what memory already did with it.
type Remember struct {
	Store  store.Store
	Memory memory.Client
	Now    func() time.Time
	Log    *slog.Logger
	nudge  chan struct{}
}

// MaxTries is how often a fact is sent before it is given up on.
const MaxTries = 12

func NewRemember(st store.Store, mem memory.Client, now func() time.Time, log *slog.Logger) *Remember {
	return &Remember{Store: st, Memory: mem, Now: now, Log: log, nudge: make(chan struct{}, 1)}
}

// Nudge asks for a flush soon: a fact was just written.
func (r *Remember) Nudge() {
	select {
	case r.nudge <- struct{}{}:
	default:
	}
}

func (r *Remember) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.nudge:
		}
		for {
			n, err := r.Flush(ctx)
			if err != nil && ctx.Err() == nil {
				r.Log.Warn("기억에 사실을 보내지 못했습니다", "err", err)
			}
			if err != nil || n < flushBatch {
				break
			}
		}
	}
}

const flushBatch = 20

// Flush sends what is due, one batch, and reports how many went.
func (r *Remember) Flush(ctx context.Context) (int, error) {
	due, err := r.Store.DueFacts(ctx, r.Now(), flushBatch)
	if err != nil || len(due) == 0 {
		return 0, err
	}
	// A name memory has already placed goes as that node, not to be
	// resolved again.
	aliases, err := r.Store.Aliases(ctx)
	if err != nil {
		return 0, err
	}
	node := map[string]string{}
	for _, a := range aliases {
		node[a.Name] = a.Node
	}
	for i := range due {
		for _, p := range []*model.Party{&due[i].Subject, &due[i].Object} {
			if !p.Self && p.Node == "" {
				p.Node = node[p.Name]
			}
		}
	}
	results, err := r.Memory.Put(ctx, due)
	if err != nil {
		for _, f := range due {
			if perr := r.retry(ctx, f, err.Error()); perr != nil {
				return 0, perr
			}
		}
		return 0, err
	}
	byRef := map[string]memory.Result{}
	for _, res := range results {
		byRef[res.Ref] = res
	}
	now := r.Now()
	for _, f := range due {
		res, ok := byRef[f.ID]
		switch {
		case !ok:
			err = r.retry(ctx, f, "기억이 이 사실에 답하지 않았습니다")
		case res.Later != "":
			err = r.retry(ctx, f, res.Later)
		default:
			err = r.stored(ctx, f, res, now)
		}
		if err != nil {
			return 0, err
		}
	}
	return len(due), nil
}

func (r *Remember) stored(ctx context.Context, f model.Fact, res memory.Result, now time.Time) error {
	f.Memory = res.Stored()
	f.Status, f.Error, f.StoredAt = model.FactStored, "", &now
	if res.Duplicate {
		f.Status = model.FactDuplicate
	}
	if !f.Subject.Self {
		f.Subject.Node = res.Source.ID
	}
	if !f.Object.Self {
		f.Object.Node = res.Target.ID
	}
	if err := r.Store.PutFact(ctx, f); err != nil {
		return err
	}
	for _, p := range []model.Party{f.Subject, f.Object} {
		if p.Self || p.Node == "" {
			continue
		}
		if err := r.Store.PutAlias(ctx, model.Alias{Name: p.Name, Kind: p.Kind, Node: p.Node, At: now}); err != nil {
			return err
		}
	}
	r.Log.Info("기억에 넣었습니다", "fact", f.ID, "status", f.Status, "ended", len(res.Ended))
	return nil
}

// retry puts a fact back with a later try - a minute, then doubling up to six
// hours - or gives up on it after MaxTries.
func (r *Remember) retry(ctx context.Context, f model.Fact, why string) error {
	f.Tries++
	f.Error = why
	if f.Tries >= MaxTries {
		f.Status = model.FactFailed
	} else {
		f.NextTry = r.Now().Add(min(time.Minute<<(f.Tries-1), 6*time.Hour))
	}
	return r.Store.PutFact(ctx, f)
}
