package mind

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// Remember sends the ledger's pending facts to long-term memory and writes
// back what memory made of them. The ledger is the outbox: a fact read while
// memory-svc is down or out of budget goes when it can, and a fact sent twice
// is answered from what memory already did with it.
//
// A failure is read by the stack's rule (gocommon/health). Memory not
// answering, or saying it is out of its day's budget or its own services are
// down, is an outage: the facts wait without losing a try, and the next
// attempt waits longer each time. A fact memory fails on while it is working
// is tried again later each time, and held after health.MaxTries; one memory
// turns down as it is (400, 422) is held at once - sent unchanged it would be
// turned down again. Held is not given up: the fact stays on the ledger (and
// in the life ledgers read from it), the status tab counts it, it goes again
// once when memory comes back from an outage, and whenever a person releases
// it. Only a person drops one.
type Remember struct {
	Store  store.Store
	Memory memory.Client
	Now    func() time.Time
	Log    *slog.Logger
	nudge  chan struct{}

	mu sync.Mutex
	// outages is how many flushes in a row met an outage, and pause the
	// time before which no flush is tried.
	outages int
	pause   time.Time
}

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

// Backoffs: a fact that failed on its own, and memory being out.
const (
	retryFirst  = time.Minute
	retryMost   = 6 * time.Hour
	outageFirst = time.Minute
	outageMost  = 30 * time.Minute
)

// Flush sends what is due, one batch, and reports how many went.
func (r *Remember) Flush(ctx context.Context) (int, error) {
	now := r.Now()
	r.mu.Lock()
	paused := now.Before(r.pause)
	r.mu.Unlock()
	if paused {
		return 0, nil
	}
	due, err := r.Store.DueFacts(ctx, now, flushBatch)
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
	switch {
	case errors.Is(err, model.ErrRefused) && len(due) > 1:
		// The batch was turned down as a whole: one at a time, so the one
		// memory will not take is the only one it costs.
		return r.oneByOne(ctx, due)
	case errors.Is(err, model.ErrRefused):
		return 0, r.hold(ctx, due[0], "기억이 받지 않았습니다: "+err.Error())
	case err != nil:
		r.outage(now)
		return 0, err
	}
	byRef := map[string]memory.Result{}
	for _, res := range results {
		byRef[res.Ref] = res
	}
	stored := 0
	out := false
	for _, f := range due {
		res, ok := byRef[f.ID]
		switch {
		case !ok:
			err = r.retry(ctx, f, "기억이 이 사실에 답하지 않았습니다")
		case res.Later != "" && res.Outage:
			err = r.wait(ctx, f, res.Later)
			out = true
		case res.Later != "":
			err = r.retry(ctx, f, res.Later)
		default:
			err = r.stored(ctx, f, res, r.Now())
			stored++
		}
		if err != nil {
			return 0, err
		}
	}
	if out && stored == 0 {
		r.outage(now)
	} else if stored > 0 {
		if err := r.back(ctx); err != nil {
			return len(due), err
		}
	}
	return len(due), nil
}

// oneByOne sends a batch memory turned down one fact at a time.
func (r *Remember) oneByOne(ctx context.Context, due []model.Fact) (int, error) {
	for _, f := range due {
		res, err := r.Memory.Put(ctx, []model.Fact{f})
		switch {
		case errors.Is(err, model.ErrRefused):
			err = r.hold(ctx, f, "기억이 받지 않았습니다: "+err.Error())
		case err != nil:
			r.outage(r.Now())
			return 0, err
		case len(res) == 0 || res[0].Ref != f.ID:
			err = r.retry(ctx, f, "기억이 이 사실에 답하지 않았습니다")
		case res[0].Later != "" && res[0].Outage:
			err = r.wait(ctx, f, res[0].Later)
		case res[0].Later != "":
			err = r.retry(ctx, f, res[0].Later)
		default:
			err = r.stored(ctx, f, res[0], r.Now())
		}
		if err != nil {
			return 0, err
		}
	}
	return len(due), nil
}

// outage pauses flushing, longer each time in a row.
func (r *Remember) outage(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outages++
	r.pause = now.Add(health.Backoff(r.outages, outageFirst, outageMost))
}

// back is memory taking facts again. Coming back from an outage, what was
// held goes once more.
func (r *Remember) back(ctx context.Context) error {
	r.mu.Lock()
	was := r.outages > 0
	r.outages, r.pause = 0, time.Time{}
	r.mu.Unlock()
	if !was {
		return nil
	}
	n, err := r.Release(ctx, "")
	if n > 0 {
		r.Log.Info("기억이 돌아와 보류한 사실을 다시 보냅니다", "count", n)
	}
	return err
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

// retry counts a try the fact failed on its own: it goes again later each
// time, and is held after health.MaxTries. One that comes back from being
// held has a single try before it is held again.
func (r *Remember) retry(ctx context.Context, f model.Fact, why string) error {
	f.Tries++
	if f.Tries >= health.MaxTries {
		return r.hold(ctx, f, why)
	}
	f.Error = why
	f.NextTry = r.Now().Add(health.Backoff(f.Tries, retryFirst, retryMost))
	return r.Store.PutFact(ctx, f)
}

// wait puts a fact back for an outage memory reported on it: no try is
// counted.
func (r *Remember) wait(ctx context.Context, f model.Fact, why string) error {
	f.Error = why
	f.NextTry = r.Now().Add(time.Hour)
	return r.Store.PutFact(ctx, f)
}

func (r *Remember) hold(ctx context.Context, f model.Fact, why string) error {
	f.Status, f.Error = model.FactHeld, why
	r.Log.Warn("기억에 넣지 못한 사실을 보류합니다", "fact", f.ID, "tries", f.Tries, "why", why)
	return r.Store.PutFact(ctx, f)
}

// Release sends held facts again - all of them, or the one id names - and
// reports how many.
func (r *Remember) Release(ctx context.Context, id string) (int, error) {
	held, err := r.Store.HeldFacts(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range held {
		if id != "" && f.ID != id {
			continue
		}
		f.Status, f.NextTry = model.FactPending, r.Now()
		if err := r.Store.PutFact(ctx, f); err != nil {
			return n, err
		}
		n++
	}
	if n > 0 {
		r.Nudge()
	}
	return n, nil
}

// Drop is a person's decision that a held fact is not to be remembered. It
// leaves the life ledgers too.
func (r *Remember) Drop(ctx context.Context, id string) error {
	f, err := r.Store.Fact(ctx, id)
	if err != nil {
		return err
	}
	if f.Status != model.FactHeld {
		return model.ErrConflict
	}
	f.Status = model.FactDropped
	return r.Store.PutFact(ctx, f)
}
