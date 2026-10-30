package mind

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/engine"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/slot"
)

// How charges the book's rules could not place are filed. The classifier says
// what a charge probably was, the engine says how far that goes - asked with
// the form empty, asked with the answer filled in, or done - and the gate
// carries it out, like any other thing the assistant wants to do.

// chargeCards is how many of these cards may wait at once: a backlog comes a
// few at a time, not as a wall of cards.
const chargeCards = 3

// chargeEvery is how often the unfiled charges are looked at.
const chargeEvery = 10 * time.Minute

// fileCharges looks at what the rules left in 기타.
func (m *Mind) fileCharges(ctx context.Context) error {
	if m.Opts.Spending == nil {
		return nil
	}
	var list struct {
		Entries    []slot.Charge `json:"entries"`
		Categories []string      `json:"categories"`
	}
	if err := m.Opts.Spending.Unclear(ctx, &list); err != nil {
		return err
	}
	if len(list.Entries) == 0 {
		return nil
	}
	st := m.Opts.Store
	recent, err := st.IntentsSince(ctx, m.Opts.Now().AddDate(0, 0, -90))
	if err != nil {
		return err
	}
	open, seen := 0, map[string]bool{}
	for _, in := range recent {
		if in.Kind != act.KindCategory {
			continue
		}
		seen[in.Topic] = true
		if in.Status == model.Pending {
			open++
		}
	}
	var fresh []slot.Charge
	for _, c := range list.Entries {
		if !seen[chargeTopic(c)] {
			fresh = append(fresh, c)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	classifier := slot.Classifier{St: st, LLM: m.Opts.LLM, Now: m.Opts.Now}
	classes := classifier.Classes(ctx, fresh, list.Categories)
	for _, c := range fresh {
		in, ask := m.chargeIntent(c, classes[c.Key()], list.Categories)
		if ask && open >= chargeCards {
			continue
		}
		if _, err := m.Gate.Submit(ctx, in); err != nil {
			return err
		}
		if ask {
			open++
		}
	}
	return nil
}

func chargeTopic(c slot.Charge) string { return "category:" + c.Ref() }

// chargeIntent is what the engine makes of one charge, and whether it takes a
// place among the waiting cards.
func (m *Mind) chargeIntent(c slot.Charge, class model.Class, categories []string) (model.Intent, bool) {
	def := slot.MerchantCategory
	r := m.Gate.Book().Read(def.Key(), class.P)
	if class.By == model.ClassByOperator {
		r.P = 1 // said by the operator: it covers the cache and needs no record
	}
	grade := engine.AskBlank
	if class.Value != "" {
		grade = engine.Decide(def.Spec, r, engine.Explored(chargeTopic(c)))
	}
	if class.By == model.ClassByOperator {
		grade = engine.Act
	}
	proposed := class.Value
	payload, _ := json.Marshal(act.CategoryPayload{Kind: c.Kind, ID: c.ID, Merchant: c.Merchant, Name: c.Name, Proposed: proposed})
	filled := ""
	if grade == engine.AskFilled || grade == engine.Act {
		filled = proposed
	}
	title, body := "이 결제는 어떤 분류인가요?", fmt.Sprintf("%s · %s원", c.Merchant, won(c.AmountKrw))
	if c.Kind == "item" {
		title, body = "이 상품은 어떤 분류인가요?", fmt.Sprintf("%s · %s · %s원", c.Merchant, c.Name, won(c.AmountKrw))
	}
	at := c.ApprovedAt.In(m.Opts.Loc)
	body += fmt.Sprintf("\n%d/%d %s", int(at.Month()), at.Day(), at.Format("15:04"))
	if card := strings.TrimSpace(c.Issuer + " " + c.CardTail); card != "" {
		body += " · " + card
	}
	reason := "가맹점 이름만으로는 분류를 몰라요"
	if filled != "" {
		reason = fmt.Sprintf("%s(으)로 보여요", filled)
	}
	return model.Intent{
		Skill: def.Skill, Kind: def.Kind, Context: def.Context, Topic: chargeTopic(c), Title: title, Body: body,
		Reason: reason, Confidence: class.P, Grade: string(grade), Reversible: true, Value: 0.3,
		Payload: payload, Fields: chargeFields(c, filled, categories),
	}, grade == engine.AskBlank || grade == engine.AskFilled
}

// won is an amount with its thousands marked.
func won(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// chargeFields is the form: the categories to pick from, filled in or not, and
// for a charge whether its merchant goes the same way from now on.
func chargeFields(c slot.Charge, filled string, categories []string) []model.Field {
	var choices []string
	for _, x := range categories {
		if x != slot.Other {
			choices = append(choices, x)
		}
	}
	fields := []model.Field{{Key: "category", Label: "분류", Kind: "select", Options: choices, Value: filled, Required: true}}
	if c.Kind != "item" {
		fields = append(fields, model.Field{Key: "remember", Label: "이 가맹점은", Kind: "select",
			Options: []string{act.RememberYes, act.RememberNo}, Value: act.RememberYes})
	}
	return fields
}

// chargeAnswered keeps what the operator said: a category they chose is what
// that merchant is from now on, over anything the model said, and one turned
// down stays 기타 and is not asked about again.
func (m *Mind) chargeAnswered(ctx context.Context, in model.Intent) {
	var p act.CategoryPayload
	if json.Unmarshal(in.Payload, &p) != nil {
		return
	}
	switch in.Status {
	case model.Executed:
		if in.DecidedBy != "user" || strings.TrimSpace(p.Answers["remember"]) == act.RememberNo {
			return
		}
		key := slot.Charge{Kind: p.Kind, Merchant: p.Merchant, Name: p.Name}.Key()
		err := m.Opts.Store.PutClass(ctx, model.Class{Kind: slot.MerchantCategory.Name, Key: key, Value: p.Chosen(), P: 1, By: model.ClassByOperator, At: m.Opts.Now()})
		if err != nil {
			m.Opts.Logger.Warn("고른 분류를 기억하지 못했습니다", "err", err)
		}
	case model.Rejected:
		if m.Opts.Spending == nil {
			return
		}
		if err := m.Opts.Spending.Settle(ctx, in.Payload); err != nil {
			m.Opts.Logger.Warn("거절한 결제를 기타로 두지 못했습니다", "err", err)
		}
	}
}

// chargeLoop keeps looking.
func (m *Mind) chargeLoop(ctx context.Context) {
	t := time.NewTicker(chargeEvery)
	defer t.Stop()
	for {
		if err := m.fileCharges(ctx); err != nil && ctx.Err() == nil {
			m.Opts.Logger.Warn("분류를 물을 결제를 읽지 못했습니다", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
