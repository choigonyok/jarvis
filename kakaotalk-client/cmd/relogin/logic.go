//go:build darwin

package main

import (
	"strings"
	"time"
)

// elem is one element of KakaoTalk's accessibility tree, as read in one look.
// ref is the platform's handle (ax.go); the logic here never touches it.
type elem struct {
	Role, Subrole, Title, Desc string
	// Value is read for text and checkboxes only - never for a secure field.
	Value   string
	Enabled int // 1, 0, or -1 when the element does not say
	Kids    []*elem
	ref     axRef
}

// each walks e and everything under it, depth first.
func (e *elem) each(fn func(*elem)) {
	fn(e)
	for _, k := range e.Kids {
		k.each(fn)
	}
}

// text is every static text under e, in order.
func (e *elem) text() string {
	var parts []string
	e.each(func(x *elem) {
		if x.Role == "AXStaticText" {
			for _, s := range []string{x.Value, x.Title, x.Desc} {
				if s = strings.TrimSpace(s); s != "" {
					parts = append(parts, s)
					break
				}
			}
		}
	})
	return strings.Join(parts, " ")
}

func (e *elem) label() string {
	if e.Title != "" {
		return e.Title
	}
	return e.Desc
}

// form is KakaoTalk's login window: the password field is what tells it apart.
type form struct {
	Window   *elem
	Account  *elem // the account field, when there is one
	Password *elem
	Login    *elem // the 로그인 button, when it can be told apart
	Auto     *elem // the 자동 로그인 checkbox, when there is one
}

// dialog is a window or sheet with a 확인 button on it - a notice.
type dialog struct {
	Window *elem
	Text   string
	OK     *elem
}

// view is what KakaoTalk is showing.
type view struct {
	Form    *form
	Dialogs []dialog
	// Windows is how many windows it has open at all.
	Windows int
	// Blind is a window that came back as the app itself: macOS hides every
	// window from an accessibility client that way while the screen is
	// locked. Nothing about the login can be read from the windows then.
	Blind bool
}

func (v view) loggedIn() bool {
	return !v.Blind && v.Form == nil && len(v.Dialogs) == 0 && v.Windows > 0
}

// menuState is what KakaoTalk's own app menu says of the login: its 로그아웃
// can be chosen only while it is logged in. A notice it waits on greys the
// menu out too, so out means "not plainly in" and the windows tell which.
type menuState int

const (
	menuUnknown menuState = iota // no 로그아웃 in the app menu to read
	menuIn
	menuOut
)

func (m menuState) String() string {
	switch m {
	case menuIn:
		return "켜짐"
	case menuOut:
		return "꺼짐"
	}
	return "모름"
}

// appMenus are the titles of KakaoTalk's app menu in the menu bar - never
// the Apple menu, whose "<user> 로그아웃…" logs the Mac's user out.
var appMenus = []string{"카카오톡", "KakaoTalk"}

// logoutItems are the app menu's 로그아웃, by the whole title.
var logoutItems = []string{"로그아웃", "Log Out", "Logout"}

func isAppMenu(title string) bool {
	for _, t := range appMenus {
		if strings.TrimSpace(title) == t {
			return true
		}
	}
	return false
}

// menuFrom is what the menu bar items say of the login: the app menu's
// 로그아웃, and nothing else in the bar.
func menuFrom(items []*elem) menuState {
	for _, it := range items {
		if !isAppMenu(it.Title) {
			continue
		}
		state := menuUnknown
		it.each(func(x *elem) {
			if state != menuUnknown || x.Role != "AXMenuItem" {
				return
			}
			for _, name := range logoutItems {
				if strings.TrimSpace(x.Title) != name {
					continue
				}
				switch x.Enabled {
				case 1:
					state = menuIn
				case 0:
					state = menuOut
				}
			}
		})
		return state
	}
	return menuUnknown
}

// lockedState is what is said while the screen is locked, when only the
// menu can be read and nothing is tried.
func lockedState(m menuState) (state, detail string) {
	switch m {
	case menuIn:
		return "locked-in", "맥미니 화면이 잠겨 있어요 - 로그아웃되면 잠금이 풀릴 때까지 다시 로그인하지 못해요"
	case menuOut:
		return "locked", "카톡이 로그아웃된 것 같은데 맥미니 화면이 잠겨 있어 로그인 창을 읽지 못해요"
	}
	return "locked-unknown", "맥미니 화면이 잠겨 있어 카톡을 볼 수 없어요"
}

// classify reads what KakaoTalk is showing from its windows.
func classify(windows []*elem) view {
	v := view{Windows: len(windows)}
	for _, w := range windows {
		if f := findForm(w); f != nil && v.Form == nil {
			v.Form = f
		}
		w.each(func(x *elem) {
			if x != w && x.Role != "AXSheet" {
				return
			}
			if ok := findButton(x, "확인", "OK"); ok != nil && (x.Role == "AXSheet" || isDialog(x)) {
				v.Dialogs = append(v.Dialogs, dialog{Window: x, Text: x.text(), OK: ok})
			}
		})
	}
	return v
}

func isDialog(w *elem) bool {
	switch w.Subrole {
	case "AXDialog", "AXSystemDialog", "AXFloatingWindow":
		return true
	}
	return false
}

func findForm(w *elem) *form {
	var f form
	w.each(func(x *elem) {
		switch {
		case x.Role == "AXTextField" && x.Subrole == "AXSecureTextField":
			if f.Password == nil {
				f.Password = x
			}
		case x.Role == "AXTextField":
			if f.Account == nil {
				f.Account = x
			}
		case x.Role == "AXCheckBox" && strings.Contains(x.label(), "자동"):
			f.Auto = x
		}
	})
	if f.Password == nil {
		return nil
	}
	f.Window = w
	f.Login = findButton(w, "로그인")
	return &f
}

// findButton is the button whose label is one of names, or failing that the
// first that contains one - "카카오계정 로그인" - but never one that only
// leads elsewhere ("QR코드 로그인", "계정 찾기").
func findButton(w *elem, names ...string) *elem {
	var exact, near *elem
	w.each(func(x *elem) {
		if x.Role != "AXButton" {
			return
		}
		l := strings.TrimSpace(x.label())
		for _, n := range names {
			switch {
			case l == n && exact == nil:
				exact = x
			case strings.Contains(l, n) && near == nil && !strings.Contains(l, "QR") &&
				!strings.Contains(l, "찾기") && !strings.Contains(l, "재설정") && !strings.Contains(l, "다른"):
				near = x
			}
		}
	})
	if exact != nil {
		return exact
	}
	return near
}

// noticeWords are what a notice KakaoTalk shows around a logout says. A 확인
// is pressed on such a notice - or on any, while the login window is up -
// and on nothing else: a 확인 somewhere else may agree to something.
var noticeWords = []string{"로그아웃", "로그인", "연결", "네트워크", "다른 기기", "다른 PC", "만료", "세션", "다시 시도"}

func safeToConfirm(d dialog, formUp bool) bool {
	if formUp {
		return true
	}
	for _, w := range noticeWords {
		if strings.Contains(d.Text, w) {
			return true
		}
	}
	return false
}

// verifyWords are what a screen asking for the phone, or an account Kakao
// has locked, says: no program can answer it, so the attempts stop and a
// person is told.
var verifyWords = []string{"인증", "휴대폰", "본인 확인", "확인 요청", "보안 문자", "보호조치", "잠금", "잠겼"}

func needsPerson(text string) bool {
	for _, w := range verifyWords {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// wrongPassword is a notice that the password was refused. Trying it again
// only brings the account closer to being locked, so it stops at the first.
func wrongPassword(text string) bool {
	if !strings.Contains(text, "비밀번호") {
		return false
	}
	for _, w := range []string{"일치", "틀", "잘못", "올바르지", "확인해"} {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// policy is how soon a login is tried again. Almost every failure is the
// network not quite back yet, so it is tried again soon - 10 s, then a
// little longer each time, never more than 5 minutes apart. What cannot be
// fixed by trying again (a refused password, the phone) stops it: GaveUp.
type policy struct {
	Fails  int
	Next   time.Time
	GaveUp bool
}

var backoff = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}

func (p *policy) due(now time.Time) bool { return !p.GaveUp && !now.Before(p.Next) }

func (p *policy) failed(now time.Time) {
	p.Fails++
	p.Next = now.Add(backoff[min(p.Fails-1, len(backoff)-1)])
}

// stop is a failure no retry can fix.
func (p *policy) stop() { p.Fails++; p.GaveUp = true }

// reset starts over: a login that worked, a person logging in, a new password.
func (p *policy) reset() { *p = policy{} }
