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
	"github.com/choigonyok/jarvis/gocommon/site"
)

const (
	Name          = "vault"
	ServerName    = "vault"
	KindSave      = "vault.save_login"
	KindForget    = "vault.forget_login"
	KindSavePin   = "vault.save_payment_pin"
	KindForgetPin = "vault.forget_payment_pin"
)

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

// Tools is what the conversation may call without a card: list_logins names
// what is saved and never hands a secret out.
var Tools = []string{"mcp__" + ServerName + "__list_logins"}

type Module struct {
	client *Client
	log    *slog.Logger
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

// AcceptSecrets takes the card's Private fields at approval and saves them.
// It is the only place a password passes through in this process, and it
// keeps nothing.
func (m *Module) AcceptSecrets(ctx context.Context, a action.Action, secrets map[string]string) error {
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

// Handler is the model's side: list what is saved, and ask to save or forget
// (each a card - see Preview).
func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_logins",
		Description: "보관함에 저장된 로그인 정보의 사이트 목록(아이디는 일부만)과 저장된 결제 비밀번호(네이버페이·쿠페이)를 본다. 비밀번호는 볼 수 없다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		logins, err := m.client.List(ctx)
		if err != nil {
			return text(err.Error(), true), nil, nil
		}
		pins, _ := m.client.Pins(ctx)
		if len(logins) == 0 && len(pins) == 0 {
			return text("저장된 로그인 정보도 결제 비밀번호도 없습니다.", false), nil, nil
		}
		var b strings.Builder
		for _, l := range logins {
			fmt.Fprintf(&b, "- %s (%s): 아이디 %s, %s\n", l.Name, l.Domain, l.Hint, lastUsed(l.LastUsedAt))
		}
		for _, p := range pins {
			fmt.Fprintf(&b, "- 결제 비밀번호 %s (%s): 저장됨, %s\n", p.Name, p.Site, lastUsed(p.LastUsedAt))
		}
		return text(b.String(), false), nil, nil
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
