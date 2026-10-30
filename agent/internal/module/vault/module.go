// Package vault saves the operator's logins in vault-svc and lets the model
// use them without ever seeing one.
//
// Saving is a card: the model asks (save_login), the card has a username
// field and a password field, and what the operator types goes from the card
// straight to vault-svc (AcceptSecrets) - never into the card's record, the
// transcript or the model's context. Using one is the browser's job: its
// browser_fill_login tool reads the login of the page it is on from vault-svc
// and types it there. The model only ever learns that a login exists, under
// a hint (ab***), and that it was filled.
//
// A payment password (네이버페이, 쿠페이 - six digits) is kept the same way:
// save_payment_pin is a card with one secret field, and the browser's
// browser_enter_payment_pin reads it from vault-svc only for a keypad on that
// payment's own host. It is not a login: naver.com's login and 네이버페이's
// password are two things, so it has a place of its own.
//
// The orderer and shipping address (save_shipping_address) are typed into
// the card the same way - not into the conversation, and not kept in the
// card's record - but they are no secret from the model: an order sheet is
// typed from them, so shipping_address reads them back whole. Saved once,
// an order no longer asks for them (2026-10-29~30: six cards asked, and the
// answers that came back put a wrong phone number and a wrong building
// number on four order sheets).
package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/tool"
	"github.com/choigonyok/jarvis/gocommon/site"
)

const (
	Name          = "vault"
	ServerName    = "vault"
	KindSave      = "vault.save_login"
	KindForget    = "vault.forget_login"
	KindSavePin   = "vault.save_payment_pin"
	KindForgetPin = "vault.forget_payment_pin"
	// KindSaveShipping saves the orderer and shipping address; a card whose
	// fields go straight to vault-svc.
	KindSaveShipping   = "vault.save_shipping_address"
	KindForgetShipping = "vault.forget_shipping_address"
)

// DefaultShipping is the shipping profile an order uses.
const DefaultShipping = "기본"

// ShippingInput names a shipping profile.
type ShippingInput struct {
	Name string `json:"name,omitempty" jsonschema:"배송지 이름. 비우면 '기본'"`
}

func shippingName(raw json.RawMessage) string {
	var in ShippingInput
	_ = json.Unmarshal(raw, &in)
	if n := strings.TrimSpace(in.Name); n != "" {
		return n
	}
	return DefaultShipping
}

// Shipping is the saved shipping profile an order uses ('기본', or the only
// one), if there is one.
func (m *Module) Shipping(ctx context.Context) (Shipping, bool, error) {
	all, err := m.client.Profiles(ctx)
	if err != nil {
		return Shipping{}, false, err
	}
	for _, s := range all {
		if s.Name == DefaultShipping {
			return s, true, nil
		}
	}
	if len(all) == 1 {
		return all[0], true, nil
	}
	return Shipping{}, false, nil
}

// CheckAddress installs what says why a saved address may be out of date ("":
// it holds). The vault keeps the address and knows nothing of moves; mind knows
// of moves and never sees the address.
func (m *Module) CheckAddress(f func(ctx context.Context, saved string) string) { m.addressDoubt = f }

// AddressDoubt is why a saved address should be confirmed before it is used,
// "" when it need not be - or when nobody can say.
func (m *Module) AddressDoubt(ctx context.Context, s Shipping) string {
	if m.addressDoubt == nil || s.UpdatedAt == "" {
		return ""
	}
	return m.addressDoubt(ctx, s.UpdatedAt)
}

// ShippingText is a profile as an order sheet is filled from it.
func ShippingText(s Shipping) string {
	email := s.Email
	if email == "" {
		email = "(없음 - 이메일 칸이 꼭 필요하면 묻는다)"
	}
	return fmt.Sprintf("- 주문자·받는 분: %s\n- 연락처: %s\n- 이메일: %s\n- 우편번호: %s\n- 주소: %s\n- 상세주소: %s",
		s.Recipient, Phone(s.Phone), email, s.Zip, s.Address, s.Detail)
}

// Phone writes a mobile number as forms want it: 010-1234-5678.
func Phone(digits string) string {
	switch len(digits) {
	case 11:
		return digits[:3] + "-" + digits[3:7] + "-" + digits[7:]
	case 10:
		return digits[:3] + "-" + digits[3:6] + "-" + digits[6:]
	}
	return digits
}

// PinInput names whose payment password it is.
type PinInput struct {
	Pay string `json:"pay" jsonschema:"어느 결제의 비밀번호인지: '네이버페이' 또는 '쿠페이'"`
}

// payOf is the payment host a payment password is kept for, and its name.
// The host is the one its keypad is on (vault-svc's PinSites).
func payOf(raw json.RawMessage) (site, name string, err error) {
	var in PinInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", "", fmt.Errorf("입력을 읽지 못했습니다: %w", err)
	}
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(in.Pay), " ", "")) {
	case "네이버페이", "네이버", "naverpay", "npay", "naver", "pay.naver.com":
		return "pay.naver.com", "네이버페이", nil
	case "쿠페이", "쿠팡", "쿠팡페이", "coupay", "coupang", "coupang.com":
		return "coupang.com", "쿠팡 쿠페이", nil
	}
	return "", "", fmt.Errorf("어느 결제의 비밀번호인지 알 수 없습니다(%q): '네이버페이' 또는 '쿠페이'로 다시 요청하세요", in.Pay)
}

func (m *Module) pinSaved(ctx context.Context, site string) (Pin, bool) {
	pins, err := m.client.Pins(ctx)
	if err != nil {
		return Pin{}, false
	}
	for _, p := range pins {
		if p.Site == site {
			return p, true
		}
	}
	return Pin{}, false
}

type Module struct {
	client *Client
	log    *slog.Logger
	// addressDoubt says why a saved address may be out of date (CheckAddress).
	addressDoubt func(ctx context.Context, saved string) string
}

func New(client *Client, log *slog.Logger) *Module { return &Module{client: client, log: log} }

func (m *Module) Name() string                    { return Name }
func (m *Module) Start(ctx context.Context) error { return nil }
func (m *Module) Stop(ctx context.Context) error  { return nil }

// SiteInput names the site a login belongs to.
type SiteInput struct {
	Site string `json:"site" jsonschema:"로그인할 사이트의 주소나 도메인. 예: 'coupang.com', 'https://nid.naver.com'"`
	Name string `json:"name,omitempty" jsonschema:"운영자에게 보일 이름. 예: '쿠팡', '네이버'"`
}

func (m *Module) Specs() []action.Spec {
	return []action.Spec{
		{Kind: KindSave, Summary: "사이트의 로그인 정보(아이디·비밀번호)를 운영자에게 카드로 받아 보관함에 저장한다.", Reversible: true},
		{Kind: KindForget, Summary: "저장된 로그인 정보를 지운다.", Reversible: false},
		{Kind: KindSavePin, Summary: "결제 비밀번호(네이버페이·쿠페이 숫자 6자리)를 운영자에게 카드로 받아 보관함에 저장한다.", Reversible: true},
		{Kind: KindForgetPin, Summary: "저장된 결제 비밀번호를 지운다.", Reversible: false},
		{Kind: KindSaveShipping, Summary: "주문자·배송지(받는 분·연락처·이메일·주소)를 운영자에게 카드로 받아 보관함에 저장한다.", Reversible: true},
		{Kind: KindForgetShipping, Summary: "저장된 배송지를 지운다.", Reversible: false},
	}
}

func parse(a action.Action) (SiteInput, string, error) {
	var in SiteInput
	if err := json.Unmarshal(a.Input, &in); err != nil {
		return in, "", fmt.Errorf("입력을 읽지 못했습니다: %w", err)
	}
	domain, err := site.Domain(in.Site)
	if err != nil {
		return in, "", fmt.Errorf("어느 사이트인지 알 수 없습니다(%q): 'coupang.com' 같은 주소로 다시 요청하세요", in.Site)
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = domain
	}
	return in, domain, nil
}

func (m *Module) find(ctx context.Context, domain string) (Login, bool) {
	logins, err := m.client.List(ctx)
	if err != nil {
		return Login{}, false
	}
	for _, l := range logins {
		if l.Domain == domain {
			return l, true
		}
	}
	return Login{}, false
}

// Preview is the card. Saving is a form whose two fields are Private: their
// values go to AcceptSecrets and nowhere else.
func (m *Module) Preview(ctx context.Context, a action.Action) (action.Card, error) {
	switch a.Kind {
	case KindSavePin, KindForgetPin:
		return m.previewPin(ctx, a)
	case KindSaveShipping, KindForgetShipping:
		return m.previewShipping(ctx, a)
	}
	in, domain, err := parse(a)
	if err != nil {
		return action.Card{}, err
	}
	switch a.Kind {
	case KindSave:
		body := fmt.Sprintf("%s 와 그 하위 주소(예: login.%s)의 로그인 화면에서만 자동으로 넣습니다.\n"+
			"넣은 아이디·비밀번호는 암호화해서 보관하고, 자비스(모델)에게 보이지 않으며 대화 기록에도 남지 않습니다.", domain, domain)
		if old, ok := m.find(ctx, domain); ok {
			body += fmt.Sprintf("\n이미 저장된 정보(아이디 %s)를 새로 바꿉니다.", old.Hint)
		}
		return action.Card{
			Title:       "로그인 정보 저장: " + in.Name,
			Body:        body,
			Consequence: "승인하면 이 사이트의 로그인 화면에서 카드 없이 자동으로 로그인합니다.",
			Fields: []action.Field{
				{Key: "username", Label: "아이디", Kind: "text", Required: true, Private: true},
				{Key: "password", Label: "비밀번호", Kind: "secret", Required: true, Private: true},
			},
		}, nil
	case KindForget:
		body := domain + " 에 저장된 로그인 정보를 지웁니다."
		if old, ok := m.find(ctx, domain); ok {
			body = fmt.Sprintf("%s 에 저장된 로그인 정보(아이디 %s)를 지웁니다.", domain, old.Hint)
		}
		return action.Card{Title: "로그인 정보 지우기: " + in.Name, Body: body, Consequence: "지우면 이 사이트에 자동으로 로그인하지 않습니다."}, nil
	}
	return action.Card{}, fmt.Errorf("모르는 동작: %s", a.Kind)
}

// previewPin is a payment password's card: one secret field to save it, or
// a plain card to forget it.
func (m *Module) previewPin(ctx context.Context, a action.Action) (action.Card, error) {
	site, name, err := payOf(a.Input)
	if err != nil {
		return action.Card{}, err
	}
	_, saved := m.pinSaved(ctx, site)
	if a.Kind == KindForgetPin {
		return action.Card{Title: "결제 비밀번호 지우기: " + name, Body: name + " 결제 비밀번호를 보관함에서 지웁니다.",
			Consequence: "지우면 " + name + " 결제 비밀번호 키패드에 자동으로 넣지 않습니다."}, nil
	}
	body := fmt.Sprintf("%s 결제 비밀번호 키패드(%s 아래의 결제 화면)에서만 자동으로 넣습니다.\n"+
		"넣은 숫자는 암호화해서 보관하고, 자비스(모델)에게 보이지 않으며 대화 기록에도 남지 않습니다.\n"+
		"결제는 지금처럼 결제 승인 카드를 승인한 것만 진행됩니다.", name, site)
	if saved {
		body += "\n이미 저장된 비밀번호를 새로 바꿉니다."
	}
	return action.Card{
		Title:       "결제 비밀번호 저장: " + name,
		Body:        body,
		Consequence: "승인하면 승인된 결제의 비밀번호 키패드에서 이 비밀번호를 자동으로 넣습니다.",
		Fields: []action.Field{
			{Key: "pin", Label: "결제 비밀번호(숫자 6자리)", Kind: "secret", Required: true, Private: true},
		},
	}, nil
}

// previewShipping is the shipping profile's card: six fields typed by the
// operator, which go to vault-svc at approval and are not kept on the card.
func (m *Module) previewShipping(ctx context.Context, a action.Action) (action.Card, error) {
	name := shippingName(a.Input)
	var old *Shipping
	if all, err := m.client.Profiles(ctx); err == nil {
		for _, s := range all {
			if s.Name == name {
				old = &s
			}
		}
	}
	if a.Kind == KindForgetShipping {
		return action.Card{Title: "배송지 지우기: " + name, Body: "저장된 배송지(" + name + ")를 지웁니다.",
			Consequence: "지우면 주문할 때 배송지를 다시 묻습니다."}, nil
	}
	body := "주문서의 주문자·받는 분·연락처·이메일·배송지 칸에 이 정보를 그대로 넣습니다. 한 번 저장하면 주문할 때마다 묻지 않습니다.\n" +
		"주소는 도로명과 건물번호까지(예: 서울 중구 세종대로 110) - 주문서의 주소 검색이 이것으로 찾습니다.\n" +
		"보관함에 암호화해 두고, 자비스가 주문서를 채울 때 읽습니다."
	if old != nil {
		body += "\n지금 저장된 배송지(" + old.Address + " " + old.Detail + ")를 새로 바꿉니다."
	}
	return action.Card{
		Title:       "배송지 저장: " + name,
		Body:        body,
		Consequence: "승인하면 작업과 대화가 주문서를 이 정보로 채웁니다.",
		Fields: []action.Field{
			{Key: "recipient", Label: "받는 분(주문자) 이름", Kind: "text", Required: true, Private: true},
			{Key: "phone", Label: "연락처(휴대폰)", Kind: "text", Required: true, Private: true, Hint: "예: 010-1234-5678"},
			{Key: "email", Label: "이메일(선택)", Kind: "text", Private: true},
			{Key: "zip", Label: "우편번호(5자리)", Kind: "text", Required: true, Private: true},
			{Key: "address", Label: "주소(도로명 + 건물번호)", Kind: "text", Required: true, Private: true, Hint: "예: 서울 중구 세종대로 110"},
			{Key: "detail", Label: "상세주소(동·호수)", Kind: "text", Required: true, Private: true, Hint: "예: 101동 1001호"},
		},
	}, nil
}

// AcceptSecrets takes the card's Private fields at approval and saves them.
// It is the only place a password passes through in this process, and it
// keeps nothing.
func (m *Module) AcceptSecrets(ctx context.Context, a action.Action, secrets map[string]string) error {
	if a.Kind == KindSaveShipping {
		s := Shipping{
			Recipient: secrets["recipient"], Phone: secrets["phone"], Email: secrets["email"],
			Zip: secrets["zip"], Address: secrets["address"], Detail: secrets["detail"],
		}
		if err := m.client.SaveProfile(ctx, shippingName(a.Input), s); err != nil {
			return fmt.Errorf("저장하지 못했습니다: %w", err)
		}
		m.log.Info("배송지를 보관함에 넣었습니다", "name", shippingName(a.Input))
		return nil
	}
	if a.Kind == KindSavePin {
		site, name, err := payOf(a.Input)
		if err != nil {
			return err
		}
		pin := strings.TrimSpace(secrets["pin"])
		if len(pin) != 6 || strings.Trim(pin, "0123456789") != "" {
			return fmt.Errorf("결제 비밀번호는 숫자 6자리입니다")
		}
		if err := m.client.SavePin(ctx, site, name, pin); err != nil {
			return fmt.Errorf("저장하지 못했습니다: %w", err)
		}
		m.log.Info("결제 비밀번호를 보관함에 넣었습니다", "site", site)
		return nil
	}
	if a.Kind != KindSave {
		return fmt.Errorf("이 카드는 비밀 값을 받지 않습니다")
	}
	in, domain, err := parse(a)
	if err != nil {
		return err
	}
	username, password := strings.TrimSpace(secrets["username"]), secrets["password"]
	if username == "" || password == "" {
		return fmt.Errorf("아이디와 비밀번호를 모두 넣어 주세요")
	}
	if _, err := m.client.Save(ctx, domain, in.Name, username, password); err != nil {
		return fmt.Errorf("저장하지 못했습니다: %w", err)
	}
	m.log.Info("로그인 정보를 보관함에 넣었습니다", "domain", domain)
	return nil
}

func (m *Module) Execute(ctx context.Context, a action.Action) (action.Result, error) {
	switch a.Kind {
	case KindSaveShipping:
		name := shippingName(a.Input)
		all, err := m.client.Profiles(ctx)
		if err != nil {
			return action.Result{}, err
		}
		for _, s := range all {
			if s.Name == name {
				return action.Result{Note: fmt.Sprintf("배송지(%s)를 저장했습니다: %s %s. 주문서는 look 의 shipping_address 값을 그대로 넣습니다.", name, s.Address, s.Detail)}, nil
			}
		}
		return action.Result{}, fmt.Errorf("배송지(%s)가 보관함에 없습니다", name)
	case KindForgetShipping:
		name := shippingName(a.Input)
		if err := m.client.ForgetProfile(ctx, name); err != nil {
			return action.Result{}, err
		}
		return action.Result{Note: "배송지(" + name + ")를 지웠습니다."}, nil
	case KindSavePin:
		site, name, err := payOf(a.Input)
		if err != nil {
			return action.Result{}, err
		}
		if _, ok := m.pinSaved(ctx, site); !ok {
			return action.Result{}, fmt.Errorf("%s 결제 비밀번호가 보관함에 없습니다", name)
		}
		return action.Result{Note: fmt.Sprintf("%s 결제 비밀번호를 저장했습니다. 그 결제의 비밀번호 키패드에서 browser_enter_payment_pin 이 넣습니다. "+
			"숫자는 당신에게 보이지 않습니다.", name)}, nil
	case KindForgetPin:
		site, name, err := payOf(a.Input)
		if err != nil {
			return action.Result{}, err
		}
		if err := m.client.ForgetPin(ctx, site); err != nil {
			return action.Result{}, err
		}
		return action.Result{Note: name + " 결제 비밀번호를 지웠습니다."}, nil
	}
	in, domain, err := parse(a)
	if err != nil {
		return action.Result{}, err
	}
	switch a.Kind {
	case KindSave:
		l, ok := m.find(ctx, domain)
		if !ok {
			return action.Result{}, fmt.Errorf("%s 로그인 정보가 보관함에 없습니다", domain)
		}
		return action.Result{Note: fmt.Sprintf("%s(%s) 로그인 정보를 저장했습니다(아이디 %s). "+
			"이 사이트의 로그인 화면에서는 browser_fill_login 으로 넣을 수 있습니다. 비밀번호는 당신에게 보이지 않습니다.", in.Name, domain, l.Hint)}, nil
	case KindForget:
		if err := m.client.Forget(ctx, domain); err != nil {
			return action.Result{}, err
		}
		return action.Result{Note: fmt.Sprintf("%s(%s) 로그인 정보를 지웠습니다.", in.Name, domain)}, nil
	}
	return action.Result{}, fmt.Errorf("모르는 동작: %s", a.Kind)
}

// Look puts what is saved on the board: what names it, never a secret, and the
// address an order sheet is filled with.
func (m *Module) Look(b *tool.Board) {
	tool.Add(b, "logins", "보관함에 저장된 로그인 정보의 사이트 목록(아이디는 일부만)과 저장된 결제 비밀번호(네이버페이·쿠페이)를 본다. 비밀번호는 볼 수 없다.",
		func(ctx context.Context, _ struct{}) (string, error) {
			logins, err := m.client.List(ctx)
			if err != nil {
				return "", err
			}
			pins, _ := m.client.Pins(ctx)
			profiles, _ := m.client.Profiles(ctx)
			if len(logins) == 0 && len(pins) == 0 && len(profiles) == 0 {
				return "저장된 로그인 정보도 결제 비밀번호도 배송지도 없습니다.", nil
			}
			var s strings.Builder
			for _, l := range logins {
				fmt.Fprintf(&s, "- %s (%s): 아이디 %s, %s\n", l.Name, l.Domain, l.Hint, lastUsed(l.LastUsedAt))
			}
			for _, p := range pins {
				fmt.Fprintf(&s, "- 결제 비밀번호 %s (%s): 저장됨, %s\n", p.Name, p.Site, lastUsed(p.LastUsedAt))
			}
			for _, a := range profiles {
				fmt.Fprintf(&s, "- 배송지 %s: %s %s (값은 shipping_address)\n", a.Name, a.Address, a.Detail)
			}
			return s.String(), nil
		})
	tool.Add(b, "shipping_address", "주문서를 채울 주문자·받는 분·연락처·이메일·배송지를 보관함에서 읽는다. 주문서에는 이 값을 그대로 넣는다(주소 검색은 도로명+건물번호로 하고 우편번호가 같은 결과를 고른다). "+
		"저장된 배송지가 없으면 save_shipping_address 로 카드를 올린다 - 대화로 주소를 묻지 않는다.",
		func(ctx context.Context, _ struct{}) (string, error) {
			s, ok, err := m.Shipping(ctx)
			if err != nil {
				return "", err
			}
			if !ok {
				return "저장된 배송지가 없습니다. save_shipping_address 로 배송지 저장 카드를 올리세요(운영자가 카드 칸에 넣습니다).", nil
			}
			if why := m.AddressDoubt(ctx, s); why != "" {
				return "⚠ 확인 필요: " + why + ". 이 주소로 주문서를 채우기 전에 save_shipping_address 로 운영자에게 확인 카드를 올리세요.\n저장된 배송지(" + s.Name + "):\n" + ShippingText(s), nil
			}
			return "저장된 배송지(" + s.Name + "):\n" + ShippingText(s), nil
		})
}

// Handler is the model's side of changing what is saved: each asks for a card
// (see Preview).
func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "save_shipping_address",
		Description: "주문자·배송지(받는 분·연락처·이메일·우편번호·주소·상세주소)를 저장하도록 운영자에게 카드를 올린다. 운영자가 카드 칸에 넣는다 - 대화로 묻지 않는다. " +
			"저장된 배송지가 없거나 운영자가 배송지를 바꾸겠다고 하면 부른다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ShippingInput) (*mcp.CallToolResult, any, error) {
		raw, _ := json.Marshal(in)
		res, err := m.Execute(ctx, action.Action{Kind: KindSaveShipping, Input: raw})
		if err != nil {
			return text(err.Error(), true), nil, nil
		}
		return text(res.Note, false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "forget_shipping_address", Description: "저장된 배송지를 지운다."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ShippingInput) (*mcp.CallToolResult, any, error) {
			raw, _ := json.Marshal(in)
			res, err := m.Execute(ctx, action.Action{Kind: KindForgetShipping, Input: raw})
			if err != nil {
				return text(err.Error(), true), nil, nil
			}
			return text(res.Note, false), nil, nil
		})
	addWrite(server, m, "save_login", KindSave, "사이트의 로그인 정보를 저장하도록 운영자에게 카드를 올린다. 운영자가 카드의 칸에 아이디·비밀번호를 넣는다 - "+
		"대화로 비밀번호를 묻거나 받지 않는다. 저장되면 그 사이트 로그인 화면에서 browser_fill_login 이 넣는다.")
	addWrite(server, m, "forget_login", KindForget, "저장된 로그인 정보를 지운다.")
	addPin(server, m, "save_payment_pin", KindSavePin, "결제 비밀번호(네이버페이·쿠페이 숫자 6자리)를 저장하도록 운영자에게 카드를 올린다. "+
		"운영자가 카드의 비밀 칸에 넣는다 - 대화로 묻거나 받지 않는다. browser_enter_payment_pin 이 결제 비밀번호가 저장되어 있지 않다고 하거나, "+
		"운영자가 결제 비밀번호를 넣겠다고 하면 부른다. 저장되면 그 결제의 비밀번호 키패드에서 browser_enter_payment_pin 이 넣는다.")
	addPin(server, m, "forget_payment_pin", KindForgetPin, "저장된 결제 비밀번호를 지운다.")
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

func addPin(s *mcp.Server, m *Module, tool, kind, desc string) {
	mcp.AddTool(s, &mcp.Tool{Name: tool, Description: desc}, func(ctx context.Context, _ *mcp.CallToolRequest, in PinInput) (*mcp.CallToolResult, any, error) {
		raw, _ := json.Marshal(in)
		res, err := m.Execute(ctx, action.Action{Kind: kind, Input: raw})
		if err != nil {
			return text(err.Error(), true), nil, nil
		}
		return text(res.Note, false), nil, nil
	})
}

func lastUsed(at *time.Time) string {
	if at == nil {
		return "아직 안 씀"
	}
	return "마지막 사용 " + at.In(time.Local).Format("1/2 15:04")
}

func addWrite(s *mcp.Server, m *Module, tool, kind, desc string) {
	mcp.AddTool(s, &mcp.Tool{Name: tool, Description: desc}, func(ctx context.Context, _ *mcp.CallToolRequest, in SiteInput) (*mcp.CallToolResult, any, error) {
		raw, _ := json.Marshal(in)
		res, err := m.Execute(ctx, action.Action{Kind: kind, Input: raw})
		if err != nil {
			return text(err.Error(), true), nil, nil
		}
		return text(res.Note, false), nil, nil
	})
}

func text(s string, isErr bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isErr, Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// ConsoleHandler is the console's view (behind the agent's own token): GET
// /vault lists what is saved with its recent uses, DELETE /vault/{domain}
// forgets one. Neither ever carries a secret.
func (m *Module) ConsoleHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/vault":
			logins, err := m.client.List(r.Context())
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			uses, _ := m.client.Uses(r.Context(), 30)
			pins, _ := m.client.Pins(r.Context())
			_ = json.NewEncoder(w).Encode(map[string]any{"logins": logins, "pins": pins, "uses": uses})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/vault/pins/"):
			if err := m.client.ForgetPin(r.Context(), strings.TrimPrefix(r.URL.Path, "/vault/pins/")); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/vault/"):
			domain, err := site.Domain(strings.TrimPrefix(r.URL.Path, "/vault/"))
			if err == nil {
				err = m.client.Forget(r.Context(), domain)
			}
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "없는 경로입니다."})
		}
	})
}

// Has is whether a login for domain is saved.
func (m *Module) Has(ctx context.Context, domain string) bool {
	_, ok := m.find(ctx, domain)
	return ok
}
