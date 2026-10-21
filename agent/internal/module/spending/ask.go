package spending

// Asking the operator what a charge was for, when the rules could not tell.
//
// The keyword rules and the merchants the operator taught place most charges;
// what they miss lands in 기타, where a month's breakdown cannot use it. So the
// module asks: one card per charge - or per product of a split order - with
// the categories to pick from, which rings the phone like any card. Approved,
// the charge takes the category, and with "앞으로도" so does every charge of
// that merchant, past and next (spending-svc's merchant rule). Turned down, it
// stays 기타, set by hand now, and is not asked about again.
//
// The model writes nothing here: the module raises the card on its own, and
// the one write is the operator's answer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

var _ module.Editor = (*Module)(nil)

// KindCategory is the card asking what a charge (or a product) was for.
const KindCategory = "spending.category"

// other is spending-svc's catch-all category: what the rules leave a charge
// in, and what a turned-down card leaves it in for good.
const other = "기타"

// Answers to the "remember" field.
const (
	rememberYes = "앞으로도"
	rememberNo  = "이번만"
)

// Unclear is spending-svc's GET /unclear entry: a charge ("tx") or a product
// of a split order ("item") that the rules left in 기타.
type Unclear struct {
	Kind       string    `json:"kind"`
	ID         int64     `json:"id"`
	TxID       int64     `json:"txId"`
	Merchant   string    `json:"merchant"`
	Name       string    `json:"name,omitempty"`
	AmountKrw  int64     `json:"amountKrw"`
	ApprovedAt time.Time `json:"approvedAt"`
	Issuer     string    `json:"issuer"`
	CardTail   string    `json:"cardTail"`
}

// AskInput is the card, kept as the proposal's action input: what is asked
// about and the form as the operator left it.
type AskInput struct {
	Entry  Unclear        `json:"entry"`
	Fields []action.Field `json:"fields"`
}

func (s *Store) Unclear(ctx context.Context) ([]Unclear, []string, error) {
	var payload struct {
		Entries    []Unclear `json:"entries"`
		Categories []string  `json:"categories"`
	}
	err := s.call(ctx, http.MethodGet, "/unclear", nil, &payload)
	return payload.Entries, payload.Categories, err
}

// SetCategory sets a charge's category; with remember, its merchant's too.
// It says how many charges changed.
func (s *Store) SetCategory(ctx context.Context, id int64, category string, remember bool) (int, error) {
	var out struct {
		Updated int `json:"updated"`
	}
	err := s.call(ctx, http.MethodPatch, fmt.Sprintf("/transactions/%d", id),
		map[string]any{"category": category, "remember": remember}, &out)
	return out.Updated, err
}

func (s *Store) SetItemCategory(ctx context.Context, id int64, category string) error {
	return s.call(ctx, http.MethodPatch, fmt.Sprintf("/items/%d", id), map[string]string{"category": category}, nil)
}

// Ask turns the asking on: cards go to proposals, and their decisions come
// back here.
func (m *Module) Ask(proposals *proposal.Store, log *slog.Logger) {
	m.proposals, m.log = proposals, log
	proposals.OnDecide(m.decided)
}

// Attach has each category card put in the conversation as it is raised,
// so it stays there answered rather than floating below the thread only
// while it waits.
func (m *Module) Attach(fn func(proposalID string)) { m.attach = fn }

type AskConfig struct {
	Every time.Duration
	// MaxOpen is how many of these cards may wait at once: a backlog comes a
	// few at a time, not as a wall of cards.
	MaxOpen int
	// Judge, when set, is asked first: a merchant whose trade is plain to
	// anyone ("써브웨이강남역점" is lunch) is filed without a card, and the
	// card is for what a person would also have to think about. Model is
	// what it judges on.
	Judge Background
	Model string
}

// RunAsker looks for what to ask about now and every Every.
func (m *Module) RunAsker(ctx context.Context, cfg AskConfig) {
	tick := time.NewTicker(cfg.Every)
	defer tick.Stop()
	for {
		if err := m.askOnce(ctx, cfg); err != nil && m.log != nil {
			m.log.Warn("분류를 물을 내역을 읽지 못했습니다", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// JudgeMin is the confidence a judgement needs to be filed without asking.
const JudgeMin = 0.85

// Judgement is what the model says one unclear charge was for.
type Judgement struct {
	ID         string  `json:"id"` // "tx:12" or "item:34"
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Why        string  `json:"why,omitempty"`
}

func (m *Module) askOnce(ctx context.Context, cfg AskConfig) error {
	if m.proposals == nil {
		return nil
	}
	entries, categories, err := m.store.Unclear(ctx)
	if err != nil {
		return err
	}
	waiting := map[string]bool{}
	for _, p := range m.proposals.List() {
		if p.State != proposal.Pending || p.Action.Kind != KindCategory {
			continue
		}
		var in AskInput
		if json.Unmarshal(p.Action.Input, &in) == nil {
			waiting[in.Entry.Kind+":"+fmt.Sprint(in.Entry.ID)] = true
		}
	}
	// The plain ones first, filed by the judge; what it is unsure of goes
	// on a card, and is not put to it again.
	var undecided []Unclear
	for _, e := range entries {
		if !waiting[e.Kind+":"+fmt.Sprint(e.ID)] {
			undecided = append(undecided, e)
		}
	}
	entries = m.judge(ctx, cfg, undecided, categories)

	open := len(waiting)
	for _, e := range entries {
		if open >= cfg.MaxOpen {
			break
		}
		in := AskInput{Entry: e, Fields: askFields(e, categories)}
		raw, _ := json.Marshal(in)
		p, _ := m.proposals.Open(proposal.Proposal{
			Origin: proposal.OriginNotice,
			Action: action.Action{Kind: KindCategory, Input: raw},
			Card:   categoryCard(in),
		})
		if m.attach != nil {
			m.attach(p.ID)
		}
		open++
	}
	return nil
}

// judge files what the model is sure of and returns the rest. Entries it
// was unsure of once are not asked of it again (m.unsure).
func (m *Module) judge(ctx context.Context, cfg AskConfig, entries []Unclear, categories []string) []Unclear {
	if cfg.Judge == nil || len(entries) == 0 {
		return entries
	}
	var ask []Unclear
	var rest []Unclear
	for _, e := range entries {
		if m.unsure[e.Kind+":"+fmt.Sprint(e.ID)] {
			rest = append(rest, e)
		} else {
			ask = append(ask, e)
		}
	}
	if len(ask) == 0 || !cfg.Judge.CanStart(false) {
		return entries
	}
	if len(ask) > judgeBatch {
		rest = append(rest, ask[judgeBatch:]...)
		ask = ask[:judgeBatch]
	}
	jctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := cfg.Judge.Background(jctx, claudecode.Task{
		Name:    "결제 분류",
		Prompt:  judgePrompt(ask, categories),
		System:  judgeSystem,
		Model:   cfg.Model,
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		if m.log != nil {
			m.log.Warn("결제 분류 판단을 받지 못해 카드로 묻습니다", "err", err)
		}
		return entries
	}
	verdicts := map[string]Judgement{}
	for _, j := range parseJudgements(out) {
		verdicts[j.ID] = j
	}
	if m.unsure == nil {
		m.unsure = map[string]bool{}
	}
	for _, e := range ask {
		key := e.Kind + ":" + fmt.Sprint(e.ID)
		j, ok := verdicts[key]
		if !ok || j.Confidence < JudgeMin || j.Category == "" || j.Category == other || !contains(categories, j.Category) {
			m.unsure[key] = true
			rest = append(rest, e)
			continue
		}
		fctx, fcancel := context.WithTimeout(ctx, 15*time.Second)
		var ferr error
		if e.Kind == "item" {
			ferr = m.store.SetItemCategory(fctx, e.ID, j.Category)
		} else {
			_, ferr = m.store.SetCategory(fctx, e.ID, j.Category, true)
		}
		fcancel()
		if ferr != nil {
			if m.log != nil {
				m.log.Warn("판단한 분류를 쓰지 못해 카드로 묻습니다", "entry", key, "err", ferr)
			}
			rest = append(rest, e)
			continue
		}
		if m.log != nil {
			m.log.Info("결제 분류를 판단해 적었습니다", "entry", key, "merchant", e.Merchant, "category", j.Category, "confidence", j.Confidence, "why", j.Why)
		}
	}
	return rest
}

// judgeBatch is how many charges go to the model in one request.
const judgeBatch = 8

const judgeSystem = `당신은 가계부의 분류 담당입니다. 카드 결제 알림의 가맹점 이름(과 상품명)만 보고 그 결제가 어떤 분류인지 판단합니다.
- 가맹점 이름에서 업종이 누구에게나 분명하면(프랜차이즈 식당·카페·편의점·마트·주유소·약국·병원·영화관·통신사·택시 등) 그 분류와 높은 확신도(0.85 이상)를 답합니다. 지점명·법인 표기·띄어쓰기·철자 차이("써브웨이"="서브웨이")는 무시합니다.
- 이름만으로 모르는 곳, 여러 분류가 다 말이 되는 곳(복합몰, 생소한 상호, 개인 이름, 온라인 결제 대행)은 "기타"와 낮은 확신도를 답합니다. 추측하지 않습니다.
- 답은 JSON 배열 하나뿐입니다. 다른 말, 코드 블록 표시는 쓰지 않습니다.`

func judgePrompt(entries []Unclear, categories []string) string {
	var b strings.Builder
	b.WriteString("고를 수 있는 분류: ")
	var choices []string
	for _, c := range categories {
		if c != other {
			choices = append(choices, c)
		}
	}
	b.WriteString(strings.Join(choices, ", "))
	b.WriteString("\n\n결제:\n")
	for _, e := range entries {
		line := fmt.Sprintf("- id=%s:%d 가맹점=%q 금액=%s", e.Kind, e.ID, e.Merchant, krw(e.AmountKrw))
		if e.Name != "" {
			line += fmt.Sprintf(" 상품=%q", e.Name)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n각 id 에 대해 {\"id\":\"tx:12\",\"category\":\"식비\",\"confidence\":0.95,\"why\":\"한 줄 근거\"} 꼴의 객체를 담은 JSON 배열만 출력하세요.")
	return b.String()
}

// parseJudgements reads the model's answer: a JSON array, possibly wrapped in
// a code fence or prose - the first array in the text is taken.
func parseJudgements(text string) []Judgement {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return nil
	}
	var out []Judgement
	if json.Unmarshal([]byte(text[start:end+1]), &out) != nil {
		return nil
	}
	for i := range out {
		out[i].Category = strings.TrimSpace(out[i].Category)
	}
	return out
}

func askFields(e Unclear, categories []string) []action.Field {
	choices := []string{}
	for _, c := range categories {
		if c != other {
			choices = append(choices, c)
		}
	}
	fields := []action.Field{{Key: "category", Label: "분류", Kind: "select", Options: choices, Required: true}}
	if e.Kind != "item" {
		fields = append(fields, action.Field{Key: "remember", Label: "이 가맹점은", Kind: "select",
			Options: []string{rememberYes, rememberNo}, Value: rememberYes})
	}
	return fields
}

func categoryCard(in AskInput) action.Card {
	e := in.Entry
	title, body := "이 결제는 어떤 분류인가요?", e.Merchant+" · "+krw(e.AmountKrw)
	consequence := "승인하면 이 분류로 바꿉니다. '앞으로도'면 이 가맹점의 지난·다음 결제도 같은 분류가 됩니다. 반려하면 기타로 두고 다시 묻지 않습니다."
	if e.Kind == "item" {
		title, body = "이 상품은 어떤 분류인가요?", e.Merchant+" · "+e.Name+" · "+krw(e.AmountKrw)
		consequence = "승인하면 이 상품을 이 분류로 바꿉니다. 반려하면 기타로 두고 다시 묻지 않습니다."
	}
	at := e.ApprovedAt.Local()
	body += fmt.Sprintf("\n%d/%d(%s) %s", int(at.Month()), at.Day(), weekdays[at.Weekday()], at.Format("15:04"))
	if card := strings.TrimSpace(e.Issuer + " " + e.CardTail); card != "" {
		body += " · " + card
	}
	return action.Card{Title: title, Body: body, Consequence: consequence, Fields: in.Fields}
}

// --- the card ---------------------------------------------------------------

func (m *Module) Specs() []action.Spec {
	return []action.Spec{{Kind: KindCategory, Summary: "가계부가 결제의 분류를 묻습니다.", Reversible: true}}
}

func (m *Module) Preview(_ context.Context, a action.Action) (action.Card, error) {
	var in AskInput
	if err := json.Unmarshal(a.Input, &in); err != nil {
		return action.Card{}, err
	}
	return categoryCard(in), nil
}

// Execute is never reached: the decision goes through decided.
func (m *Module) Execute(context.Context, action.Action) (action.Result, error) {
	return action.Result{}, errors.New("분류 카드는 실행하지 않습니다")
}

// Revise applies the picks on the card and checks them against its choices.
func (m *Module) Revise(_ context.Context, a action.Action, edits map[string]string) (action.Action, action.Card, error) {
	if a.Kind != KindCategory {
		return a, action.Card{}, errors.New("이 카드는 고칠 수 없습니다.")
	}
	var in AskInput
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
		if v != "" && !contains(f.Options, v) {
			return a, action.Card{}, fmt.Errorf("%s: 고를 수 있는 값이 아닙니다.", f.Label)
		}
		if f.Required && v == "" {
			return a, action.Card{}, fmt.Errorf("%s을 골라 주세요.", f.Label)
		}
	}
	for k := range edits {
		if !known[k] {
			return a, action.Card{}, fmt.Errorf("고칠 수 없는 항목입니다: %s", k)
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return a, action.Card{}, err
	}
	return action.Action{Kind: a.Kind, Input: raw}, categoryCard(in), nil
}

// decided writes the answer: the category picked, or 기타 for good.
func (m *Module) decided(p proposal.Proposal, d proposal.Decision) {
	if p.Action.Kind != KindCategory {
		return
	}
	var in AskInput
	if json.Unmarshal(p.Action.Input, &in) != nil {
		return
	}
	approved := d == proposal.Approve
	category, remember := other, false
	if approved {
		category, remember = fieldValue(in.Fields, "category"), fieldValue(in.Fields, "remember") != rememberNo
		if category == "" {
			m.proposals.Settle(p.ID, proposal.Failed, "고른 분류가 없습니다.")
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	changed := 1
	var err error
	if in.Entry.Kind == "item" {
		err = m.store.SetItemCategory(ctx, in.Entry.ID, category)
	} else {
		changed, err = m.store.SetCategory(ctx, in.Entry.ID, category, remember)
	}
	switch {
	case err != nil:
		m.proposals.Settle(p.ID, proposal.Failed, "분류를 바꾸지 못했습니다: "+err.Error())
	case !approved:
		m.proposals.Settle(p.ID, proposal.Rejected, "기타로 두었습니다. 다시 묻지 않습니다.")
	case changed > 1:
		m.proposals.Settle(p.ID, proposal.Executed, fmt.Sprintf("%s(으)로 바꿨습니다. 같은 가맹점 %d건도 함께 바꿨습니다.", category, changed-1))
	default:
		m.proposals.Settle(p.ID, proposal.Executed, fmt.Sprintf("%s(으)로 바꿨습니다.", category))
	}
}

func fieldValue(fields []action.Field, key string) string {
	for _, f := range fields {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
