package assets

// Asking the operator where money taken out of an investment account went.
//
// assets-svc pairs each withdrawal with a deposit into another of the
// accounts (outflows.ts); what nothing pairs, three days on, is put to the
// operator here - one card per withdrawal, all at once: there are a few a
// month, and the first time, the ones from before. Approved, the answer goes
// to assets-svc, and what was spent reaches the 가계부 from there. Turned
// down, it is asked again in a week.
//
// The model writes nothing here: the module raises the card on its own, and
// the one write is the operator's answer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

// KindOutflow is the card asking where a withdrawal went.
const KindOutflow = "assets.outflow"

// SnoozeFor is how long a turned-down card waits before it is raised again.
const SnoozeFor = 7 * 24 * time.Hour

// The card's choices and what assets-svc calls them.
const (
	useSpend    = "지출"
	useCard     = "카드값"
	useSaved    = "보관"
	useParked   = "아직 통장에"
	useTransfer = "다른 투자 계좌로"
)

var useCodes = map[string]string{
	useSpend: "spend", useCard: "card", useSaved: "saved", useParked: "parked", useTransfer: "transfer",
}

var useChoices = []string{useSpend, useCard, useSaved, useParked, useTransfer}

const useHint = "지출: 계좌이체·현금으로 씀 - 가계부에 들어가요\n" +
	"카드값: 이미 가계부에 있는 카드 결제를 메움\n" +
	"보관: 예금·청약처럼 다른 곳에 둠\n" +
	"아직 통장에: 다시 넣을 돈 - 30일 안에 들어오면 옮긴 것으로 봐요"

// OutflowInput is the card, kept as the proposal's action input: the
// withdrawal asked about and the form as the operator left it.
type OutflowInput struct {
	Outflow Outflow        `json:"outflow"`
	Fields  []action.Field `json:"fields"`
}

type AskConfig struct {
	Every time.Duration
	// MaxOpen is how many of these cards may wait at once - a guard, not a
	// pace: every withdrawal is asked about now (the operator's choice,
	// 2026-10-26), and this only stops a wall of cards if something breaks.
	MaxOpen int
	// Categories is the 가계부's list, for the card's 분류 (spending-svc).
	Categories func(ctx context.Context) ([]string, error)
}

// Ask turns the asking on: cards go to proposals, and their decisions come
// back here.
func (m *Module) Ask(proposals *proposal.Store, log *slog.Logger) {
	m.proposals, m.log = proposals, log
	proposals.OnDecide(m.decided)
}

// Attach has each card put in the conversation as it is raised, so it stays
// there answered rather than floating below the thread only while it waits.
func (m *Module) Attach(fn func(proposalID string)) { m.attach = fn }

// RunAsker looks for what to ask about now and every Every.
func (m *Module) RunAsker(ctx context.Context, cfg AskConfig) {
	tick := time.NewTicker(cfg.Every)
	defer tick.Stop()
	for {
		if err := m.askOnce(ctx, cfg); err != nil && m.log != nil && ctx.Err() == nil {
			m.log.Warn("출금 용도를 물을 것을 읽지 못했습니다", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (m *Module) askOnce(ctx context.Context, cfg AskConfig) error {
	if m.proposals == nil {
		return nil
	}
	outflows, err := m.store.Outflows(ctx)
	if err != nil {
		return err
	}
	var asks []Outflow
	for _, o := range outflows {
		if o.Status == "ask" && o.AskKrw >= 1 {
			asks = append(asks, o)
		}
	}

	asking := map[string]bool{}
	for _, o := range asks {
		asking[o.Key] = true
	}

	// A card waiting, or one decided and still being written, is the
	// question already; one turned down this week is not asked again yet.
	// A card waiting on what is no longer a question - the money turned up
	// in an account here, or it was answered on the screen - is put away.
	busy := map[string]bool{}
	open := 0
	now := time.Now()
	for _, p := range m.proposals.List() {
		if p.Action.Kind != KindOutflow {
			continue
		}
		var in OutflowInput
		if json.Unmarshal(p.Action.Input, &in) != nil {
			continue
		}
		switch p.State {
		case proposal.Pending:
			if !asking[in.Outflow.Key] {
				m.proposals.Abandon(p.ID, "이제 물을 게 없어요 - 다른 투자 계좌로 들어온 돈을 찾았거나 이미 답했어요.")
				continue
			}
			busy[in.Outflow.Key] = true
			open++
		case proposal.Approved:
			busy[in.Outflow.Key] = true
		case proposal.Rejected:
			// Raised within the week: the decision is in memory until a
			// restart, the card's own time after one.
			if now.Sub(p.Opened()) < SnoozeFor {
				busy[in.Outflow.Key] = true
			}
		}
	}
	m.mu.Lock()
	for key, at := range m.snoozed {
		if now.Sub(at) < SnoozeFor {
			busy[key] = true
		}
	}
	m.mu.Unlock()

	var fresh []Outflow
	for _, o := range asks {
		if !busy[o.Key] {
			fresh = append(fresh, o)
		}
	}
	if len(fresh) == 0 || open >= cfg.MaxOpen {
		return nil
	}
	if cfg.Categories == nil {
		return errors.New("가계부 분류 목록을 받을 곳이 없습니다")
	}
	categories, err := cfg.Categories(ctx)
	if err != nil {
		return fmt.Errorf("가계부 분류 목록: %w", err)
	}
	for _, o := range fresh {
		if open >= cfg.MaxOpen {
			break
		}
		in := OutflowInput{Outflow: o, Fields: outflowFields(o, categories)}
		raw, _ := json.Marshal(in)
		p, _ := m.proposals.Open(proposal.Proposal{
			Origin: proposal.OriginNotice,
			Action: action.Action{Kind: KindOutflow, Input: raw},
			Card:   outflowCard(in),
		})
		if m.attach != nil {
			m.attach(p.ID)
		}
		open++
	}
	return nil
}

func outflowFields(o Outflow, categories []string) []action.Field {
	return []action.Field{
		{Key: "use", Label: "용도", Kind: "select", Options: useChoices, Required: true, Hint: useHint},
		{Key: "category", Label: "가계부 분류 (지출일 때)", Kind: "select", Options: categories},
		{Key: "memo", Label: "쓴 곳 (지출일 때, 안 적어도 돼요)", Kind: "text",
			Hint: "예: 월세 - 가계부에 이 이름으로 들어가요"},
		{Key: "amount", Label: "금액", Kind: "number", Value: strconv.FormatInt(int64(o.AskKrw+0.5), 10), Required: true,
			Hint: "일부만 적으면 나머지는 다시 물어요"},
	}
}

var weekdays = [...]string{"일", "월", "화", "수", "목", "금", "토"}

// day is "10/26(일)".
func day(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return fmt.Sprintf("%d/%d(%s)", int(t.Month()), t.Day(), weekdays[t.Weekday()])
}

func shortDay(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return fmt.Sprintf("%d/%d", int(t.Month()), t.Day())
}

var useLabels = map[string]string{
	"transfer": "옮김", "spend": useSpend, "card": useCard, "saved": useSaved, "parked": useParked,
}

func outflowCard(in OutflowInput) action.Card {
	o := in.Outflow
	account := venueLabel[o.Venue]
	what := strings.TrimSpace(account + " " + o.Memo)
	body := fmt.Sprintf("%s %s · %s", what, krw(o.AmountKrw), day(o.Date))
	var known []string
	for _, p := range o.Parts {
		line := fmt.Sprintf("%s %s", useLabels[p.Use], krw(p.AmountKrw))
		if p.To != nil {
			line = fmt.Sprintf("%s %s 입금으로 옮김 %s", shortDay(p.To.Date), venueLabel[p.To.Venue], krw(p.AmountKrw))
		}
		known = append(known, line)
	}
	if len(known) == 0 {
		body += "\n다른 투자 계좌로 다시 들어온 돈이 없어요."
	} else {
		body += "\n" + strings.Join(known, " · ") + fmt.Sprintf("\n남은 %s을 물어요.", krw(o.AskKrw))
	}
	if len(o.Candidates) > 0 {
		var near []string
		for _, c := range o.Candidates {
			near = append(near, fmt.Sprintf("%s %s %s", shortDay(c.Date), venueLabel[c.Venue], krw(c.AmountKrw)))
		}
		body += "\n그 뒤 들어온 돈: " + strings.Join(near, " · ") + " - 같은 돈이면 '다른 투자 계좌로'"
	}
	// The form comes before the body on the card, and there may be several
	// cards at once: the title alone has to say which withdrawal this is.
	title := fmt.Sprintf("%s %s에서 뺀 %s, 어디로 갔나요?", shortDay(o.Date), account, krw(o.AmountKrw))
	if len(o.Parts) > 0 {
		title = fmt.Sprintf("%s %s에서 뺀 돈 중 %s은 어디로 갔나요?", shortDay(o.Date), account, krw(o.AskKrw))
	}
	return action.Card{
		Title: title,
		Body:  body,
		Consequence: "승인하면 자산 탭에 적어요. 지출이면 가계부에 투자금으로 들어가고, 나머지는 가계부에 넣지 않아요. " +
			"수익률은 그대로예요. 반려하면 일주일 뒤에 다시 물어요.",
		Fields: in.Fields,
	}
}

// --- the card ---------------------------------------------------------------

func (m *Module) Specs() []action.Spec {
	return []action.Spec{{Kind: KindOutflow, Summary: "자산이 투자 계좌에서 뺀 돈이 어디로 갔는지 묻습니다.", Reversible: true}}
}

func (m *Module) Preview(_ context.Context, a action.Action) (action.Card, error) {
	var in OutflowInput
	if err := json.Unmarshal(a.Input, &in); err != nil {
		return action.Card{}, err
	}
	return outflowCard(in), nil
}

// Execute is never reached: the decision goes through decided.
func (m *Module) Execute(context.Context, action.Action) (action.Result, error) {
	return action.Result{}, errors.New("출금 용도 카드는 실행하지 않습니다")
}

// Revise applies the picks on the card and checks them: a choice it offered,
// a category for a spend, an amount no more than went out.
func (m *Module) Revise(_ context.Context, a action.Action, edits map[string]string) (action.Action, action.Card, error) {
	if a.Kind != KindOutflow {
		return a, action.Card{}, errors.New("이 카드는 고칠 수 없습니다.")
	}
	var in OutflowInput
	if err := json.Unmarshal(a.Input, &in); err != nil {
		return a, action.Card{}, err
	}
	known := map[string]bool{}
	for i, f := range in.Fields {
		known[f.Key] = true
		if v, ok := edits[f.Key]; ok {
			in.Fields[i].Value = strings.TrimSpace(v)
		}
		v := in.Fields[i].Value
		if f.Kind == "select" && v != "" && !contains(f.Options, v) {
			return a, action.Card{}, fmt.Errorf("%s: 고를 수 있는 값이 아닙니다.", f.Label)
		}
		if f.Required && v == "" {
			return a, action.Card{}, fmt.Errorf("%s을 채워 주세요.", f.Label)
		}
	}
	for k := range edits {
		if !known[k] {
			return a, action.Card{}, fmt.Errorf("고칠 수 없는 항목입니다: %s", k)
		}
	}
	if _, err := answerOf(in); err != nil {
		return a, action.Card{}, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return a, action.Card{}, err
	}
	return action.Action{Kind: a.Kind, Input: raw}, outflowCard(in), nil
}

// answerOf reads the card's fields as an answer for assets-svc.
func answerOf(in OutflowInput) (OutflowAnswer, error) {
	o := in.Outflow
	use, ok := useCodes[fieldValue(in.Fields, "use")]
	if !ok {
		return OutflowAnswer{}, errors.New("어디로 갔는지 골라 주세요.")
	}
	amount, err := strconv.ParseInt(digits(fieldValue(in.Fields, "amount")), 10, 64)
	if err != nil || amount <= 0 {
		return OutflowAnswer{}, errors.New("금액을 적어 주세요.")
	}
	if float64(amount) > o.AmountKrw+0.5 {
		return OutflowAnswer{}, fmt.Errorf("금액이 출금액 %s보다 커요.", krw(o.AmountKrw))
	}
	a := OutflowAnswer{Key: o.Key, FlowKrw: o.AmountKrw, Use: use, AmountKrw: amount}
	if use == "spend" {
		a.Category = fieldValue(in.Fields, "category")
		if a.Category == "" {
			return OutflowAnswer{}, errors.New("지출이면 가계부 분류를 골라 주세요.")
		}
		a.Memo = fieldValue(in.Fields, "memo")
		if len([]rune(a.Memo)) > 40 {
			return OutflowAnswer{}, errors.New("어디에 썼는지는 40자까지예요.")
		}
	}
	return a, nil
}

// decided writes the answer, or puts the question off for a week.
func (m *Module) decided(p proposal.Proposal, d proposal.Decision) {
	if p.Action.Kind != KindOutflow {
		return
	}
	var in OutflowInput
	if json.Unmarshal(p.Action.Input, &in) != nil {
		return
	}
	if d != proposal.Approve {
		m.mu.Lock()
		if m.snoozed == nil {
			m.snoozed = map[string]time.Time{}
		}
		m.snoozed[in.Outflow.Key] = time.Now()
		m.mu.Unlock()
		m.proposals.Settle(p.ID, proposal.Rejected, "일주일 뒤에 다시 물어요.")
		return
	}
	a, err := answerOf(in)
	if err != nil {
		m.proposals.Settle(p.ID, proposal.Failed, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := m.store.AnswerOutflow(ctx, a); err != nil {
		m.proposals.Settle(p.ID, proposal.Failed, "적지 못했습니다: "+err.Error())
		return
	}
	m.proposals.Settle(p.ID, proposal.Executed, answeredNote(in.Outflow, a))
}

func answeredNote(o Outflow, a OutflowAnswer) string {
	var note string
	switch a.Use {
	case "spend":
		what := a.Category
		if a.Memo != "" {
			what = a.Memo + ", " + a.Category
		}
		note = fmt.Sprintf("지출(%s) %s - 가계부에 투자금으로 들어가요.", what, krw(float64(a.AmountKrw)))
	case "card":
		note = fmt.Sprintf("카드값 %s - 가계부에는 이미 있어서 넣지 않아요.", krw(float64(a.AmountKrw)))
	case "saved":
		note = fmt.Sprintf("보관 %s - 가계부에는 넣지 않아요.", krw(float64(a.AmountKrw)))
	case "parked":
		note = fmt.Sprintf("아직 통장에 %s - 30일 안에 투자 계좌로 들어오면 옮긴 것으로 볼게요.", krw(float64(a.AmountKrw)))
	case "transfer":
		note = fmt.Sprintf("다른 투자 계좌로 옮김 %s.", krw(float64(a.AmountKrw)))
	}
	if rest := o.AskKrw - float64(a.AmountKrw); rest >= 1 {
		note += fmt.Sprintf(" 남은 %s은 다시 물어요.", krw(rest))
	}
	return note
}

func fieldValue(fields []action.Field, key string) string {
	for _, f := range fields {
		if f.Key == key {
			return strings.TrimSpace(f.Value)
		}
	}
	return ""
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
