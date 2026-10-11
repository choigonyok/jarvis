// Package gate is the one door between the assistant and the person. Every
// intent from every skill passes here, and here alone is decided whether it
// is done quietly, done and then told (with a way to undo), asked first,
// kept for the evening's summary, or dropped.
//
// What decides it: how much each kind of action is trusted - learned from
// what the person approved, rejected and undid - how sure the skill was,
// whether the action can be taken back, and how many times today the person
// has already been interrupted.
package gate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/obs"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// Trust levels of a kind of action.
const (
	Auto   = "auto"   // done quietly
	Tell   = "notify" // done, then told
	AskFor = "ask"    // asked first
	Never  = "never"
)

type Policy struct {
	// Levels is where each kind of action starts. Unknown kinds are asked.
	Levels map[string]string
	// Fixed kinds never move from their level, whatever the history.
	Fixed map[string]bool
	// Promote: this many approvals in a row (none rejected or undone in
	// between) and an asked kind is done-and-told instead.
	Promote int
	// Window is how far back approvals and rejections count.
	Window time.Duration
	// DailyInterrupts is how many pushes a day the gate allows itself; past
	// it, what is not urgent waits for the evening.
	DailyInterrupts int
	// MinConfidence: an action a skill is less sure of than this is asked,
	// and quietly.
	MinConfidence float64
}

func DefaultPolicy() Policy {
	return Policy{
		Levels:          map[string]string{"calendar.create": AskFor, "calendar.update": AskFor, "calendar.delete": AskFor},
		Fixed:           map[string]bool{"calendar.delete": true},
		Promote:         3,
		Window:          30 * 24 * time.Hour,
		DailyInterrupts: 8,
		MinConfidence:   0.7,
	}
}

type Gate struct {
	st     store.Store
	acts   *act.Registry
	notify act.Notifier
	log    obs.Log
	policy Policy
	loc    *time.Location
	now    func() time.Time
	url    string // where a notice links to
	out    *slog.Logger

	mu sync.Mutex
}

func New(st store.Store, acts *act.Registry, n act.Notifier, log obs.Log, p Policy, loc *time.Location, now func() time.Time, url string, out *slog.Logger) *Gate {
	return &Gate{st: st, acts: acts, notify: n, log: log, policy: p, loc: loc, now: now, url: url, out: out}
}

// Submit takes an intent from a skill and does with it what the policy says.
func (g *Gate) Submit(ctx context.Context, in model.Intent) (model.Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	in.ID = newID()
	in.CreatedAt = g.now()
	in.Status = model.Pending
	if in.Evidence == nil {
		in.Evidence = []model.Evidence{}
	}
	// A newer intent about the same thing replaces a question still open:
	// the plan moved again before anyone answered.
	if in.EntityID != "" {
		if err := g.withdraw(ctx, in.EntityID, in.Skill, "새 판단으로 대체됨"); err != nil {
			return in, err
		}
	}

	if in.Kind == "info" {
		in.Decision, in.Status, in.DecidedBy = model.Digest, model.Executed, "policy"
		in.Tier, in.Why = "digest", "알려 둘 일"
		if in.Urgent {
			in.Decision, in.Tier, in.Why = model.Notify, "now", "지금 알 일"
			if g.overBudget(ctx) {
				in.Decision, in.Tier, in.Why = model.Digest, "digest", "오늘 알림 한도를 넘어 저녁 요약으로"
			}
		}
		at := g.now()
		in.DecidedAt, in.ExecutedAt = &at, &at
		g.tell(ctx, in, in.Title, in.Body)
		return in, g.st.PutIntent(ctx, in)
	}

	a, ok := g.acts.For(in.Kind)
	if !ok {
		in.Status, in.Error = model.Failed, "이 행동을 할 수 있는 곳이 없습니다"
		return in, g.st.PutIntent(ctx, in)
	}
	preview, err := a.Preview(ctx, in.Kind, in.Payload)
	if err != nil {
		in.Status, in.Error = model.Failed, "미리보기 실패: "+err.Error()
		return in, g.st.PutIntent(ctx, in)
	}
	in.Preview = preview

	decision, why, err := g.decide(ctx, in)
	if err != nil {
		return in, err
	}
	in.Decision, in.Why = decision, why
	switch decision {
	case model.Execute, model.Notify:
		g.run(ctx, a, &in, "policy")
		if decision == model.Notify && in.Status == model.Executed {
			in.Tier = "now"
			if g.overBudget(ctx) {
				in.Tier = "digest"
			}
			g.tell(ctx, in, "처리했어요", in.Title+"\n되돌리려면 눌러서 확인하세요")
		}
	case model.Ask:
		in.Tier = "now"
		if in.Confidence < g.policy.MinConfidence || g.overBudget(ctx) {
			in.Tier = "digest"
		}
		g.tell(ctx, in, "확인해 주세요", in.Title+"\n"+in.Reason)
	case model.Drop:
		in.Status = model.Dropped
	}
	return in, g.st.PutIntent(ctx, in)
}

// Approve does an intent that was asked.
func (g *Gate) Approve(ctx context.Context, id string) (model.Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	in, err := g.pending(ctx, id)
	if err != nil {
		return in, err
	}
	a, ok := g.acts.For(in.Kind)
	if !ok {
		return in, errors.New("이 행동을 할 수 있는 곳이 없습니다")
	}
	g.run(ctx, a, &in, "user")
	return in, g.st.PutIntent(ctx, in)
}

// Reject turns down an intent that was asked.
func (g *Gate) Reject(ctx context.Context, id string) (model.Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	in, err := g.pending(ctx, id)
	if err != nil {
		return in, err
	}
	at := g.now()
	in.Status, in.DecidedBy, in.DecidedAt = model.Rejected, "user", &at
	return in, g.st.PutIntent(ctx, in)
}

// Undo takes back something done. The result is fed back as an observation,
// so the world model learns the entry is gone.
func (g *Gate) Undo(ctx context.Context, id string) (model.Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	in, err := g.st.Intent(ctx, id)
	if err != nil {
		return in, err
	}
	if in.Status != model.Executed || len(in.Undo) == 0 {
		return in, errors.New("되돌릴 수 있는 상태가 아닙니다")
	}
	a, ok := g.acts.For(in.Kind)
	if !ok {
		return in, errors.New("이 행동을 할 수 있는 곳이 없습니다")
	}
	res, err := a.Undo(ctx, in.Kind, in.Undo)
	if err != nil {
		return in, fmt.Errorf("되돌리지 못했습니다: %w", err)
	}
	in.Status = model.Undone
	g.observe(ctx, obs.ActionUndone, in, res)
	return in, g.st.PutIntent(ctx, in)
}

// Withdraw drops a skill's open questions about an entity.
func (g *Gate) Withdraw(ctx context.Context, entityID, skill string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.withdraw(ctx, entityID, skill, "더 이상 필요 없음")
}

func (g *Gate) withdraw(ctx context.Context, entityID, skill, why string) error {
	open, err := g.st.IntentsFor(ctx, entityID)
	if err != nil {
		return err
	}
	for _, p := range open {
		if p.Status == model.Pending && p.Skill == skill {
			p.Status, p.Error = model.Superseded, why
			if err := g.st.PutIntent(ctx, p); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *Gate) pending(ctx context.Context, id string) (model.Intent, error) {
	in, err := g.st.Intent(ctx, id)
	if err != nil {
		return in, err
	}
	if in.Status != model.Pending {
		return in, fmt.Errorf("이미 처리된 제안입니다 (%s)", in.Status)
	}
	return in, nil
}

func (g *Gate) run(ctx context.Context, a act.Actuator, in *model.Intent, by string) {
	at := g.now()
	in.DecidedBy, in.DecidedAt = by, &at
	res, undo, err := a.Execute(ctx, in.Kind, in.Payload)
	if err != nil {
		in.Status, in.Error = model.Failed, err.Error()
		g.out.Warn("행동을 하지 못했습니다", "kind", in.Kind, "err", err)
		return
	}
	in.Status, in.Result, in.Undo, in.ExecutedAt = model.Executed, res, undo, &at
	g.observe(ctx, obs.ActionExecuted, *in, res)
}

func (g *Gate) observe(ctx context.Context, typ string, in model.Intent, res json.RawMessage) {
	data, _ := json.Marshal(obs.ActionData{IntentID: in.ID, Kind: in.Kind, EntityID: in.EntityID, Result: res})
	err := g.log.Append(ctx, obs.Observation{
		Source: "mind", Type: typ, Subject: in.EntityID, Data: data,
		Key: typ + ":" + in.ID, OccurredAt: g.now(),
	})
	if err != nil {
		g.out.Warn("행동 결과를 기록하지 못했습니다", "err", err)
	}
}

func (g *Gate) tell(ctx context.Context, in model.Intent, title, body string) {
	if g.notify == nil || in.Tier == "" {
		return
	}
	level := "info"
	if in.Tier == "now" {
		level = "warn"
	}
	g.notify.Notify(ctx, act.Notice{
		Title: title, Body: strings.TrimSpace(body), URL: g.url + "#" + in.ID,
		Tier: in.Tier, Level: level, Key: "mind:" + in.ID, At: g.now(),
	})
}

// decide is the policy applied to one action.
func (g *Gate) decide(ctx context.Context, in model.Intent) (string, string, error) {
	history, err := g.history(ctx, in.Kind)
	if err != nil {
		return "", "", err
	}
	level, why := g.policy.Level(in.Kind, history)
	if level == Never {
		return model.Drop, why, nil
	}
	if in.Confidence < g.policy.MinConfidence {
		return model.Ask, fmt.Sprintf("확신도 %.0f%%라 직접 확인받아요", in.Confidence*100), nil
	}
	if !in.Reversible && level != AskFor {
		return model.Ask, "되돌릴 수 없는 일이라 먼저 물어요", nil
	}
	switch level {
	case Auto:
		return model.Execute, why, nil
	case Tell:
		return model.Notify, why, nil
	}
	return model.Ask, why, nil
}

// history is this kind's decided intents within the window, newest first.
func (g *Gate) history(ctx context.Context, kind string) ([]model.Intent, error) {
	all, err := g.st.IntentsSince(ctx, g.now().Add(-g.policy.Window))
	if err != nil {
		return nil, err
	}
	var out []model.Intent
	for _, in := range all {
		if in.Kind == kind {
			out = append(out, in)
		}
	}
	return out, nil
}

// Level is where a kind of action stands now, given its history (newest
// first), and why.
func (p Policy) Level(kind string, history []model.Intent) (string, string) {
	base, ok := p.Levels[kind]
	if !ok {
		base = AskFor
	}
	if p.Fixed[kind] {
		return base, "항상 먼저 묻는 행동"
	}
	streak := 0
	for _, h := range history {
		if h.Status == model.Rejected || h.Status == model.Undone {
			if streak == 0 && base != AskFor {
				return AskFor, "최근에 거절하거나 되돌리셔서 다시 물어요"
			}
			break
		}
		if h.Status == model.Executed {
			streak++
		}
	}
	if base == AskFor && p.Promote > 0 && streak >= p.Promote {
		return Tell, fmt.Sprintf("최근 %d번 연속 승인하셔서 바로 하고 알려요", streak)
	}
	if base == AskFor {
		return AskFor, "먼저 물어보는 행동"
	}
	return base, "믿고 맡긴 행동"
}

// Levels is where every known kind stands, for the console.
func (g *Gate) Levels(ctx context.Context) (map[string][2]string, error) {
	out := map[string][2]string{}
	for kind := range g.policy.Levels {
		h, err := g.history(ctx, kind)
		if err != nil {
			return nil, err
		}
		l, why := g.policy.Level(kind, h)
		out[kind] = [2]string{l, why}
	}
	return out, nil
}

// overBudget is whether today's pushes are used up.
func (g *Gate) overBudget(ctx context.Context) bool {
	if g.policy.DailyInterrupts <= 0 {
		return false
	}
	now := g.now().In(g.loc)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, g.loc)
	today, err := g.st.IntentsSince(ctx, midnight)
	if err != nil {
		return false
	}
	n := 0
	for _, in := range today {
		if in.Tier == "now" {
			n++
		}
	}
	return n >= g.policy.DailyInterrupts
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "int_" + hex.EncodeToString(b)
}
