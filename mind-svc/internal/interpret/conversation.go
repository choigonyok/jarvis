// Package interpret turns observations into changes to the world model.
//
// Conversations are read a stretch at a time, not a message at a time: "강릉
// 갈까?" alone means little until "그러자!" follows. A room's new messages
// are held until it falls quiet (or grows long), then read in one go along
// with what is already known about that room - its plans and loops - so
// "춘천으로 가자" is read as the existing trip moving, not a new one.
//
// Nothing here matches words. A model reads the conversation and proposes
// changes, each with the line it rests on quoted; a change whose quote is not
// in that line is not believed. The same goes for facts worth remembering
// (what someone likes, who is whose friend): believed ones are written to the
// evidence ledger, which sends them on to long-term memory.
package interpret

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
	"github.com/choigonyok/jarvis/mind-svc/internal/world"
)

// Config is how long a room must be quiet before it is read.
type Config struct {
	Owner    string        // the operator's name, for "나"
	Idle     time.Duration // a conversation with someone
	IdleSelf time.Duration // a notes-to-self room: nobody is going to answer
	IdleSim  time.Duration // the simulator
	MaxBatch int           // read at once past this many waiting messages
	Context  int           // earlier messages given as context
	MinConf  float64       // changes below this are not applied
	// Noise is rooms not worth reading at all: alerts, ads, banks.
	Noise *regexp.Regexp
	// Recall is what long-term memory already holds about a room's people,
	// shown to the model as context (nil: nothing is shown).
	Recall func(ctx context.Context, room, roomName string, fresh []model.Message) []string
	// OnFact hears that a fact went into the ledger (to send it soon).
	OnFact func()
}

// KindFact is a fact for long-term memory, beside the world model's kinds.
const KindFact = "fact"

type Conversation struct {
	cfg   Config
	st    store.Store
	world *world.World
	llm   llm.Client
	loc   *time.Location
	now   func() time.Time
	log   *slog.Logger

	mu      sync.Mutex
	due     map[string]time.Time // room -> when to read it
	others  map[string]bool      // room has someone besides me in it
	backoff map[string]time.Time // room -> not before (after a failed read)
	reading sync.Mutex           // one read at a time
}

func NewConversation(cfg Config, st store.Store, w *world.World, client llm.Client, loc *time.Location, now func() time.Time, log *slog.Logger) *Conversation {
	if cfg.MaxBatch == 0 {
		cfg.MaxBatch = 40
	}
	if cfg.Context == 0 {
		cfg.Context = 12
	}
	if cfg.MinConf == 0 {
		cfg.MinConf = 0.5
	}
	if cfg.Owner == "" {
		cfg.Owner = "나"
	}
	return &Conversation{
		cfg: cfg, st: st, world: w, llm: client, loc: loc, now: now, log: log,
		due: map[string]time.Time{}, others: map[string]bool{}, backoff: map[string]time.Time{},
	}
}

// Resume schedules rooms left half-read when the process last stopped.
func (c *Conversation) Resume(ctx context.Context) error {
	rooms, err := c.st.Rooms(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range rooms {
		c.others[r.Room] = !r.OnlyMine
		if r.Uninterpreted > 0 && (c.cfg.Noise == nil || !c.cfg.Noise.MatchString(r.Name)) {
			c.due[r.Room] = c.now()
		}
	}
	return nil
}

// Observe notes a new message: its room is read once it has been quiet long
// enough.
func (c *Conversation) Observe(m model.Message) {
	if c.cfg.Noise != nil && c.cfg.Noise.MatchString(m.RoomName) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !m.IsMine {
		c.others[m.Room] = true
	}
	idle := c.cfg.Idle
	switch {
	case m.Source == "sim":
		idle = c.cfg.IdleSim
	case !c.others[m.Room]:
		idle = c.cfg.IdleSelf
	}
	c.due[m.Room] = c.now().Add(idle)
}

// Run reads rooms as they come due.
func (c *Conversation) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, room := range c.dueRooms() {
				if err := c.Read(ctx, room); err != nil && ctx.Err() == nil {
					c.log.Warn("대화를 해석하지 못했습니다", "room", room, "err", err)
				}
			}
		}
	}
}

func (c *Conversation) dueRooms() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	var out []string
	for room, at := range c.due {
		if !now.Before(at) && !now.Before(c.backoff[room]) {
			out = append(out, room)
		}
	}
	slices.Sort(out)
	return out
}

// Pending is the rooms waiting to be read, for the console.
func (c *Conversation) Pending() map[string]time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]time.Time{}
	for k, v := range c.due {
		out[k] = v
	}
	return out
}

// Read interprets what is new in a room now, whether or not it is quiet.
func (c *Conversation) Read(ctx context.Context, room string) error {
	c.reading.Lock()
	defer c.reading.Unlock()

	marks, err := c.st.Rooms(ctx)
	if err != nil {
		return err
	}
	var through time.Time
	name := room
	for _, r := range marks {
		if r.Room == room {
			through, name = r.Interpreted, r.Name
		}
	}
	fresh, err := c.st.RoomMessages(ctx, room, through, time.Time{}, 200)
	if err != nil {
		return err
	}
	if len(fresh) == 0 {
		c.done(room)
		return nil
	}
	// A long backlog is read in batches, oldest first; the rest is read next.
	more := len(fresh) > c.cfg.MaxBatch
	if more {
		fresh = fresh[:c.cfg.MaxBatch]
	}
	last := fresh[len(fresh)-1].SentAt

	earlier, err := c.st.RoomMessages(ctx, room, time.Time{}, fresh[0].SentAt, c.cfg.Context)
	if err != nil {
		return err
	}
	known, err := c.known(ctx, room)
	if err != nil {
		return err
	}

	var remembered []string
	if c.cfg.Recall != nil {
		remembered = c.cfg.Recall(ctx, room, name, fresh)
	}
	answer, err := c.llm.Complete(ctx, system, prompt(name, c.cfg.Owner, c.now(), c.loc, known, remembered, earlier, fresh))
	if err != nil {
		c.retryLater(room, err)
		return err
	}
	ops, err := parse(answer)
	if err != nil {
		c.retryLater(room, err)
		return fmt.Errorf("해석 결과를 읽지 못했습니다: %w", err)
	}
	applied := 0
	for _, o := range ops {
		ok, err := c.apply(ctx, room, o, fresh, known)
		if err != nil {
			return err
		}
		if ok {
			applied++
		}
	}
	if err := c.st.MarkRoom(ctx, room, last); err != nil {
		return err
	}
	c.log.Info("대화를 해석했습니다", "room", room, "messages", len(fresh), "changes", applied, "proposed", len(ops))
	if more {
		c.mu.Lock()
		c.due[room] = c.now()
		c.mu.Unlock()
		return nil
	}
	c.done(room)
	return nil
}

func (c *Conversation) done(room string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.due, room)
	delete(c.backoff, room)
}

func (c *Conversation) retryLater(room string, err error) {
	wait := time.Minute
	if errors.Is(err, llm.ErrBudget) || errors.Is(err, llm.ErrNoKey) {
		wait = 30 * time.Minute
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.backoff[room] = c.now().Add(wait)
}

// known is what the world model holds for this room, by short ref: live
// plans and loops, and plans that ended recently (so "다시 가기로 했어" can
// revive one instead of starting another).
func (c *Conversation) known(ctx context.Context, room string) ([]view, error) {
	all, err := c.world.Entities(ctx, "", room, false)
	if err != nil {
		return nil, err
	}
	cutoff := c.now().AddDate(0, 0, -14)
	var out []view
	np, nl := 0, 0
	for _, e := range all {
		if !e.Live() && e.UpdatedAt.Before(cutoff) {
			continue
		}
		switch e.Kind {
		case model.KindPlan:
			np++
			out = append(out, view{ref: fmt.Sprintf("P%d", np), entity: e})
		case model.KindLoop:
			nl++
			out = append(out, view{ref: fmt.Sprintf("L%d", nl), entity: e})
		}
	}
	return out, nil
}

// op is one change the model proposes.
type op struct {
	Op         string                     `json:"op"`
	Kind       string                     `json:"kind"`
	Ref        string                     `json:"ref"`
	Set        map[string]json.RawMessage `json:"set"`
	Evidence   []cite                     `json:"evidence"`
	Confidence float64                    `json:"confidence"`
	Why        string                     `json:"why"`
}

type cite struct {
	Line  int    `json:"line"`
	Quote string `json:"quote"`
}

func parse(answer string) ([]op, error) {
	start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if start < 0 || end < start {
		return nil, errors.New("JSON 이 없습니다")
	}
	var out struct {
		Changes []op `json:"changes"`
	}
	if err := json.Unmarshal([]byte(answer[start:end+1]), &out); err != nil {
		return nil, err
	}
	return out.Changes, nil
}

// apply checks a proposed change and writes it. A change it does not
// believe - no evidence that holds, a ref that names nothing, too unsure -
// is logged and skipped, not an error.
func (c *Conversation) apply(ctx context.Context, room string, o op, fresh []model.Message, known []view) (bool, error) {
	skip := func(why string) (bool, error) {
		c.log.Info("해석 결과를 버립니다", "room", room, "op", o.Op, "ref", o.Ref, "why", why)
		return false, nil
	}
	evidence := verify(o.Evidence, fresh)
	if len(evidence) == 0 {
		return skip("근거가 새 메시지에 없음")
	}
	if o.Confidence < c.cfg.MinConf {
		return skip(fmt.Sprintf("확신도 %.2f", o.Confidence))
	}
	kind := o.Kind
	if kind == KindFact {
		return c.remember(ctx, room, o, evidence, skip)
	}
	if kind != model.KindPlan && kind != model.KindLoop {
		return skip("모르는 종류 " + kind)
	}
	refs := map[string]string{}
	for _, v := range known {
		refs[v.ref] = v.entity.ID
	}
	meta := world.Meta{Actor: "interpreter", Evidence: evidence, Confidence: o.Confidence, Why: o.Why}

	switch o.Op {
	case "create":
		var s model.State
		if kind == model.KindPlan {
			s.Status = model.Proposed
		} else {
			s.Status, s.Owner = model.Open, "me"
		}
		setAll(&s, kind, o.Set, refs)
		if strings.TrimSpace(s.Title) == "" {
			return skip("제목 없음")
		}
		// The same plan named again is that plan, not a second one.
		if id := c.same(kind, s, known); id != "" {
			_, _, err := c.world.Update(ctx, id, func(cur *model.State) bool { setAll(cur, kind, o.Set, refs); return true }, meta)
			return err == nil, err
		}
		_, err := c.world.Create(ctx, kind, room, s, meta)
		return err == nil, err

	case "update", "cancel", "close":
		id := refs[strings.TrimSpace(o.Ref)]
		if id == "" {
			return skip("없는 ref " + o.Ref)
		}
		_, changed, err := c.world.Update(ctx, id, func(cur *model.State) bool {
			setAll(cur, kind, o.Set, refs)
			switch o.Op {
			case "cancel":
				cur.Status = model.Cancelled
			case "close":
				cur.Status = model.Closed
			}
			return true
		}, meta)
		return changed, err
	}
	return skip("모르는 op " + o.Op)
}

// remember writes a believed fact to the evidence ledger, pending for
// memory. The same sentence already written for this room (a room read again
// after a failure) is not written twice.
func (c *Conversation) remember(ctx context.Context, room string, o op, evidence []model.Evidence, skip func(string) (bool, error)) (bool, error) {
	str := func(k string) string {
		var v string
		if raw, ok := o.Set[k]; ok && json.Unmarshal(raw, &v) == nil {
			return strings.TrimSpace(v)
		}
		return ""
	}
	subject, object, relation, text := str("subject"), str("object"), relationName(str("relation")), str("text")
	if subject == "" || object == "" || relation == "" || text == "" {
		return skip("사실의 두 끝·관계·문장 중 빠진 것이 있음")
	}
	recent, err := c.st.Facts(ctx, 300)
	if err != nil {
		return false, err
	}
	for _, f := range recent {
		if f.Room == room && norm(f.Text) == norm(text) {
			return skip("이미 적은 사실")
		}
	}
	now := c.now()
	f := model.Fact{
		ID: "mf_" + randomHex(6), Room: room, Subject: c.party(subject, str("subjectKind")), Relation: relation,
		Object: c.party(object, str("objectKind")), Text: text, Evidence: evidence, Confidence: o.Confidence, Why: o.Why,
		Status: model.FactPending, NextTry: now, CreatedAt: now,
	}
	if v := str("validAt"); dateRE.MatchString(v) {
		f.ValidAt = v
	}
	if err := c.st.PutFact(ctx, f); err != nil {
		return false, err
	}
	if c.cfg.OnFact != nil {
		c.cfg.OnFact()
	}
	return true, nil
}

// party is one end of a fact: "나" (or the operator's name) is the operator.
func (c *Conversation) party(name, kind string) model.Party {
	if name == "나" || name == c.cfg.Owner || name == c.cfg.Owner+"(나)" {
		return model.Party{Name: "나", Self: true}
	}
	switch kind {
	case "Person", "Place", "Org", "Thing", "Event":
	default:
		kind = "Thing"
	}
	return model.Party{Name: name, Kind: kind}
}

var relationRE = regexp.MustCompile(`[^A-Z0-9_]+`)

// relationName is "DISLIKES" whatever spacing or case the model used.
func relationName(s string) string {
	s = relationRE.ReplaceAllString(strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", "_")), "")
	if len(s) > 60 {
		s = s[:60]
	}
	return strings.Trim(s, "_")
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// same finds a live entity this create is really about: a plan with the
// same title, or on the same day at the same place.
func (c *Conversation) same(kind string, s model.State, known []view) string {
	for _, v := range known {
		e := v.entity
		if e.Kind != kind || !e.Live() {
			continue
		}
		if norm(e.State.Title) == norm(s.Title) {
			return e.ID
		}
		if kind == model.KindPlan && s.Date != "" && e.State.Date == s.Date && s.Place != "" && e.State.Place == s.Place {
			return e.ID
		}
	}
	return ""
}

// verify keeps the evidence that holds: a line among the new messages, and a
// quote that is in it.
func verify(cites []cite, fresh []model.Message) []model.Evidence {
	var out []model.Evidence
	for _, ci := range cites {
		if ci.Line < 1 || ci.Line > len(fresh) {
			continue
		}
		m := fresh[ci.Line-1]
		q := strings.TrimSpace(ci.Quote)
		if q == "" || !strings.Contains(norm(m.Text), norm(q)) {
			continue
		}
		out = append(out, model.Evidence{Ref: m.Ref, Quote: q})
	}
	return out
}

var (
	dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	timeRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	dueRE  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(T([01]\d|2[0-3]):[0-5]\d)?$`)
)

// setAll writes the fields the model set, keeping only values of the right
// shape: a malformed date is dropped, not stored.
func setAll(s *model.State, kind string, set map[string]json.RawMessage, refs map[string]string) {
	str := func(k string) (string, bool) {
		raw, ok := set[k]
		if !ok {
			return "", false
		}
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return "", false
		}
		return strings.TrimSpace(v), true
	}
	if v, ok := str("title"); ok && v != "" {
		s.Title = v
	}
	if v, ok := str("status"); ok && validStatus(kind, v) {
		s.Status = v
	}
	for key, dst := range map[string]*string{"date": &s.Date, "endDate": &s.EndDate} {
		if v, ok := str(key); ok && (v == "" || dateRE.MatchString(v)) {
			*dst = v
		}
	}
	for key, dst := range map[string]*string{"start": &s.Start, "end": &s.End} {
		if v, ok := str(key); ok && (v == "" || timeRE.MatchString(v)) {
			*dst = v
		}
	}
	if v, ok := str("place"); ok {
		s.Place = v
	}
	if v, ok := str("note"); ok {
		s.Note = v
	}
	if raw, ok := set["people"]; ok {
		var people []string
		if json.Unmarshal(raw, &people) == nil {
			s.People = clean(people)
		}
	}
	if kind == model.KindLoop {
		if v, ok := str("owner"); ok && (v == "me" || v == "them") {
			s.Owner = v
		}
		if v, ok := str("due"); ok && (v == "" || dueRE.MatchString(v)) {
			s.Due = v
		}
		if v, ok := str("plan"); ok && refs[v] != "" {
			s.PlanID = refs[v]
		}
	}
}

func validStatus(kind, v string) bool {
	if kind == model.KindPlan {
		return v == model.Proposed || v == model.Confirmed || v == model.Cancelled || v == model.Done
	}
	return v == model.Open || v == model.Closed
}

func clean(people []string) []string {
	out := []string{}
	for _, p := range people {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func norm(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}
