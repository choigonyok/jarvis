// Package check runs the probes and keeps the latest verdict of each.
//
// A probe answers one question a person would ask ("are KakaoTalk messages
// still arriving?"), not "is container X up". Probes only read signals that
// already exist - a service's own status, a timestamp in Postgres, a cookie
// on disk. None of them spends a rate-limited resource: the uniple refresh
// token rotates on use and KIS issues one token a minute, so a health check
// that exercised either would be the thing that broke them.
package check

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

type State string

const (
	OK      State = "ok"
	Warn    State = "warn"
	Fail    State = "fail"
	Unknown State = "unknown"
)

// Verdict is what a probe returns.
type Verdict struct {
	State State
	// Summary is the one sentence shown on the row: what is true right now.
	Summary string
	// ObservedAt is the newest signal the verdict rests on (the last message,
	// the last alert), shown as "<ObservedLabel> 3일 전". Nil when the verdict
	// is not about an age.
	ObservedAt    *time.Time
	ObservedLabel string
	// Facts are extra lines for the expanded row.
	Facts []string
}

// Probe is one check.
type Probe struct {
	ID    string
	Group string
	Name  string
	// Rule says how the state is decided, and Fix what to do when it is not
	// ok. Both are shown when the row is opened.
	Rule string
	Fix  string
	// Deps are the probes whose failure would explain this one's.
	Deps []string
	// Every is how often it runs. MinGap is the floor for a manual refresh,
	// for probes that call something slow or rate-limited.
	Every  time.Duration
	MinGap time.Duration
	Run    func(ctx context.Context) Verdict
}

// Result is a probe's latest verdict as served.
type Result struct {
	ID            string     `json:"id"`
	Group         string     `json:"group"`
	Name          string     `json:"name"`
	State         State      `json:"state"`
	Summary       string     `json:"summary"`
	ObservedAt    *time.Time `json:"observedAt,omitempty"`
	ObservedLabel string     `json:"observedLabel,omitempty"`
	Facts         []string   `json:"facts,omitempty"`
	Rule          string     `json:"rule"`
	Fix           string     `json:"fix,omitempty"`
	CheckedAt     time.Time  `json:"checkedAt"`
	// Since is when the state last changed, within this process's life.
	Since time.Time `json:"since"`
	// Cause names an upstream probe that is also not ok - the likelier
	// explanation, so the screen can say "because of X" instead of listing
	// two independent faults.
	Cause     string `json:"cause,omitempty"`
	CauseName string `json:"causeName,omitempty"`
}

type Runner struct {
	probes []Probe
	groups []string
	log    *slog.Logger

	mu      sync.RWMutex
	results map[string]*Result
	running map[string]bool

	// onChange hears a probe's verdict whenever its state differs from the
	// last one - and its first verdict, with prev Unknown.
	onChange func(p Probe, prev State, v Verdict)
}

// State is a probe's latest state, Unknown before its first run.
func (r *Runner) State(id string) State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state(id)
}

// OnChange registers fn for state changes. Set before Run; fn must not block.
func (r *Runner) OnChange(fn func(p Probe, prev State, v Verdict)) { r.onChange = fn }

func NewRunner(groups []string, probes []Probe, log *slog.Logger) *Runner {
	return &Runner{
		probes:  probes,
		groups:  groups,
		log:     log,
		results: map[string]*Result{},
		running: map[string]bool{},
	}
}

// Run starts one loop per probe and blocks until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, p := range r.probes {
		wg.Add(1)
		go func(p Probe) {
			defer wg.Done()
			r.runOne(ctx, p)
			t := time.NewTicker(p.Every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					r.runOne(ctx, p)
				}
			}
		}(p)
	}
	wg.Wait()
}

// Refresh runs every probe now, except those that ran within their MinGap,
// and waits for them.
func (r *Runner) Refresh(ctx context.Context) {
	var wg sync.WaitGroup
	for _, p := range r.probes {
		r.mu.RLock()
		last := r.results[p.ID]
		r.mu.RUnlock()
		if last != nil && p.MinGap > 0 && time.Since(last.CheckedAt) < p.MinGap {
			continue
		}
		wg.Add(1)
		go func(p Probe) {
			defer wg.Done()
			r.runOne(ctx, p)
		}(p)
	}
	wg.Wait()
}

func (r *Runner) runOne(ctx context.Context, p Probe) {
	r.mu.Lock()
	if r.running[p.ID] {
		r.mu.Unlock()
		return
	}
	r.running[p.ID] = true
	r.mu.Unlock()

	timeout := 20 * time.Second
	if p.MinGap > 0 {
		timeout = 90 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	v := safeRun(cctx, p)
	cancel()

	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running[p.ID] = false
	prev := r.results[p.ID]
	if r.onChange != nil && (prev == nil || prev.State != v.State) {
		was := Unknown
		if prev != nil {
			was = prev.State
		}
		r.onChange(p, was, v)
	}
	since := now
	if prev != nil && prev.State == v.State {
		since = prev.Since
	}
	if prev != nil && prev.State != v.State {
		r.log.Info("상태가 바뀌었습니다", "probe", p.ID, "from", prev.State, "to", v.State, "summary", v.Summary)
	}
	r.results[p.ID] = &Result{
		ID: p.ID, Group: p.Group, Name: p.Name,
		State: v.State, Summary: v.Summary,
		ObservedAt: v.ObservedAt, ObservedLabel: v.ObservedLabel, Facts: v.Facts,
		Rule: p.Rule, Fix: p.Fix,
		CheckedAt: now, Since: since,
	}
}

func safeRun(ctx context.Context, p Probe) (v Verdict) {
	defer func() {
		if rec := recover(); rec != nil {
			v = Verdict{State: Unknown, Summary: "확인 중 오류가 났어요."}
		}
	}()
	return p.Run(ctx)
}

// Snapshot is every probe's latest result in declaration order, with causes
// resolved. Probes that have not finished their first run are left out.
type Snapshot struct {
	Groups  []string  `json:"groups"`
	Results []*Result `json:"results"`
}

func (r *Runner) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	byID := map[string]Probe{}
	for _, p := range r.probes {
		byID[p.ID] = p
	}
	out := make([]*Result, 0, len(r.probes))
	for _, p := range r.probes {
		res, ok := r.results[p.ID]
		if !ok {
			continue
		}
		c := *res
		if c.State == Fail || c.State == Warn {
			if root := r.rootCause(p, byID, map[string]bool{}); root != "" {
				c.Cause, c.CauseName = root, byID[root].Name
			}
		}
		out = append(out, &c)
	}
	return Snapshot{Groups: r.groups, Results: out}
}

// rootCause walks the dependencies down to the deepest probe that is failing
// (or warning) itself: when the database is down, every row should point at
// the database, not at the service in between.
func (r *Runner) rootCause(p Probe, byID map[string]Probe, seen map[string]bool) string {
	deps := append([]string(nil), p.Deps...)
	// Failing deps first: a hard fault upstream explains more than a warning.
	sort.SliceStable(deps, func(i, j int) bool {
		return rank(r.state(deps[i])) > rank(r.state(deps[j]))
	})
	for _, d := range deps {
		if seen[d] {
			continue
		}
		seen[d] = true
		s := r.state(d)
		if s != Fail && s != Warn {
			continue
		}
		if deeper := r.rootCause(byID[d], byID, seen); deeper != "" {
			return deeper
		}
		return d
	}
	return ""
}

func (r *Runner) state(id string) State {
	if res, ok := r.results[id]; ok {
		return res.State
	}
	return Unknown
}

func rank(s State) int {
	switch s {
	case Fail:
		return 2
	case Warn:
		return 1
	}
	return 0
}
