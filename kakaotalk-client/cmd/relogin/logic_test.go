//go:build darwin

package main

import (
	"testing"
	"time"
)

func n(role, sub, title string, kids ...*elem) *elem {
	return &elem{Role: role, Subrole: sub, Title: title, Enabled: 1, Kids: kids}
}

func text(v string) *elem { return &elem{Role: "AXStaticText", Value: v, Enabled: 1} }

// KakaoTalk's login window, as near as the accessibility tree is known: an
// account field, a secure one, 자동 로그인, and buttons that do not all log in.
func loginWindow() *elem {
	return n("AXWindow", "AXStandardWindow", "카카오톡",
		n("AXTextField", "", ""),
		n("AXTextField", "AXSecureTextField", ""),
		n("AXCheckBox", "", "자동 로그인"),
		n("AXButton", "", "QR코드 로그인"),
		n("AXButton", "", "카카오계정 찾기"),
		n("AXButton", "", "로그인"),
	)
}

func TestClassifyTellsTheLoginWindow(t *testing.T) {
	v := classify([]*elem{loginWindow()})
	if v.Form == nil || v.Form.Password == nil || v.Form.Account == nil || v.Form.Auto == nil {
		t.Fatalf("로그인 창을 알아봐야 합니다: %+v", v.Form)
	}
	if v.Form.Login == nil || v.Form.Login.Title != "로그인" {
		t.Fatalf("로그인 버튼은 '로그인' 이어야 합니다 (QR·찾기 말고): %+v", v.Form.Login)
	}
	if v.loggedIn() {
		t.Fatal("로그인 창이 떠 있으면 로그인된 게 아닙니다")
	}
}

func TestClassifyFindsTheNoticeOverIt(t *testing.T) {
	sheet := n("AXSheet", "", "", text("다른 PC에서 로그인하여 로그아웃되었습니다."), n("AXButton", "", "확인"))
	w := loginWindow()
	w.Kids = append(w.Kids, sheet)
	v := classify([]*elem{w})
	if len(v.Dialogs) != 1 || v.Dialogs[0].OK == nil || v.Dialogs[0].Text != "다른 PC에서 로그인하여 로그아웃되었습니다." {
		t.Fatalf("로그인 창 위의 안내를 찾아야 합니다: %+v", v.Dialogs)
	}
	if !safeToConfirm(v.Dialogs[0], v.Form != nil) {
		t.Fatal("로그아웃 안내의 확인은 눌러도 됩니다")
	}
}

func TestOnlyLogoutNoticesAreConfirmedWhenNoLoginWindow(t *testing.T) {
	other := dialog{Text: "이 대화방을 나가시겠습니까?", OK: n("AXButton", "", "확인")}
	if safeToConfirm(other, false) {
		t.Fatal("로그인과 상관없는 확인은 누르지 않습니다")
	}
	if !safeToConfirm(dialog{Text: "네트워크 연결이 끊어져 로그아웃되었습니다"}, false) {
		t.Fatal("로그아웃 안내는 눌러야 합니다")
	}
}

func TestClassifyLoggedIn(t *testing.T) {
	main := n("AXWindow", "AXStandardWindow", "카카오톡", n("AXTextField", "", "검색"), n("AXButton", "", "채팅"))
	v := classify([]*elem{main})
	if !v.loggedIn() || v.Form != nil {
		t.Fatalf("비밀번호 칸도 안내도 없으면 로그인된 것입니다: %+v", v)
	}
}

// While the screen is locked every window comes back as the app itself and is
// left out: that is no window that could be logged in (2026-10-23 it read as
// logged in for eleven hours while KakaoTalk was logged out).
func TestBlindWindowsAreNotLoggedIn(t *testing.T) {
	for _, v := range []view{{Blind: true}, {Blind: true, Windows: 1}} {
		if v.loggedIn() {
			t.Fatalf("창을 읽지 못했으면 로그인된 게 아닙니다: %+v", v)
		}
	}
}

func menuItem(title string, enabled int) *elem {
	return &elem{Role: "AXMenuItem", Title: title, Enabled: enabled}
}

// The menu bar as KakaoTalk shows it: the Apple menu first, whose "<user>
// 로그아웃…" logs the Mac's user out, then its own app menu.
func menuBar(logout int) []*elem {
	return []*elem{
		n("AXMenuBarItem", "", "Apple", n("AXMenu", "", "", menuItem("홍길동 로그아웃…", 1), menuItem("홍길동 로그아웃", 1))),
		n("AXMenuBarItem", "", "카카오톡", n("AXMenu", "", "",
			menuItem("KakaoTalk 정보", 1), menuItem("환경설정...", logout), menuItem("로그아웃", logout), menuItem("KakaoTalk 종료", 1))),
		n("AXMenuBarItem", "", "창", n("AXMenu", "", "", menuItem("친구", logout))),
	}
}

func TestMenuTellsTheLogin(t *testing.T) {
	if got := menuFrom(menuBar(1)); got != menuIn {
		t.Fatalf("로그아웃을 고를 수 있으면 로그인돼 있습니다: %v", got)
	}
	if got := menuFrom(menuBar(0)); got != menuOut {
		t.Fatalf("로그아웃이 꺼져 있으면 로그인돼 있지 않습니다(애플 메뉴의 사용자 로그아웃은 보지 않음): %v", got)
	}
	noApp := menuBar(1)[:1]
	if got := menuFrom(noApp); got != menuUnknown {
		t.Fatalf("카카오톡 메뉴가 없으면 모릅니다: %v", got)
	}
	noItem := []*elem{n("AXMenuBarItem", "", "카카오톡", n("AXMenu", "", "", menuItem("KakaoTalk 정보", 1)))}
	if got := menuFrom(noItem); got != menuUnknown {
		t.Fatalf("로그아웃 항목이 없으면 모릅니다: %v", got)
	}
}

func TestLockedScreenSaysWhatItCannotDo(t *testing.T) {
	for m, want := range map[menuState]string{menuIn: "locked-in", menuOut: "locked", menuUnknown: "locked-unknown"} {
		if got, detail := lockedState(m); got != want || detail == "" {
			t.Fatalf("잠긴 화면에서 로그아웃 메뉴 %v 는 %s 여야 합니다: %s %q", m, want, got, detail)
		}
	}
}

// The Mac says its screen is locked whenever the display sleeps, password or
// not: a sleeping display is woken for a logout. Behind a lock that wants a
// password it is not woken again if it stays locked; behind one that wants
// none it is woken again every wakeEvery - one wake that did not take once
// stopped it for 33 hours (10/28 17:56 to 10/30 02:40).
func TestSleepingDisplayIsWokenForALogout(t *testing.T) {
	never := time.Since(time.Time{})
	for _, c := range []struct {
		m             menuState
		password      bool
		useless       bool
		sinceWake     time.Duration
		want, because string
	}{
		{menuIn, false, false, never, "in", "로그인돼 있고 잠금에 비밀번호가 없으면 화면이 꺼져 있을 뿐입니다"},
		{menuIn, true, false, never, "locked-in", "비밀번호 잠금 뒤의 로그인은 로그아웃되면 기다려야 합니다"},
		{menuOut, false, false, never, "wake", "로그아웃이면 화면을 깨웁니다"},
		{menuOut, true, false, never, "wake", "비밀번호가 있다고 해도 한 번은 깨워 봅니다"},
		{menuOut, false, false, 5 * time.Second, "waking", "깨운 직후에는 잠금이 풀리기를 기다립니다"},
		{menuOut, true, false, time.Minute, "stays-locked", "비밀번호 잠금이 깨운 뒤에도 잠겨 있으면 다시 깨우지 않습니다"},
		{menuOut, false, false, time.Minute, "retry", "비밀번호 없는 잠금이 깨운 뒤에도 잠겨 있으면 그만두지 않고 기다립니다"},
		{menuOut, false, false, wakeEvery, "wake", "비밀번호 없는 잠금은 wakeEvery 가 지나면 다시 깨웁니다"},
		{menuOut, false, true, never, "locked", "깨워도 소용없었으면 다시 깨우지 않습니다"},
		{menuUnknown, false, false, never, "locked-unknown", "메뉴를 모르면 깨우지 않습니다"},
	} {
		if got := whileLocked(c.m, c.password, c.useless, c.sinceWake); got != c.want {
			t.Fatalf("%s: %s 여야 하는데 %s", c.because, c.want, got)
		}
	}
}

func TestPhoneVerificationStopsIt(t *testing.T) {
	if !needsPerson("휴대폰에서 카카오톡 PC 로그인을 확인해 주세요") || !needsPerson("본인 확인이 필요합니다") {
		t.Fatal("휴대폰 확인 화면은 사람이 해야 합니다")
	}
	if needsPerson("비밀번호가 일치하지 않습니다") {
		t.Fatal("틀린 비밀번호는 휴대폰 확인이 아닙니다")
	}
}

func TestWrongPasswordStopsAtOnce(t *testing.T) {
	if !wrongPassword("비밀번호가 일치하지 않습니다. 다시 입력해 주세요.") || !wrongPassword("계정 또는 비밀번호를 확인해 주세요") {
		t.Fatal("틀린 비밀번호 안내를 알아봐야 합니다")
	}
	if wrongPassword("네트워크 연결을 확인해 주세요") {
		t.Fatal("네트워크 안내는 틀린 비밀번호가 아닙니다")
	}
	if !needsPerson("계정이 보호조치되었습니다") {
		t.Fatal("잠긴 계정은 사람이 해야 합니다")
	}
}

func TestPolicyRetriesSoonAndKeepsTrying(t *testing.T) {
	var p policy
	now := time.Date(2026, 10, 22, 14, 6, 0, 0, time.UTC)
	if !p.due(now) {
		t.Fatal("로그아웃을 보면 바로 시도합니다")
	}
	p.failed(now)
	if p.due(now.Add(9*time.Second)) || !p.due(now.Add(10*time.Second)) {
		t.Fatalf("첫 실패 뒤 10초면 다시 시도합니다: %+v", p)
	}
	for i := 0; i < 10; i++ {
		p.failed(now)
	}
	if p.GaveUp || !p.due(now.Add(5*time.Minute)) {
		t.Fatalf("인터넷 탓 실패는 멈추지 않고 5분보다 길게 기다리지 않습니다: %+v", p)
	}
	p.stop()
	if !p.GaveUp || p.due(now.Add(24*time.Hour)) {
		t.Fatalf("틀린 비밀번호·휴대폰 확인은 멈춥니다: %+v", p)
	}
	p.reset()
	if !p.due(now) || p.Fails != 0 {
		t.Fatal("다시 로그인되거나 비밀번호를 새로 넣으면 처음부터입니다")
	}
}
