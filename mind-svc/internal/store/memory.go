package store

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// Memory keeps everything in this process: the loop runs on a laptop with
// nothing else, and tests start from nothing.
type Memory struct {
	mu       sync.Mutex
	entities map[string]model.Entity
	changes  []model.Change
	messages map[string]model.Message
	marks    map[string]time.Time
	timers   []model.Timer
	intents  map[string]model.Intent
	cursors  map[string]int64
	facts    map[string]model.Fact
	aliases  map[string]model.Alias
	policy   map[[2]string]model.PolicyStat
	occurs   map[string]model.Occurrence
	links    map[[3]string]model.Link
	things   map[string]string
	cache    map[[2]string]model.Judgement
	patterns map[[4]string]model.Pattern
	notes    map[string]model.Note
	versions map[string][]model.Note
	preds    map[string]model.Prediction
	expects  map[[2]string]model.Expectation
	replies  []model.ReplySituation
	seq      int64
}

func NewMemory() *Memory {
	return &Memory{
		entities: map[string]model.Entity{},
		messages: map[string]model.Message{},
		marks:    map[string]time.Time{},
		intents:  map[string]model.Intent{},
		cursors:  map[string]int64{},
		facts:    map[string]model.Fact{},
		aliases:  map[string]model.Alias{},
		policy:   map[[2]string]model.PolicyStat{},
		occurs:   map[string]model.Occurrence{},
		links:    map[[3]string]model.Link{},
		things:   map[string]string{},
		cache:    map[[2]string]model.Judgement{},
		patterns: map[[4]string]model.Pattern{},
		notes:    map[string]model.Note{},
		versions: map[string][]model.Note{},
		preds:    map[string]model.Prediction{},
		expects:  map[[2]string]model.Expectation{},
	}
}

var _ Store = (*Memory)(nil)

func (m *Memory) next() int64 { m.seq++; return m.seq }

func (m *Memory) Entity(_ context.Context, id string) (model.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entities[id]
	if !ok {
		return model.Entity{}, model.ErrNotFound
	}
	return clone(e), nil
}

func (m *Memory) Entities(_ context.Context, kind, room string, liveOnly bool) ([]model.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Entity{}
	for _, e := range m.entities {
		if (kind == "" || e.Kind == kind) && (room == "" || e.Room == room) && (!liveOnly || e.Live()) {
			out = append(out, clone(e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) PlansOn(_ context.Context, from, to string) ([]model.Entity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Entity{}
	for _, e := range m.entities {
		st := e.State
		last := st.EndDate
		if last == "" {
			last = st.Date
		}
		if e.Kind == model.KindPlan && st.Date != "" && st.Date <= to && last >= from {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) Put(_ context.Context, e model.Entity, c model.Change) (model.Entity, model.Change, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, exists := m.entities[e.ID]
	switch {
	case !exists && e.Version != 0, exists && cur.Version != e.Version:
		return model.Entity{}, model.Change{}, model.ErrConflict
	}
	if exists {
		before := clone(cur).State
		c.Before = &before
		e.CreatedAt = cur.CreatedAt
	}
	e.Version++
	e.UpdatedAt = c.At
	if !exists {
		e.CreatedAt = c.At
	}
	c.ID = m.next()
	c.EntityID = e.ID
	c.Version = e.Version
	c.After = clone(e).State
	m.entities[e.ID] = clone(e)
	m.changes = append(m.changes, c)
	return clone(e), c, nil
}

func (m *Memory) Changes(_ context.Context, entityID string, limit int) ([]model.Change, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Change{}
	for i := len(m.changes) - 1; i >= 0 && len(out) < limit; i-- {
		if entityID == "" || m.changes[i].EntityID == entityID {
			out = append(out, m.changes[i])
		}
	}
	return out, nil
}

func (m *Memory) PutMessage(_ context.Context, msg model.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.messages[msg.Ref]; !ok {
		m.messages[msg.Ref] = msg
	}
	return nil
}

func (m *Memory) Message(_ context.Context, ref string) (model.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.messages[ref]
	if !ok {
		return model.Message{}, model.ErrNotFound
	}
	return msg, nil
}

func (m *Memory) RoomMessages(_ context.Context, room string, after, before time.Time, limit int) ([]model.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Message{}
	for _, msg := range m.messages {
		if msg.Room == room && msg.SentAt.After(after) && (before.IsZero() || msg.SentAt.Before(before)) {
			out = append(out, msg)
		}
	}
	sortMessages(out)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (m *Memory) Rooms(_ context.Context) ([]Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	by := map[string]*Room{}
	for _, msg := range m.messages {
		r := by[msg.Room]
		if r == nil {
			r = &Room{Room: msg.Room, Name: msg.RoomName, Source: msg.Source, OnlyMine: true, Interpreted: m.marks[msg.Room]}
			by[msg.Room] = r
		}
		r.Messages++
		r.OnlyMine = r.OnlyMine && msg.IsMine
		if msg.SentAt.After(r.LastAt) {
			r.LastAt = msg.SentAt
		}
		if msg.SentAt.After(r.Interpreted) {
			r.Uninterpreted++
		}
	}
	out := make([]Room, 0, len(by))
	for _, r := range by {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out, nil
}

func (m *Memory) MarkRoom(_ context.Context, room string, through time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if through.After(m.marks[room]) {
		m.marks[room] = through
	}
	return nil
}

func (m *Memory) SetTimer(_ context.Context, t model.Timer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.timers {
		if x.Skill == t.Skill && x.EntityID == t.EntityID && x.Name == t.Name {
			m.timers[i].FireAt, m.timers[i].FiredAt = t.FireAt, nil
			return nil
		}
	}
	t.ID = m.next()
	t.FiredAt = nil
	m.timers = append(m.timers, t)
	return nil
}

func (m *Memory) CancelTimers(_ context.Context, skill, entityID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timers = slices.DeleteFunc(m.timers, func(t model.Timer) bool { return t.Skill == skill && t.EntityID == entityID })
	return nil
}

func (m *Memory) DueTimers(_ context.Context, now time.Time) ([]model.Timer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Timer{}
	for _, t := range m.timers {
		if t.FiredAt == nil && !t.FireAt.After(now) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FireAt.Before(out[j].FireAt) })
	return out, nil
}

func (m *Memory) FireTimer(_ context.Context, id int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.timers {
		if m.timers[i].ID == id {
			m.timers[i].FiredAt = &at
		}
	}
	return nil
}

func (m *Memory) Timers(_ context.Context) ([]model.Timer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.timers)
	sort.Slice(out, func(i, j int) bool { return out[i].FireAt.Before(out[j].FireAt) })
	return out, nil
}

func (m *Memory) PutIntent(_ context.Context, in model.Intent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.intents[in.ID] = in
	return nil
}

func (m *Memory) Intent(_ context.Context, id string) (model.Intent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.intents[id]
	if !ok {
		return model.Intent{}, model.ErrNotFound
	}
	return in, nil
}

func (m *Memory) Intents(_ context.Context, limit int) ([]model.Intent, error) {
	return m.filter(func(model.Intent) bool { return true }, limit), nil
}

func (m *Memory) IntentsFor(_ context.Context, entityID string) ([]model.Intent, error) {
	return m.filter(func(in model.Intent) bool { return in.EntityID == entityID }, 1000), nil
}

func (m *Memory) IntentsSince(_ context.Context, t time.Time) ([]model.Intent, error) {
	return m.filter(func(in model.Intent) bool { return !in.CreatedAt.Before(t) }, 10000), nil
}

func (m *Memory) filter(keep func(model.Intent) bool, limit int) []model.Intent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Intent{}
	for _, in := range m.intents {
		if keep(in) {
			out = append(out, in)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *Memory) Cursor(_ context.Context, name string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cursors[name], nil
}

func (m *Memory) SetCursor(_ context.Context, name string, v int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cursors[name] = v
	return nil
}

func (m *Memory) PutFact(_ context.Context, f model.Fact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f.Evidence = slices.Clone(f.Evidence)
	m.facts[f.ID] = f
	return nil
}

func (m *Memory) Fact(_ context.Context, id string) (model.Fact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.facts[id]
	if !ok {
		return model.Fact{}, model.ErrNotFound
	}
	return f, nil
}

func (m *Memory) Facts(_ context.Context, limit int) ([]model.Fact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.Fact, 0, len(m.facts))
	for _, f := range m.facts {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) DueFacts(_ context.Context, now time.Time, limit int) ([]model.Fact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Fact{}
	for _, f := range m.facts {
		if f.Status == model.FactPending && !f.NextTry.After(now) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) PutAlias(_ context.Context, a model.Alias) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.aliases[a.Name]
	a.Seen = 1
	if ok {
		a.Seen = cur.Seen + 1
		if a.Kind == "" {
			a.Kind = cur.Kind
		}
	}
	m.aliases[a.Name] = a
	return nil
}

func (m *Memory) Aliases(_ context.Context) ([]model.Alias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.Alias, 0, len(m.aliases))
	for _, a := range m.aliases {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func clone(e model.Entity) model.Entity {
	e.State.People = slices.Clone(e.State.People)
	return e
}

func sortMessages(ms []model.Message) {
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].SentAt.Equal(ms[j].SentAt) {
			return ms[i].Ref < ms[j].Ref
		}
		return ms[i].SentAt.Before(ms[j].SentAt)
	})
}

func (m *Memory) Policy(_ context.Context, skill, context string) (model.PolicyStat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.policy[[2]string{skill, context}]
	if !ok {
		return model.PolicyStat{Skill: skill, Context: context}, nil
	}
	return p, nil
}

func (m *Memory) Policies(_ context.Context) ([]model.PolicyStat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.PolicyStat, 0, len(m.policy))
	for _, p := range m.policy {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Skill+out[i].Context < out[j].Skill+out[j].Context })
	return out, nil
}

func (m *Memory) CountPolicy(_ context.Context, skill, context, outcome string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := [2]string{skill, context}
	p, ok := m.policy[k]
	if !ok {
		p = model.PolicyStat{Skill: skill, Context: context}
	}
	switch outcome {
	case model.OutcomeShown:
		p.Shown++
	case model.OutcomeApproved:
		p.Approved++
		p.Last = outcome
	case model.OutcomeRejected:
		p.Rejected++
		p.Last = outcome
	case model.OutcomeUndone:
		p.Undone++
		p.Last = outcome
	}
	p.UpdatedAt = at
	m.policy[k] = p
	return nil
}

func (m *Memory) ResetPolicy(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = map[[2]string]model.PolicyStat{}
	return nil
}

func (m *Memory) PutOccurrence(_ context.Context, o model.Occurrence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o.People, o.Things = slices.Clone(o.People), slices.Clone(o.Things)
	m.occurs[o.ID] = o
	return nil
}

func (m *Memory) Occurrence(_ context.Context, id string) (model.Occurrence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.occurs[id]
	if !ok {
		return model.Occurrence{}, model.ErrNotFound
	}
	return o, nil
}

func (m *Memory) Occurrences(_ context.Context, from, to time.Time, kinds ...string) ([]model.Occurrence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Occurrence{}
	for _, o := range m.occurs {
		if o.At.Before(from) || !o.At.Before(to) || (len(kinds) > 0 && !slices.Contains(kinds, o.Kind)) {
			continue
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].ID < out[j].ID
		}
		return out[i].At.Before(out[j].At)
	})
	return out, nil
}

func (m *Memory) PutLink(_ context.Context, l model.Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l.Evidence = slices.Clone(l.Evidence)
	m.links[[3]string{l.From, l.To, l.Rel}] = l
	return nil
}

func (m *Memory) LinksOf(_ context.Context, id string) ([]model.Link, error) {
	return m.linksWhere(func(l model.Link) bool { return l.From == id || l.To == id }), nil
}

func (m *Memory) LinksSince(_ context.Context, t time.Time) ([]model.Link, error) {
	return m.linksWhere(func(l model.Link) bool { return !l.UpdatedAt.Before(t) }), nil
}

func (m *Memory) linksWhere(keep func(model.Link) bool) []model.Link {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Link{}
	for _, l := range m.links {
		if keep(l) {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].From+out[i].To+out[i].Rel < out[j].From+out[j].To+out[j].Rel
		}
		return out[i].UpdatedAt.Before(out[j].UpdatedAt)
	})
	return out
}

func (m *Memory) ThingClass(_ context.Context, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.things[name]
	if !ok {
		return "", model.ErrNotFound
	}
	return c, nil
}

func (m *Memory) PutThingClass(_ context.Context, name, class string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.things[name] = class
	return nil
}

func (m *Memory) LinkCache(_ context.Context, class, planKind string) (model.Judgement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.cache[[2]string{class, planKind}]
	if !ok {
		return model.Judgement{}, model.ErrNotFound
	}
	return j, nil
}

func (m *Memory) PutLinkCache(_ context.Context, planKind string, j model.Judgement) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache[[2]string{j.Class, planKind}] = j
	return nil
}

func (m *Memory) CountPattern(_ context.Context, p model.Pattern) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := [4]string{p.KindA, p.Rel, p.KindB, p.Bucket}
	cur := m.patterns[k]
	cur.KindA, cur.Rel, cur.KindB, cur.Bucket = p.KindA, p.Rel, p.KindB, p.Bucket
	cur.Support += p.Support
	cur.Total += p.Total
	if p.LastSeen.After(cur.LastSeen) {
		cur.LastSeen = p.LastSeen
	}
	m.patterns[k] = cur
	return nil
}

func (m *Memory) Patterns(_ context.Context, kindA string) ([]model.Pattern, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Pattern{}
	for _, p := range m.patterns {
		if kindA == "" || p.KindA == kindA {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.KindA+a.Rel+a.KindB+a.Bucket < b.KindA+b.Rel+b.KindB+b.Bucket
	})
	return out, nil
}

func (m *Memory) ResetPatterns(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.patterns = map[[4]string]model.Pattern{}
	return nil
}

func (m *Memory) PutNote(_ context.Context, n model.Note) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n.Evidence = slices.Clone(n.Evidence)
	m.notes[n.ID] = n
	m.versions[n.ID] = append(m.versions[n.ID], n)
	return nil
}

func (m *Memory) Note(_ context.Context, id string) (model.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.notes[id]
	if !ok {
		return model.Note{}, model.ErrNotFound
	}
	return n, nil
}

func (m *Memory) Notes(_ context.Context) ([]model.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Note{}
	for _, n := range m.notes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *Memory) NoteVersions(_ context.Context, id string) ([]model.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.versions[id]), nil
}

func (m *Memory) PutPrediction(_ context.Context, p model.Prediction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.preds[p.ID] = p
	return nil
}

func (m *Memory) Prediction(_ context.Context, id string) (model.Prediction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.preds[id]
	if !ok {
		return model.Prediction{}, model.ErrNotFound
	}
	return p, nil
}

func (m *Memory) Predictions(_ context.Context, from, to time.Time) ([]model.Prediction, error) {
	return m.predsWhere(func(p model.Prediction) bool { return !p.Due.Before(from) && p.Due.Before(to) }), nil
}

func (m *Memory) DuePredictions(_ context.Context, now time.Time) ([]model.Prediction, error) {
	return m.predsWhere(func(p model.Prediction) bool { return p.ScoredAt == nil && !p.Due.After(now) }), nil
}

func (m *Memory) predsWhere(keep func(model.Prediction) bool) []model.Prediction {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Prediction{}
	for _, p := range m.preds {
		if keep(p) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Due.Before(out[j].Due) || out[i].Due.Equal(out[j].Due) && out[i].ID < out[j].ID
	})
	return out
}

func (m *Memory) PutExpectation(_ context.Context, e model.Expectation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expects[[2]string{e.PredictionID, e.Key}] = e
	return nil
}

func (m *Memory) OpenExpectations(_ context.Context, key string) ([]model.Expectation, error) {
	return m.expectsWhere(func(e model.Expectation) bool { return e.Key == key && e.State == "open" }), nil
}

func (m *Memory) Expectations(_ context.Context, predictionID string) ([]model.Expectation, error) {
	return m.expectsWhere(func(e model.Expectation) bool { return e.PredictionID == predictionID }), nil
}

func (m *Memory) expectsWhere(keep func(model.Expectation) bool) []model.Expectation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.Expectation{}
	for _, e := range m.expects {
		if keep(e) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PredictionID+out[i].Key < out[j].PredictionID+out[j].Key })
	return out
}

func (m *Memory) AddReplySituation(_ context.Context, r model.ReplySituation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.replies {
		if x.Kind == r.Kind && x.InRef == r.InRef {
			return nil
		}
	}
	m.replies = append(m.replies, r)
	return nil
}

func (m *Memory) AnswerReplySituations(_ context.Context, room, outRef string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.replies {
		if m.replies[i].Room == room && m.replies[i].OutRef == "" {
			m.replies[i].OutRef = outRef
		}
	}
	return nil
}

func (m *Memory) ReplyExamples(_ context.Context, kind string, limit int) ([]model.ReplySituation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.ReplySituation
	for i := len(m.replies) - 1; i >= 0 && len(out) < limit; i-- {
		if r := m.replies[i]; r.Kind == kind && r.OutRef != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *Memory) ResetReplySituations(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replies = nil
	return nil
}
