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

func TestPhoneVerificationStopsIt(t *testing.T) {
	if !needsPerson("휴대폰에서 카카오톡 PC 로그인을 확인해 주세요") || !needsPerson("본인 확인이 필요합니다") {
		t.Fatal("휴대폰 확인 화면은 사람이 해야 합니다")
	}
	if needsPerson("비밀번호가 일치하지 않습니다") {
		t.Fatal("틀린 비밀번호는 휴대폰 확인이 아닙니다")
	}
}

func TestPolicyBacksOffThenGivesUp(t *testing.T) {
	var p policy
	now := time.Date(2026, 10, 22, 14, 6, 0, 0, time.UTC)
	if !p.due(now) {
		t.Fatal("처음에는 바로 시도합니다")
	}
	p.failed(now)
	if p.due(now.Add(time.Minute)) || !p.due(now.Add(2*time.Minute)) {
		t.Fatalf("첫 실패 뒤 2분 기다립니다: %+v", p)
	}
	p.failed(now)
	if p.due(now.Add(9*time.Minute)) || !p.due(now.Add(10*time.Minute)) {
		t.Fatalf("두 번째 실패 뒤 10분 기다립니다: %+v", p)
	}
	p.failed(now)
	if !p.GaveUp || p.due(now.Add(24*time.Hour)) {
		t.Fatalf("세 번 연속 실패하면 멈춥니다 (계정 잠김을 막으려고): %+v", p)
	}
	p.reset()
	if !p.due(now) || p.Fails != 0 {
		t.Fatal("다시 로그인되거나 비밀번호를 새로 넣으면 처음부터입니다")
	}
}
