package mind

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/tool"
)

// RuleScope is the scope of the notes that are the operator's rules for the
// assistant (mind-svc model.RuleScope).
const RuleScope = "규칙"

// Rule is one rule as mind keeps it.
type Rule struct {
	ID    string    `json:"id"`
	Text  string    `json:"text"`
	Why   string    `json:"why"`
	Since time.Time `json:"since"`
}

// Rules is the rules standing now, the oldest first.
func (c Client) Rules(ctx context.Context) ([]Rule, error) {
	var out struct {
		Rules []Rule `json:"rules"`
	}
	err := c.Do(ctx, http.MethodGet, "/api/rules", nil, &out)
	return out.Rules, err
}

// RuleLines is the rules as lines, for a job's run: nil when mind could not
// say (the run goes on without them).
func (c Client) RuleLines(ctx context.Context) []string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rules, err := c.Rules(ctx)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Text)
	}
	return out
}

type ruleSetIn struct {
	Text string `json:"text" jsonschema:"앞으로 지킬 것을 한 문장으로, 무엇을 할지·하지 말지가 드러나게(예: '렌터카 대여는 일정 카드로 올리지 않는다')"`
	Said string `json:"said,omitempty" jsonschema:"운영자가 그렇게 말한 문장 그대로"`
}

type ruleEndIn struct {
	ID  string `json:"id" jsonschema:"규칙 id (look 의 rules)"`
	Why string `json:"why,omitempty" jsonschema:"운영자가 거두며 한 말"`
}

// LookRules puts the rules standing on the board.
func (m *Module) LookRules(b *tool.Board) {
	tool.Add(b, "rules", "운영자가 정한 규칙(비서가 늘 지킬 것)을 id 와 함께 준다. 지금 규칙은 메시지 앞에도 붙는다.",
		func(ctx context.Context, _ struct{}) (string, error) {
			rules, err := m.c.Rules(ctx)
			if err != nil {
				return "", err
			}
			if len(rules) == 0 {
				return "정한 규칙이 없습니다.", nil
			}
			var s strings.Builder
			for _, r := range rules {
				fmt.Fprintf(&s, "- %s (id %s, %s부터 · %s)\n", r.Text, r.ID, r.Since.In(time.Local).Format("2006-01-02"), r.Why)
			}
			return s.String(), nil
		})
}

// Change puts keeping and taking back a rule on the board: no card - the
// operator hears of a new rule on the phone, and takes one back on the 생활
// screen or by saying so.
func (m *Module) Change(b *tool.Board) {
	tool.Add(b, "rule.set", "운영자가 앞으로 늘 지킬 것을 정하면(답하는 방식, 카드·알림을 올릴 때와 안 올릴 때, 일정으로 넣을 것과 안 넣을 것) 규칙으로 남긴다. 대화·mind 의 해석·작업이 모두 따른다.",
		func(ctx context.Context, in ruleSetIn) (string, error) {
			var out struct {
				Rule struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				} `json:"rule"`
				New bool `json:"new"`
			}
			if err := m.c.Do(ctx, http.MethodPost, "/api/rules", in, &out); err != nil {
				return "", err
			}
			if !out.New {
				return fmt.Sprintf("이미 있는 규칙입니다: %s (id %s)", out.Rule.Text, out.Rule.ID), nil
			}
			return fmt.Sprintf("규칙으로 남겼습니다: %s (id %s). 운영자 폰에 알림이 갑니다.", out.Rule.Text, out.Rule.ID), nil
		})
	tool.Add(b, "rule.end", "운영자가 그만 지켜도 된다고 한 규칙을 거둔다.",
		func(ctx context.Context, in ruleEndIn) (string, error) {
			// A rule only: the other notes are mind's own findings, which the
			// operator answers on the 생활 screen.
			rules, err := m.c.Rules(ctx)
			if err != nil {
				return "", err
			}
			found := false
			for _, r := range rules {
				found = found || r.ID == in.ID
			}
			if !found {
				return "", errors.New("그런 규칙이 없습니다. id 는 look 의 rules 에 있습니다")
			}
			reason := strings.TrimSpace(in.Why)
			if reason == "" {
				reason = "대화에서 거둠"
			}
			body := map[string]string{"status": "rejected", "reason": reason}
			if err := m.c.Do(ctx, http.MethodPost, "/api/notes/"+url.PathEscape(in.ID), body, nil); err != nil {
				return "", err
			}
			return "규칙을 거뒀습니다.", nil
		})
}
