package assemble

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/browsercheck"
	"github.com/choigonyok/jarvis/agent/internal/claudecode"
	"github.com/choigonyok/jarvis/agent/internal/config"
	"github.com/choigonyok/jarvis/agent/internal/core/run"
	"github.com/choigonyok/jarvis/agent/internal/module/jobs"
	"github.com/choigonyok/jarvis/agent/internal/module/spending"
	"github.com/choigonyok/jarvis/agent/internal/module/vault"
	"github.com/choigonyok/jarvis/gocommon/notify"
	"github.com/choigonyok/jarvis/gocommon/site"
)

// loginSites are the order histories the login check opens. None is on a
// host the payment guard asks about (browser/payment-hosts.json): the guard
// stops every write there, the page's own too, and with nobody behind the
// check it is the operator's screen that gets the cards. 네이버페이's history
// raised two at its first look, for its own point lookups. Its sign-in is
// read off what the order lookups ran into instead (Say).
var loginSites = []browsercheck.Site{
	{ID: "coupang", Name: "쿠팡 주문 내역", Orders: "https://mc.coupang.com/ssr/desktop/order/list", LoginHost: "login.coupang.com"},
}

// loginCheck is the shops' login check on the shared browser at browserURL:
// it opens their order histories when nothing else runs here, signs a site
// that has logged out back in with what the vault keeps, says so when it
// cannot, and has the book's lookups go on once a site is signed in again.
func loginCheck(cfg *config.Config, runner *claudecode.Runner, vaultModule *vault.Module, spendingModule *spending.Module, browserURL string, log *slog.Logger) *browsercheck.Checker {
	checker := &browsercheck.Checker{
		Client: browsercheck.New(browserURL, cfg.ExtraMCPToken),
		Sites:  loginSites,
		// 다른 탭이 로그인 화면이면(누가 비밀번호를 치는 중일 수 있다) 열지 않는다.
		BusyHosts: []string{"login.coupang.com", "nid.naver.com", "pay.naver.com"},
		Every:     30 * time.Minute,
		WhenDown:  6 * time.Hour,
		Idle:      runner.Idle,
		Log:       log,
	}
	// 풀린 로그인은 보관함의 정보로 다시 한다. 안 되면(문자 인증 등) 알린다.
	if vaultModule != nil {
		// What the relogin task last said it saw, by site - the reason a
		// login did not take ("보안문자", "비밀번호 오류") belongs in the
		// notice. The checker calls Relogin and then StillOut from its one
		// loop, so nothing else touches this map.
		reloginSaid := map[string]string{}
		checker.Relogin = relogin(runner, vaultModule, browserURL, cfg.ExtraMCPToken, func(id, said string) {
			log.Info("재로그인 작업이 말한 결과", "site", id, "result", clip(said, 300))
			reloginSaid[id] = said
		})
		checker.CanRelogin = func(ctx context.Context, s browsercheck.Site) bool {
			domain, err := site.Domain(s.LoginHost)
			return err == nil && vaultModule.Has(ctx, domain)
		}
		loginNote := notify.New(cfg.NotifyURL, cfg.DatabaseURL, cfg.APIToken, log)
		checker.StillOut = func(s browsercheck.Site, why string) {
			if said := strings.TrimSpace(reloginSaid[s.ID]); said != "" {
				why += " · 재로그인 작업: " + clip(said, 160)
			}
			loginNote.Send(notify.Event{
				Source: "agent", Kind: "browser.login", Tier: "now", Level: "warn",
				Title: s.ID + " 자동 로그인이 안 됐어요",
				Body:  why + " - 화면 탭에서 직접 로그인해 주세요.",
				URL:   "/screen", Key: "relogin:" + s.ID + ":" + time.Now().Format("2006-01-02T15"),
			})
		}
	}
	// Signed in again (a relogin, or by hand on the 화면 tab): the book's
	// lookups that stopped at the login page go on by themselves.
	if spendingModule != nil {
		checker.SignedIn = func(ctx context.Context, s browsercheck.Site) {
			if n, err := spendingModule.Resume(ctx, s.ID); err != nil {
				log.Warn("로그인 뒤 멈춘 주문 조회를 다시 잇지 못했습니다", "site", s.ID, "err", err)
			} else if n > 0 {
				log.Info("로그인돼 있어 멈췄던 주문 조회를 다시 잇습니다", "site", s.ID, "count", n)
			}
		}
	}
	return checker
}

// relogin signs a logged-out site back in with the login the vault keeps: a
// background task, held to that site's domain, that opens the page, has the
// browser fill the login (browser_fill_login - the model never sees it, and
// it turns on the form's 자동 로그인 so the login outlives the browser) and
// presses the button. Anything beyond a plain login - a code by text, a
// captcha, a new-device check - it leaves for the operator. said hears the
// task's own one-line account of what it saw.
func relogin(bg run.Background, v *vault.Module, browserURL, browserToken string, said func(site, result string)) func(context.Context, browsercheck.Site) error {
	return func(ctx context.Context, s browsercheck.Site) error {
		domain, err := site.Domain(s.LoginHost)
		if err != nil {
			return err
		}
		if !v.Has(ctx, domain) {
			return fmt.Errorf("%s 에 저장된 로그인 정보가 없습니다", domain)
		}
		guard, err := jobs.NewBridgeGuard(browserURL, browserToken)
		if err != nil {
			return err
		}
		tools := []string{}
		for _, t := range []string{"browser_navigate", "browser_get_state", "browser_click", "browser_fill_login", "browser_screenshot", "browser_list_tabs", "browser_switch_tab", "browser_close_tab"} {
			tools = append(tools, "mcp__"+jobs.BrowserServer+"__"+t)
		}
		result, err := bg.Background(ctx, run.Task{
			Name: "재로그인 " + s.ID,
			Prompt: fmt.Sprintf("%s(%s) 로그인이 풀렸습니다. 다시 로그인하세요.\n"+
				"1. browser_navigate 로 %s 를 엽니다. 로그인 화면으로 넘어갑니다.\n"+
				"2. browser_fill_login 으로 저장된 아이디·비밀번호를 넣습니다(값은 당신에게 보이지 않습니다). 자동 로그인 칸은 이 도구가 켭니다.\n"+
				"3. 로그인 버튼을 한 번 누릅니다. 로그인 뒤 원래 페이지로 넘어가는 데 몇 초 걸리니, 몇 초 간격으로 browser_get_state 를 두세 번 보며 주소가 로그인 화면을 벗어났는지 확인합니다. "+
				"'잠시만 기다려 주세요' 같은 확인 화면은 저절로 넘어갈 때까지 기다립니다(누르지 않습니다).\n"+
				"4. 넘어간 것을 본 뒤에 연 탭을 닫고, 결과를 한 줄로 씁니다: 로그인됨, 또는 무엇이 막았는지(보안문자, 문자 인증, 오류 문구 그대로).", s.ID, domain, s.Orders),
			System: "당신은 Jarvis의 백그라운드 재로그인입니다. 아무도 보지 않는 채로, 운영자의 계정이 로그인된 브라우저를 씁니다.\n" +
				"- 로그인 말고는 아무것도 누르지 않습니다. 주문·결제·설정·광고는 건드리지 않습니다.\n" +
				"- 문자·이메일 인증, 보안문자, 새 기기 확인, 비밀번호 변경 안내가 나오면 더 누르지 말고 멈춘 뒤 그 사실을 한 줄로 씁니다.\n" +
				"- 로그인 버튼은 한 번만 누릅니다. 실패하면 다시 넣거나 다시 누르지 않습니다(계정이 잠길 수 있습니다).",
			Extra:   []string{jobs.BrowserServer},
			Allowed: tools,
			Timeout: 5 * time.Minute,
			Browser: true,
			// Held to the site only while the task has the browser, and let go
			// before anyone else gets it.
			Hold: func(hctx context.Context) error {
				if err := guard.Restrict(hctx, []string{domain}, 6*time.Minute); err != nil {
					return fmt.Errorf("브라우저를 %s 안에 묶지 못했습니다: %w", domain, err)
				}
				return nil
			},
			Done: func() { _ = guard.Release(context.WithoutCancel(ctx)) },
		})
		if said != nil {
			said(s.ID, result)
		}
		return err
	}
}

// browserHandover is how long the conversation's first browser call waits
// for a background task on the browser to let go: its process to end and its
// site guard to come off. Seconds, as a rule.
const browserHandover = 20 * time.Second

// clip shortens s to n runes for a log line or a notice.
func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
