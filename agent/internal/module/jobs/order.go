package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/vault"
)

// What a run that buys something needs besides the browser: the orderer and
// shipping address an order sheet is filled from, the cards that save one or
// a payment password, and the card alerts that say whether a payment went
// through.
//
// 2026-10-29~30 (장범준 LP): a run asked for the orderer and the address on
// six cards, the answers differed each time, and four order sheets went out
// with a wrong phone number or a building number one digit short; a run that found
// no payment password could only ask the conversation to raise its card; and
// whether a payment had gone through was asked of the operator twice, who
// looked it up by hand.

// VaultCards is the vault module's half: the saved shipping profile, and the
// cards that save one or a payment password.
type VaultCards interface {
	Shipping(ctx context.Context) (vault.Shipping, bool, error)
	// AddressDoubt is why a saved address should be confirmed first, "" if not.
	AddressDoubt(ctx context.Context, s vault.Shipping) string
	Preview(ctx context.Context, a action.Action) (action.Card, error)
}

// Charge is a card payment as its alert told it.
type Charge struct {
	At       time.Time
	Issuer   string
	Merchant string
	Amount   int64
	// Status is "" for a payment, otherwise what became of it (취소...).
	Status string
}

// Charges is what the card alerts say since a time, and whether they are
// still coming in: the alerts are KakaoTalk's, read on the Mac mini, and a
// logged-out KakaoTalk once stopped them for 33 hours (10/28 17:56).
type Charges struct {
	List        []Charge
	LastAlertAt *time.Time
	Stale       bool
}

// ChargeLog is where the card alerts are (spending-svc's book).
type ChargeLog interface {
	Charges(ctx context.Context, since time.Time) (Charges, error)
}

// SetVault gives runs the saved shipping profile and the vault's cards.
func (m *Module) SetVault(v VaultCards) { m.vault = v }

// SetCharges gives runs the card alerts.
func (m *Module) SetCharges(c ChargeLog) { m.charges = c }

type PinRequestInput struct {
	Pay    string `json:"pay" jsonschema:"어느 결제의 비밀번호인지: '네이버페이' 또는 '쿠페이'"`
	Reason string `json:"reason,omitempty" jsonschema:"왜 필요한지 한 줄. 예: '키패드에서 저장된 비밀번호가 없다고 함'"`
}

type ShippingRequestInput struct {
	Reason string `json:"reason,omitempty" jsonschema:"왜 필요한지 한 줄. 예: '주문서의 받는 분·주소 칸'"`
}

type AlertsInput struct {
	Hours int `json:"hours,omitempty" jsonschema:"몇 시간 전부터 볼지(1~72, 기본 6)"`
}

// vaultCard is a card of the vault's raised for a job: the vault module draws
// it and takes its fields at approval, the job waits on it (decided).
type vaultCard struct {
	Pay   string `json:"pay,omitempty"`
	Name  string `json:"name,omitempty"`
	JobID int64  `json:"jobId"`
}

func vaultKind(kind string) bool {
	return kind == vault.KindSavePin || kind == vault.KindSaveShipping
}

// vaultJob is the job a vault card was raised for; zero is the
// conversation's own card, which the permission gate looks after.
func vaultJob(p proposal.Proposal) int64 {
	if !vaultKind(p.Action.Kind) {
		return 0
	}
	var in vaultCard
	if json.Unmarshal(p.Action.Input, &in) != nil {
		return 0
	}
	return in.JobID
}

// decidedVault hands a job the decision on a vault card it raised, and wakes
// it. The card's fields went to vault-svc before this (the router's
// AcceptSecrets); what the job hears is only whether they did.
func (m *Module) decidedVault(ctx context.Context, p proposal.Proposal, d proposal.Decision) {
	job := vaultJob(p)
	if job == 0 {
		return
	}
	var in vaultCard
	_ = json.Unmarshal(p.Action.Input, &in)
	var text string
	switch {
	case p.Action.Kind == vault.KindSavePin && d == proposal.Approve:
		text = fmt.Sprintf("[승인] 결제 비밀번호 저장: %s - 보관함에 저장됐습니다. 이제 browser_enter_payment_pin 이 넣습니다. "+
			"결제 단계를 다시 진행하세요(결제창이면 결제 승인 카드가 다시 올라갈 수 있습니다).", in.Pay)
	case p.Action.Kind == vault.KindSavePin:
		text = fmt.Sprintf("[반려] 결제 비밀번호 저장: %s - 운영자가 저장하지 않았습니다. 비밀번호 없이는 결제를 이어 갈 수 없으니, 결제하지 말고 그렇게 finish 로 알리세요.", in.Pay)
	case d == proposal.Approve:
		text = "[승인] 배송지 저장 - 보관함에 저장됐습니다. shipping_address 로 읽어 주문서에 그대로 넣고 이어 가세요."
	default:
		text = "[반려] 배송지 저장 - 운영자가 배송지를 저장하지 않았습니다. 주문서를 채울 수 없으니 ask 로 무엇이 필요한지 알리세요."
	}
	if err := m.store.Answer(ctx, job, p.ID, text); err != nil {
		if JobEnded(err) {
			m.endedAnswer(p.ID, d)
			return
		}
		state := proposal.Approved
		if d != proposal.Approve {
			state = proposal.Rejected
		}
		m.proposals.Settle(p.ID, state, "작업에 아직 전하지 못해 곧 다시 전합니다: "+err.Error())
		m.retrySoon()
		return
	}
	if d == proposal.Approve {
		m.proposals.Settle(p.ID, proposal.Executed, "저장했습니다. 작업이 이어서 합니다.")
	} else {
		m.proposals.Settle(p.ID, proposal.Rejected, "작업에 반려로 전했습니다.")
	}
	m.wake()
}

// raiseVault opens a vault card for the job's run and has the job wait on it.
func (m *Module) raiseVault(ctx context.Context, run int64, j Job, kind string, in vaultCard, what string) (*mcp.CallToolResult, error) {
	if m.vault == nil {
		return toolText("이 에이전트에는 보관함이 없어 카드를 올릴 수 없습니다. ask 로 알리세요.", true), nil
	}
	in.JobID = j.ID
	raw, _ := json.Marshal(in)
	act := action.Action{Kind: kind, Input: raw}
	card, err := m.vault.Preview(ctx, act)
	if err != nil {
		return toolText(err.Error(), true), nil
	}
	card.Title = card.Title + " · 작업: " + j.Title
	if open, ok := m.proposals.FindPendingWhere(func(p proposal.Proposal) bool {
		return p.Action.Kind == kind && vaultJob(p) == j.ID
	}); ok {
		if err := m.store.Wait(ctx, run, open.ID, what); err != nil {
			return toolText(err.Error(), true), nil
		}
		m.current.park(run, open.ID)
		m.current.decide(run)
		return toolText("같은 카드가 이미 운영자에게 올라가 있습니다. 작업은 그 카드를 기다립니다. 지금 실행을 마치세요.", false), nil
	}
	p, _ := m.proposals.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: act, Card: card})
	if err := m.store.Wait(ctx, run, p.ID, what); err != nil {
		m.proposals.Abandon(p.ID, "작업에 걸지 못했습니다: "+err.Error())
		return toolText(err.Error(), true), nil
	}
	m.current.park(run, p.ID)
	m.current.decide(run)
	m.attach(p.ID)
	return toolText("카드를 올렸습니다. 운영자가 카드 칸에 넣고 승인하면(또는 반려하면) 작업이 곧바로 다시 실행됩니다. 지금 실행을 마치세요.", false), nil
}

// addOrderTools are the run's tools for an order: the shipping profile, the
// cards that save it or a payment password, and the card alerts.
func (m *Module) addOrderTools(server *mcp.Server, run int64, guard func() (Job, *mcp.CallToolResult), payUp func(Job) *mcp.CallToolResult) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "shipping_address",
		Description: "주문서에 넣을 주문자·받는 분·연락처·이메일·배송지를 보관함에서 읽는다. 주문서는 이 값을 그대로 넣는다 - ask 로 묻지 않는다. " +
			"주소 검색은 저장된 도로명 주소(건물번호까지)로 하고 우편번호가 같은 결과를 고른다. 없으면 request_shipping_address 를 부른다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, any, error) {
		if _, bad := guard(); bad != nil {
			return bad, nil, nil
		}
		if m.vault == nil {
			return toolText("이 에이전트에는 보관함이 없습니다. 주문자·배송지가 필요하면 ask 로 묻습니다.", true), nil, nil
		}
		s, ok, err := m.vault.Shipping(ctx)
		if err != nil {
			return toolText("보관함을 읽지 못했습니다: "+err.Error(), true), nil, nil
		}
		if !ok {
			return toolText("저장된 배송지가 없습니다. request_shipping_address 를 불러 배송지 저장 카드를 올리고 실행을 마치세요 - 주소를 ask 로 묻지 않습니다.", false), nil, nil
		}
		if why := m.vault.AddressDoubt(ctx, s); why != "" {
			return toolText("⚠ 확인 필요: "+why+". 주문서를 채우지 말고 request_shipping_address 로 배송지 확인 카드를 올린 뒤 실행을 마치세요.\n"+
				"저장된 배송지("+s.Name+"):\n"+vault.ShippingText(s), false), nil, nil
		}
		return toolText("저장된 배송지("+s.Name+") - 주문서에 이대로 넣습니다:\n"+vault.ShippingText(s)+
			"\n결제 직전 report 의 배송지는 이 주소를 그대로 적습니다.", false), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "request_shipping_address",
		Description: "보관함에 배송지가 없을 때 운영자에게 배송지 저장 카드(받는 분·연락처·이메일·우편번호·주소·상세주소 칸)를 올린다. 카드 칸의 값은 보관함으로 가고, " +
			"승인되면 작업이 곧바로 다시 실행되어 shipping_address 로 읽는다. 부른 뒤에는 바로 실행을 마친다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ShippingRequestInput) (*mcp.CallToolResult, any, error) {
		j, bad := guard()
		if bad != nil {
			return bad, nil, nil
		}
		if up := payUp(j); up != nil {
			return up, nil, nil
		}
		res, err := m.raiseVault(ctx, run, j, vault.KindSaveShipping, vaultCard{Name: vault.DefaultShipping}, "배송지 저장")
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		if r := strings.TrimSpace(in.Reason); r != "" {
			_ = m.store.Step(ctx, run, "step", "배송지 저장 카드: "+r)
		}
		return res, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "request_payment_pin",
		Description: "browser_enter_payment_pin 이 결제 비밀번호가 저장되어 있지 않다고 할 때 운영자에게 결제 비밀번호 저장 카드(비밀 칸)를 올린다. " +
			"숫자는 카드에서 보관함으로만 가고 작업에게는 보이지 않는다. 승인되면 작업이 곧바로 다시 실행된다. 부른 뒤에는 바로 실행을 마친다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in PinRequestInput) (*mcp.CallToolResult, any, error) {
		j, bad := guard()
		if bad != nil {
			return bad, nil, nil
		}
		res, err := m.raiseVault(ctx, run, j, vault.KindSavePin, vaultCard{Pay: in.Pay}, "결제 비밀번호 저장")
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		if r := strings.TrimSpace(in.Reason); r != "" {
			_ = m.store.Step(ctx, run, "step", "결제 비밀번호 저장 카드: "+r)
		}
		return res, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "payment_alerts",
		Description: "최근 카드 결제 알림(카드사 카톡 알림을 가계부가 모은 것)을 본다: 시각·카드사·금액·가맹점. 결제가 됐는지 애매할 때(인증은 됐는데 주문 완료가 안 보임 등) " +
			"운영자에게 묻기 전에 쇼핑몰 주문내역과 함께 먼저 본다. 읽기만 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in AlertsInput) (*mcp.CallToolResult, any, error) {
		if _, bad := guard(); bad != nil {
			return bad, nil, nil
		}
		if m.charges == nil {
			return toolText("이 에이전트에서는 결제 알림을 볼 수 없습니다. 쇼핑몰 주문내역으로 확인하세요.", true), nil, nil
		}
		hours := in.Hours
		if hours <= 0 {
			hours = 6
		}
		hours = min(hours, 72)
		got, err := m.charges.Charges(ctx, time.Now().Add(-time.Duration(hours)*time.Hour))
		if err != nil {
			return toolText("결제 알림을 읽지 못했습니다: "+err.Error(), true), nil, nil
		}
		return toolText(renderCharges(got, hours), false), nil, nil
	})
}

// renderCharges is the card alerts as a run reads them, with a word on
// whether they are still coming in.
func renderCharges(c Charges, hours int) string {
	var b strings.Builder
	if len(c.List) == 0 {
		fmt.Fprintf(&b, "최근 %d시간 동안 들어온 카드 결제 알림이 없습니다.\n", hours)
	} else {
		fmt.Fprintf(&b, "최근 %d시간의 카드 결제 알림(%d건):\n", hours, len(c.List))
		for _, ch := range c.List {
			line := fmt.Sprintf("- %s %s %s · %s", ch.At.Local().Format("1/2 15:04"), ch.Issuer, won(ch.Amount), ch.Merchant)
			if ch.Status != "" {
				line += " (" + ch.Status + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	switch {
	case c.Stale:
		b.WriteString("주의: 카드 알림 수집이 멈춰 있을 수 있습니다(카톡 수집 지연) - 알림이 없다고 결제가 안 된 것은 아닙니다. 쇼핑몰 주문내역을 기준으로 보고, 그래도 모르면 ask 로 알리세요.")
	case c.LastAlertAt != nil:
		fmt.Fprintf(&b, "마지막 카드 알림: %s. 결제 직후의 알림은 1~2분 늦게 들어올 수 있습니다.", c.LastAlertAt.Local().Format("1/2 15:04"))
	}
	return b.String()
}

// won writes an amount as a card alert says it: 113000 is 113,000원.
func won(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String() + "원"
	}
	return b.String() + "원"
}
