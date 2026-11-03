package mind

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/self"
)

// Rules are what the operator told the assistant to keep doing, or not to:
// "렌터카 대여는 일정에 넣지 마", "답에 링크는 붙이지 마". Said once in the
// conversation, they used to last as long as its session; a card turned down
// with a reason reached mind only from 11/03, and a word to the conversation
// never did. Kept here as notes of their own scope (model.RuleScope) -
// confirmed when said, nothing to predict from - they are read by the
// conversation in front of its turns, by the interpreter reading any room, and
// by the jobs. The 생활 screen shows them with the other notes, where one is
// taken back or reworded (the notes' own door).

// maxRules is how many rules may stand at once: every turn carries them.
const maxRules = 30

// Rule text bounds, in characters.
const (
	minRuleRunes = 4
	maxRuleRunes = 200
)

var errRule = errors.New("규칙으로 남길 수 없습니다")

// BadRule is whether a rule was turned down for what it said.
func BadRule(err error) bool { return errors.Is(err, errRule) }

// Rules is the rules standing now, the oldest first.
func (m *Mind) Rules(ctx context.Context) ([]model.Note, error) {
	notes, err := m.Opts.Store.Notes(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.Note
	for _, n := range notes {
		if n.Scope == model.RuleScope && n.Status == model.Confirmed {
			out = append(out, n)
		}
	}
	slices.SortStableFunc(out, func(a, b model.Note) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

// RuleLines is the rules as lines, for whoever reads them in front of its
// work.
func (m *Mind) RuleLines(ctx context.Context) ([]string, error) {
	rules, err := m.Rules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Text)
	}
	return out, nil
}

// SetRule keeps a rule the operator gave, in their words as the conversation
// put them; said is what they said, kept as why. The same rule said again is
// the same note - one taken back stands again. The operator hears of a new
// one: a rule is followed by everything that reads it, and the conversation
// that set it may have been talked into it by a page it read.
func (m *Mind) SetRule(ctx context.Context, text, said string) (model.Note, bool, error) {
	text = strings.Join(strings.Fields(text), " ")
	said = strings.TrimSpace(said)
	switch n := utf8.RuneCountInString(text); {
	case n < minRuleRunes:
		return model.Note{}, false, fmt.Errorf("%w: 규칙이 비어 있거나 너무 짧습니다", errRule)
	case n > maxRuleRunes:
		return model.Note{}, false, fmt.Errorf("%w: 규칙은 %d자까지입니다", errRule, maxRuleRunes)
	}
	now := m.Opts.Now()
	id := self.ID("rule", text)
	reason := "운영자가 대화에서 정함"
	if said != "" {
		reason += ": 「" + clip(said, 120) + "」"
	}
	old, err := m.Opts.Store.Note(ctx, id)
	switch {
	case err == nil && old.Status == model.Confirmed:
		return old, false, nil
	case err == nil:
		old.Status, old.Version, old.Reason, old.UpdatedAt, old.Confidence = model.Confirmed, old.Version+1, reason, now, 1
		if err := m.Opts.Store.PutNote(ctx, old); err != nil {
			return model.Note{}, false, err
		}
		m.toldRule(ctx, old)
		return old, true, nil
	case !errors.Is(err, model.ErrNotFound):
		return model.Note{}, false, err
	}
	standing, err := m.Rules(ctx)
	if err != nil {
		return model.Note{}, false, err
	}
	if len(standing) >= maxRules {
		return model.Note{}, false, fmt.Errorf("%w: 규칙이 이미 %d개입니다. 안 쓰는 규칙부터 지워 주세요", errRule, maxRules)
	}
	n := model.Note{ID: id, Text: text, Scope: model.RuleScope, Status: model.Confirmed, Confidence: 1, Version: 1,
		Reason: reason, CreatedAt: now, UpdatedAt: now}
	if err := m.Opts.Store.PutNote(ctx, n); err != nil {
		return model.Note{}, false, err
	}
	m.toldRule(ctx, n)
	return n, true, nil
}

// toldRule tells the operator a rule now stands, with where to take it back.
func (m *Mind) toldRule(ctx context.Context, n model.Note) {
	if m.Opts.Notifier == nil {
		return
	}
	m.Opts.Notifier.Notify(ctx, act.Notice{
		Kind: "memory.rule", Tier: "now", Level: "info",
		Title: "자비스가 지킬 규칙을 남겼어요", Body: n.Text + " - 아니면 생활 탭에서 거두세요.",
		URL: "/life?focus=note:" + n.ID, Key: "rule:" + n.ID + ":" + fmt.Sprint(n.Version), At: m.Opts.Now(),
	})
}
