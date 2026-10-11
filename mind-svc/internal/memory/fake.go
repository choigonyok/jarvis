package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Fake is long-term memory inside this process, for the console on a laptop,
// replays and tests. Graphiti decides by reading; this only matches, which is
// enough to see the loop around it work: a name is one node (the operator is
// "나"), the same sentence again is a duplicate, and a new fact between the
// same two nodes under the same relation retires the one before.
type Fake struct {
	mu    sync.Mutex
	nodes map[string]model.MemoryNode
	facts []fakeFact
	now   func() time.Time
}

type fakeFact struct {
	Hit
	ref string
}

func NewFake(now func() time.Time) *Fake {
	if now == nil {
		now = time.Now
	}
	return &Fake{nodes: map[string]model.MemoryNode{}, now: now}
}

func (f *Fake) node(p model.Party) model.MemoryNode {
	name := p.Name
	if p.Self {
		name = "나"
	}
	if n, ok := f.nodes[name]; ok {
		return n
	}
	n := model.MemoryNode{ID: fmt.Sprintf("node_%d", len(f.nodes)+1), Name: name}
	f.nodes[name] = n
	return n
}

func (f *Fake) Put(_ context.Context, facts []model.Fact) ([]Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Result{}
	for _, in := range facts {
		var r Result
		r.Ref = in.ID
		r.Source, r.Target = f.node(in.Subject), f.node(in.Object)
		dup := -1
		for i, x := range f.facts {
			if x.ref == in.ID || (!x.Ended() && norm(x.Fact) == norm(in.Text)) {
				dup = i
				break
			}
		}
		if dup >= 0 {
			r.Edge.ID, r.Edge.Fact, r.Duplicate = f.facts[dup].ID, f.facts[dup].Fact, true
			out = append(out, r)
			continue
		}
		at := in.ValidAt
		if at == "" {
			at = f.now().Format("2006-01-02")
		}
		for i := range f.facts {
			x := &f.facts[i]
			if !x.Ended() && x.Source.ID == r.Source.ID && x.Target.ID == r.Target.ID && x.Relation == in.Relation {
				x.InvalidAt = at
				r.Ended = append(r.Ended, model.Ended{ID: x.ID, Fact: x.Fact, InvalidAt: at})
			}
		}
		h := Hit{ID: fmt.Sprintf("edge_%d", len(f.facts)+1), Fact: in.Text, Relation: in.Relation, ValidAt: in.ValidAt, Source: r.Source, Target: r.Target}
		f.facts = append(f.facts, fakeFact{Hit: h, ref: in.ID})
		r.Edge.ID, r.Edge.Fact = h.ID, h.Fact
		out = append(out, r)
	}
	return out, nil
}

func (f *Fake) Search(_ context.Context, q, center string, limit int) ([]Hit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	words := strings.Fields(q)
	type scored struct {
		h Hit
		s int
	}
	var all []scored
	for _, x := range f.facts {
		s := 0
		for _, w := range words {
			if len([]rune(w)) >= 2 && strings.Contains(x.Fact, string([]rune(w)[:2])) {
				s++
			}
		}
		if center != "" && (x.Source.ID == center || x.Target.ID == center) {
			s += 2
		}
		if s > 0 {
			all = append(all, scored{x.Hit, s})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].s != all[j].s {
			return all[i].s > all[j].s
		}
		return !all[i].h.Ended() && all[j].h.Ended()
	})
	out := []Hit{}
	for _, x := range all {
		if len(out) == limit {
			break
		}
		out = append(out, x.h)
	}
	return out, nil
}

func (f *Fake) Find(_ context.Context, name string, limit int) ([]Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Node{}
	for n, node := range f.nodes {
		if strings.Contains(n, name) || strings.Contains(name, n) {
			out = append(out, Node{ID: node.ID, Name: node.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *Fake) Profile(_ context.Context, id string) (Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var p Profile
	for _, n := range f.nodes {
		if n.ID == id {
			p.ID, p.Name = n.ID, n.Name
		}
	}
	if p.ID == "" {
		return p, model.ErrNotFound
	}
	for i := len(f.facts) - 1; i >= 0; i-- {
		x := f.facts[i]
		if x.Source.ID != id && x.Target.ID != id {
			continue
		}
		other := x.Target.Name
		if x.Target.ID == id {
			other = x.Source.Name
		}
		p.Facts = append(p.Facts, ProfileFact{x.ID, x.Fact, x.Relation, other, x.ValidAt, x.Ended()})
	}
	return p, nil
}

func norm(s string) string { return strings.Join(strings.Fields(s), "") }
