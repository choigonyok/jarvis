package assets

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/choigonyok/jarvis/agent/internal/core/module"
)

// Read-only by construction: a ContextSource and nothing else. Were this ever
// to implement Actuator, that assertion would belong here too - and so would
// a card for every order.
var _ module.ContextSource = (*Module)(nil)

const Name = "assets"

type Module struct {
	store *Store
}

func New(store *Store) *Module { return &Module{store: store} }

func (m *Module) Name() string { return Name }

// Start does not dial the service. The calendar refuses to boot without its
// backend because an empty calendar would be written over a full one; nothing
// here writes, and a brokerage outage should not take the whole assistant
// down with it. A failed lookup is reported to the model as a tool error.
func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error { return nil }

// Facts is the portfolio in a few lines - total, return, and how far off
// target - for when a reasoning loop wants it without a tool call.
func (m *Module) Facts(ctx context.Context, _ string) ([]module.Fact, error) {
	p, err := m.store.Portfolio(ctx)
	if err != nil {
		return nil, err
	}
	var out []module.Fact
	for _, line := range strings.Split(strings.TrimSpace(renderSummary(p)), "\n") {
		out = append(out, module.Fact{Source: Name, Text: line})
	}
	return out, nil
}

// --- 렌더링 ----------------------------------------------------------------
//
// The model gets text first and structured content alongside. Text is what it
// actually reads, so every figure is already formatted the way the console
// shows it - a model doing its own rounding of 3165432.7 is how "약 317만"
// and "316만" end up in the same answer.

var windowLabel = []struct{ key, label string }{
	{"day", "1일"}, {"month", "1개월"}, {"year", "1년"},
}

var venueLabel = map[string]string{
	"upbit": "업비트", "kis": "한국투자증권", "gold": "금현물", "other": "기타",
}

func renderSummary(p Portfolio) string {
	var b strings.Builder
	fmt.Fprintf(&b, "총자산 %s (현금 %s, 환율 %s원/USD, 기준 %s)\n",
		krw(p.TotalKrw), krw(p.CashKrw), comma(p.UsdKrw), p.At)
	fmt.Fprintf(&b, "보유 종목 평가손익 %s (%s, 원가 %s 대비)\n",
		signedKrw(p.ProfitKrw), pct(p.ReturnRate), krw(p.CostKrw))
	if pr := p.Principal; pr != nil {
		fmt.Fprintf(&b, "원금 대비 %s (%s, %s부터 넣은 원금 %s)\n",
			signedKrw(pr.ProfitKrw), pct(pr.Rate), pr.Since, krw(pr.PrincipalKrw))
		// The principal is only as right as the ledger under it.
		for _, c := range pr.Checks {
			b.WriteString("원금 기록 불일치: " + c.Message + "\n")
		}
	}
	var changes []string
	for _, w := range windowLabel {
		c, ok := p.Changes[w.key]
		if !ok || c.Rate == nil {
			continue
		}
		s := w.label + " " + pct(*c.Rate)
		if len(c.Missing) > 0 {
			s += "(" + strings.Join(c.Missing, "·") + " 제외)"
		}
		changes = append(changes, s)
	}
	if len(changes) > 0 {
		b.WriteString("기간 변동 " + strings.Join(changes, ", ") + "\n")
	}
	if a := p.Allocation; a != nil {
		var off []string
		for _, r := range a.Rows {
			if !r.InBand {
				off = append(off, fmt.Sprintf("%s %s→목표 %s", r.Label, pct0(r.Current), pct0(r.Target)))
			}
		}
		if len(off) == 0 {
			b.WriteString("목표 비중: 모두 허용 범위 안\n")
		} else {
			b.WriteString("목표 비중 이탈: " + strings.Join(off, ", ") + "\n")
		}
	}
	for _, problem := range p.Problems {
		b.WriteString("조회 실패: " + problem + "\n")
	}
	return b.String()
}

func renderPortfolio(p Portfolio) string {
	var b strings.Builder
	b.WriteString(renderSummary(p))

	b.WriteString("\n보유 종목\n")
	if len(p.Holdings) == 0 {
		b.WriteString("- 없음\n")
	}
	for _, h := range p.Holdings {
		fmt.Fprintf(&b, "- %s(%s) · %s · %s개 · 평가 %s · 손익 %s (%s) · 현재가 %s / 평단 %s\n",
			h.Name, h.Symbol, venueLabel[h.Venue], qty(h.Quantity),
			krw(h.ValueKrw), signedKrw(h.ValueKrw-h.CostKrw), pct(rateOf(h.ValueKrw, h.CostKrw)),
			price(h.Price, h.Currency), price(h.AvgPrice, h.Currency))
	}
	for _, f := range p.Fixed {
		fmt.Fprintf(&b, "- %s · 고정 %s (비중 계산 제외)\n", f.Label, krw(f.ValueKrw))
	}

	if r := p.Realized; r != nil {
		fmt.Fprintf(&b, "\n판 것 (%s부터 실현손익 %s, 수수료 뺀 값)\n", r.Since, signedKrw(r.TotalKrw))
		for _, l := range r.Lines {
			state := "보유 중"
			if !l.Held {
				state = "다 팖"
			}
			fmt.Fprintf(&b, "- %s(%s) · %d번 · %s (%s) · %s\n",
				l.Name, l.Symbol, l.Sales, signedKrw(l.ProfitKrw), pct(rateOf(l.CostKrw+l.ProfitKrw, l.CostKrw)), state)
		}
		for _, t := range r.Tax.Baskets {
			label := map[string]string{"overseas": "해외주식", "coin": "코인"}[t.Kind]
			if !t.InForce {
				fmt.Fprintf(&b, "- %d년 %s 실현손익 %s · 아직 과세 안 함\n", r.Tax.Year, label, signedKrw(t.GainKrw))
				continue
			}
			fmt.Fprintf(&b, "- %d년 %s 실현손익 %s · 공제 %s · 예상 양도세 %s (22%%, 매도일 환율 기준 추정)\n",
				r.Tax.Year, label, signedKrw(t.GainKrw), krw(t.DeductionKrw), krw(t.TaxKrw))
		}
		for _, v := range r.Missing {
			fmt.Fprintf(&b, "- %s 매도 내역을 읽지 못해 빠짐\n", venueLabel[v])
		}
	}

	if pr := p.Principal; pr != nil && len(pr.Parts) > 0 {
		b.WriteString("\n계좌별 원금 대비\n")
		for _, part := range pr.Parts {
			if part.ValueKrw == nil {
				fmt.Fprintf(&b, "- %s · 원금 %s · 평가 불가\n", venueLabel[part.Venue], krw(part.PrincipalKrw))
				continue
			}
			line := fmt.Sprintf("- %s · 평가 %s · 원금 %s", venueLabel[part.Venue], krw(*part.ValueKrw), krw(part.PrincipalKrw))
			if part.ProfitKrw != nil {
				line += " · " + signedKrw(*part.ProfitKrw)
			}
			if part.Rate != nil {
				line += " (" + pct(*part.Rate) + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func renderAllocation(p Portfolio) string {
	a := p.Allocation
	if a == nil {
		return "자산 서비스가 목표 비중을 보내지 않았습니다. assets-svc 가 이 에이전트보다 오래된 버전입니다."
	}
	if a.TotalKrw <= 0 {
		return "비중을 계산할 자산이 없습니다."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "비중 계산 대상 %s (고정자산 제외). 허용 범위는 목표 ±7%%p, 목표의 절반을 넘지 않음.\n\n", krw(a.TotalKrw))
	for _, r := range a.Rows {
		state := "범위 안"
		if !r.InBand {
			state = "범위 밖"
		}
		gap := "목표대로"
		if math.Abs(r.GapKrw) >= 1 {
			if r.GapKrw > 0 {
				gap = krw(r.GapKrw) + " 부족"
			} else {
				gap = krw(-r.GapKrw) + " 초과"
			}
		}
		symbols := ""
		if len(r.Symbols) > 0 {
			symbols = " [" + strings.Join(r.Symbols, " ") + "]"
		}
		fmt.Fprintf(&b, "- %s: 지금 %s (%s) / 목표 %s · %s · %s%s\n",
			r.Label, pct1(r.Current), krw(r.ValueKrw), pct0(r.Target), gap, state, symbols)
	}

	label := map[string]string{}
	for _, r := range a.Rows {
		label[r.ID] = r.Label
	}
	if len(a.Moves) == 0 {
		b.WriteString("\n옮길 돈 없음.\n")
		return b.String()
	}
	b.WriteString("\n옮기면 목표가 되는 돈 (큰 초과분이 큰 부족분을 먼저 채움)\n")
	for _, mv := range a.Moves {
		note := ""
		if mv.Optional {
			note = " · 여유(받는 쪽이 이미 범위 안)"
		}
		fmt.Fprintf(&b, "- %s → %s %s%s\n", label[mv.From], label[mv.To], krw(mv.AmountKrw), note)
	}
	return b.String()
}

func renderHistory(rows []Snapshot) string {
	if len(rows) == 0 {
		return "기록된 스냅샷이 없습니다. 스냅샷은 자산 화면이나 조회가 있던 날에만 하루 한 장 남습니다."
	}
	var b strings.Builder
	b.WriteString("날짜 · 총자산 · 현금 · 원가 (그날 실제로 들고 있던 것 기준, 조회가 없던 날은 빠짐)\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s · %s · %s · %s\n", r.Date, krw(r.TotalKrw), krw(r.CashKrw), krw(r.CostKrw))
	}
	first, last := rows[0], rows[len(rows)-1]
	// No percentage: a deposit would read as a return, and the model would
	// repeat it as one. The rate belongs to get_portfolio.
	if len(rows) > 1 {
		fmt.Fprintf(&b, "\n%s → %s: %s. 입출금이 섞인 금액 변화이며 수익이 아닙니다.\n",
			first.Date, last.Date, signedKrw(last.TotalKrw-first.TotalKrw))
	}
	return b.String()
}

func rateOf(value, cost float64) float64 {
	if cost <= 0 {
		return 0
	}
	return (value - cost) / cost
}

func comma(v float64) string {
	n := int64(math.Round(math.Abs(v)))
	s := fmt.Sprintf("%d", n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if v < 0 && n != 0 {
		return "-" + b.String()
	}
	return b.String()
}

func krw(v float64) string { return "₩" + comma(v) }

func signedKrw(v float64) string {
	if math.Round(v) > 0 {
		return "+₩" + comma(v)
	}
	if math.Round(v) < 0 {
		return "−₩" + comma(-v)
	}
	return "₩0"
}

func pct(rate float64) string {
	v := rate * 100
	switch {
	case v >= 0.005:
		return fmt.Sprintf("+%.2f%%", v)
	case v <= -0.005:
		return fmt.Sprintf("−%.2f%%", -v)
	}
	return "0.00%"
}

func pct0(rate float64) string { return fmt.Sprintf("%.0f%%", rate*100) }
func pct1(rate float64) string { return fmt.Sprintf("%.1f%%", rate*100) }

func qty(q float64) string {
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.8f", q), "0"), ".")
	if s == "" {
		return "0"
	}
	return s
}

func price(v float64, currency string) string {
	if currency == "USD" {
		return fmt.Sprintf("$%.2f", v)
	}
	return krw(v)
}
