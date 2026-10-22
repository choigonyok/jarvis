//go:build darwin

// Command relogin logs KakaoTalk on the Mac mini back in when it has been
// logged out - as it is when the network comes back after a long drop
// (2026-10-19 and 2026-10-22: twelve minutes offline, logged out on the way
// back). kakaotalk-client reads KakaoTalk's database, so a logged-out
// KakaoTalk silently stops every message.
//
// It looks at KakaoTalk every five seconds through the accessibility API. A
// notice about the logout gets its 확인; the login window gets the password
// kept in the login keychain and its 로그인 - the moment the internet answers
// again, and again 10 s after a failure, a little longer each time but never
// more than 5 minutes apart. A refused password stops it at once (tried on
// and on, it locks the account), and so does a screen asking for the phone. What it sees and did goes to
// ~/.jarvis-host/kakaotalk-login.json, which the 상태 tab reads.
//
// It runs as the LaunchAgent com.jarvis.kakaotalk-relogin
// (scripts/kakaotalk-relogin install) and needs, once, Accessibility for this
// binary (System Settings → Privacy & Security → Accessibility) and the
// password (scripts/kakaotalk-relogin password). Neither goes anywhere else.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const bundleID = "com.kakao.KakaoTalkMac"

// poll is how often KakaoTalk is looked at: a logout is seen within it, and
// so is the internet coming back.
const poll = 5 * time.Second

// status is what the 상태 tab reads. Nothing in it is secret.
type status struct {
	CheckedAt time.Time `json:"checkedAt"`
	// logged-in | no-window | relogged | notice | starting | offline | waiting |
	// failed | wrong-password | needs-person | no-access | no-password | unknown
	State      string     `json:"state"`
	Detail     string     `json:"detail,omitempty"`
	Access     bool       `json:"access"`
	Password   string     `json:"password"` // stored | missing | unreadable | unknown
	Fails      int        `json:"fails"`
	NextTry    *time.Time `json:"nextTry,omitempty"`
	LastTry    *time.Time `json:"lastTry,omitempty"`
	LastResult string     `json:"lastResult,omitempty"`
	ReloggedAt *time.Time `json:"reloggedAt,omitempty"`
}

type daemon struct {
	statusFile, dropFile, inspectTrigger, inspectOut, promptedFile string

	pol     policy
	st      status
	started time.Time
	// The last look saw it logged out: the next that sees it logged in says so.
	wasOut bool
	// A view nothing here knows was written down once, for a person to read.
	dumped bool
}

func main() {
	log.SetFlags(log.LstdFlags)
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	d := &daemon{
		statusFile:     filepath.Join(home, ".jarvis-host", "kakaotalk-login.json"),
		dropFile:       filepath.Join(home, ".config", "jarvis", "kakaotalk-login.new"),
		inspectTrigger: filepath.Join(home, ".jarvis-host", "kakaotalk-inspect"),
		inspectOut:     filepath.Join(home, ".config", "jarvis", "kakaotalk-ax.txt"),
		promptedFile:   filepath.Join(home, ".config", "jarvis", "kakaotalk-relogin.prompted"),
		started:        time.Now(),
		st:             status{Password: "unknown"},
	}
	switch cmd {
	case "run":
		d.run()
	default:
		fmt.Fprintln(os.Stderr, "usage: jarvis-kakaotalk-relogin run   (scripts/kakaotalk-relogin 이 LaunchAgent 로 띄운다)")
		os.Exit(2)
	}
}

func (d *daemon) run() {
	log.Printf("카톡 자동 로그인을 시작합니다")
	if _, _, err := getPassword(); err == nil {
		d.st.Password = "stored"
	} else if err == errNoPassword {
		d.st.Password = "missing"
	}
	for {
		wait := d.tick()
		d.write()
		time.Sleep(wait)
	}
}

func (d *daemon) set(state, detail string) {
	d.st.State, d.st.Detail = state, detail
	d.st.Fails = d.pol.Fails
	d.st.NextTry = nil
	if !d.pol.Next.IsZero() && !d.pol.GaveUp {
		t := d.pol.Next
		d.st.NextTry = &t
	}
}

func (d *daemon) tick() time.Duration {
	now := time.Now()
	d.st.CheckedAt = now
	d.st.Access = axTrusted(false)
	d.importPassword()

	if !d.st.Access {
		// macOS asks the person at the screen once; after that the grant is
		// theirs to give in System Settings.
		if _, err := os.Stat(d.promptedFile); err != nil {
			axTrusted(true)
			_ = os.WriteFile(d.promptedFile, []byte(now.Format(time.RFC3339)+"\n"), 0o600)
		}
		d.set("no-access", "이 프로그램에 손쉬운 사용 권한이 없어요")
		// A process's access is settled when it starts: a fresh start sees a
		// new grant, so this one makes way for one now and then.
		if time.Since(d.started) > 5*time.Minute {
			d.write()
			os.Exit(0)
		}
		return 30 * time.Second
	}

	pid := kakaoPID()
	if pid == 0 {
		_ = exec.Command("/usr/bin/open", "-g", "-b", bundleID).Run()
		d.set("starting", "카톡이 꺼져 있어 켰어요")
		return 10 * time.Second
	}
	l, err := readApp(pid)
	if err != nil {
		d.set("unknown", err.Error())
		return 10 * time.Second
	}
	defer l.close()
	d.inspect(l)
	v := classify(l.windows)

	if v.loggedIn() || v.Windows == 0 {
		if d.wasOut {
			// Someone logged in by hand (a login of ours says so where it happens).
			log.Printf("카톡이 다시 로그인돼 있어요")
		}
		d.wasOut = false
		d.pol.reset()
		if v.Windows == 0 {
			d.set("no-window", "")
		} else {
			d.set("logged-in", "")
		}
		return poll
	}
	d.wasOut = true

	for _, dg := range v.Dialogs {
		if needsPerson(dg.Text) {
			d.pol.stop()
			d.set("needs-person", clip(dg.Text))
			return 30 * time.Second
		}
		if wrongPassword(dg.Text) && v.Form != nil {
			_ = press(dg.OK)
			d.pol.stop()
			d.st.LastResult = clip(dg.Text)
			d.set("wrong-password", clip(dg.Text))
			return 30 * time.Second
		}
		if safeToConfirm(dg, v.Form != nil) {
			log.Printf("안내창을 닫습니다: %q", clip(dg.Text))
			if err := press(dg.OK); err != nil {
				log.Printf("확인을 누르지 못했습니다: %v", err)
			}
			d.set("notice", clip(dg.Text))
			return 2 * time.Second
		}
	}

	if v.Form == nil {
		if !d.dumped {
			d.dumped = true
			d.writeDump(l, "알 수 없는 화면")
		}
		d.set("unknown", "로그인 화면도 아니고 아는 안내창도 아니에요")
		return 10 * time.Second
	}

	switch {
	case d.pol.GaveUp:
		// Kept as it was stopped (wrong-password, needs-person) until a person
		// logs in or a new password arrives.
		return 30 * time.Second
	case !d.pol.due(now):
		d.set("waiting", d.st.LastResult)
		return poll
	case !online():
		d.set("offline", "인터넷이 아직 돌아오지 않았어요")
		return poll
	}
	account, password, err := getPassword()
	if err != nil {
		if err == errNoPassword {
			d.st.Password = "missing"
		} else {
			d.st.Password = "unreadable"
		}
		d.set("no-password", err.Error())
		return 30 * time.Second
	}
	d.st.Password = "stored"

	ok, detail, person := d.attempt(l, v.Form, account, password)
	t := time.Now()
	d.st.LastTry = &t
	switch {
	case ok:
		log.Printf("카톡에 다시 로그인했습니다")
		d.pol.reset()
		d.wasOut = false
		d.st.ReloggedAt = &t
		d.st.LastResult = "로그인됨"
		d.set("relogged", "")
	case person:
		log.Printf("사람이 해야 하는 화면이 떠서 멈춥니다: %q", detail)
		d.pol.stop()
		d.st.LastResult = detail
		d.set("needs-person", detail)
	case wrongPassword(detail):
		log.Printf("비밀번호가 맞지 않아 멈춥니다: %q", detail)
		d.pol.stop()
		d.st.LastResult = detail
		d.set("wrong-password", detail)
	default:
		d.pol.failed(t)
		log.Printf("로그인하지 못했습니다(%d번째, 다음은 %s 뒤): %s", d.pol.Fails, d.pol.Next.Sub(t).Round(time.Second), detail)
		d.st.LastResult = detail
		d.set("failed", detail)
	}
	return poll
}

// attempt fills the login window and presses 로그인, then watches what follows.
func (d *daemon) attempt(l *look, f *form, account, password string) (ok bool, detail string, person bool) {
	_ = setBool(l.app, "AXFrontmost", true)
	_ = raise(f.Window)
	if f.Account != nil && strings.TrimSpace(f.Account.Value) == "" {
		if account == "" {
			return false, "계정 칸이 비어 있는데 저장된 계정이 없어요", false
		}
		if err := setValue(f.Account, account); err != nil {
			return false, "계정 칸에 넣지 못했어요", false
		}
	}
	// KakaoTalk's own 자동 로그인, while the window is open anyway.
	if f.Auto != nil && f.Auto.Value == "0" {
		_ = press(f.Auto)
	}
	_ = setBool(f.Password.ref, "AXFocused", true)
	err := setValue(f.Password, password)
	time.Sleep(300 * time.Millisecond)
	if err != nil || (f.Login != nil && axBool(f.Login.ref, "AXEnabled") == 0) {
		// The field did not take the value as set: typed instead, as a person would.
		_ = setBool(f.Password.ref, "AXFocused", true)
		key(l.pid, keyA, true)
		key(l.pid, keyDelete, false)
		typeText(l.pid, password)
		time.Sleep(300 * time.Millisecond)
	}
	if f.Login != nil && axBool(f.Login.ref, "AXEnabled") != 0 {
		if err := press(f.Login); err != nil {
			key(l.pid, keyReturn, false)
		}
	} else {
		key(l.pid, keyReturn, false)
	}

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		nl, err := readApp(l.pid)
		if err != nil {
			continue
		}
		v := classify(nl.windows)
		var said []string
		for _, dg := range v.Dialogs {
			said = append(said, dg.Text)
		}
		text := strings.Join(said, " ")
		switch {
		case needsPerson(text) || (v.Form == nil && needsPerson(smallWindowsText(nl.windows))):
			if text == "" {
				text = smallWindowsText(nl.windows)
			}
			nl.close()
			return false, clip(text), true
		case len(v.Dialogs) > 0 && v.Form != nil:
			// A notice over the login window: "비밀번호가 일치하지 않습니다" and the like.
			_ = press(v.Dialogs[0].OK)
			nl.close()
			return false, clip(text), false
		case v.loggedIn():
			nl.close()
			return true, "", false
		}
		nl.close()
	}
	return false, "로그인 화면이 그대로예요", false
}

// smallWindowsText is the text of windows small enough to be a screen of
// their own - not the chat list, whose previews say anything at all.
func smallWindowsText(ws []*elem) string {
	var parts []string
	for _, w := range ws {
		n := 0
		w.each(func(*elem) { n++ })
		if n < 120 {
			if t := w.text(); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, " ")
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 140 {
		return string(r[:140]) + "…"
	}
	return s
}

func kakaoPID() int {
	out, err := exec.Command("/usr/bin/pgrep", "-x", "KakaoTalk").Output()
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]))
	return pid
}

// online is whether Kakao's servers answer at all: a login tried before the
// network is back only counts as a failure.
func online() bool {
	for _, host := range []string{"www.kakao.com:443", "kakao.com:443"} {
		if c, err := net.DialTimeout("tcp", host, 3*time.Second); err == nil {
			c.Close()
			return true
		}
	}
	return false
}

// importPassword moves a password handed in by scripts/kakaotalk-relogin
// password (a 0600 file, account on the first line) into the keychain and
// removes the file. It is done here, in the person's session, where the
// login keychain is open - not over SSH, where it is not.
func (d *daemon) importPassword() {
	raw, err := os.ReadFile(d.dropFile)
	if err != nil {
		return
	}
	defer func() {
		_ = os.WriteFile(d.dropFile, make([]byte, len(raw)), 0o600)
		_ = os.Remove(d.dropFile)
	}()
	account, password, _ := strings.Cut(string(raw), "\n")
	password = strings.TrimSuffix(password, "\n")
	if password == "" {
		log.Printf("받은 비밀번호가 비어 있어 버립니다")
		return
	}
	if err := putPassword(strings.TrimSpace(account), password); err != nil {
		log.Printf("비밀번호를 키체인에 옮기지 못했습니다: %v", err)
		d.st.Password = "unreadable"
		return
	}
	log.Printf("비밀번호를 키체인에 옮겼습니다")
	d.st.Password = "stored"
	d.pol.reset()
}

func (d *daemon) write() {
	raw, _ := json.Marshal(d.st)
	tmp := d.statusFile + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		log.Printf("상태를 적지 못했습니다: %v", err)
		return
	}
	_ = os.Rename(tmp, d.statusFile)
}

// inspect writes KakaoTalk's windows down when asked (scripts/kakaotalk-relogin
// inspect touches the trigger): over SSH this program's access does not
// apply, so it is asked through a file.
func (d *daemon) inspect(l *look) {
	if _, err := os.Stat(d.inspectTrigger); err != nil {
		return
	}
	_ = os.Remove(d.inspectTrigger)
	d.writeDump(l, "요청")
}

// writeDump writes the windows' shape: roles, labels, and the words of a
// login window or a notice - never what a chat says, never a secure field.
func (d *daemon) writeDump(l *look, why string) {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s %s\n", time.Now().Format(time.RFC3339), why)
	for _, w := range l.windows {
		words := findForm(w) != nil || isDialog(w)
		dump(&b, w, 0, words)
	}
	if err := os.WriteFile(d.inspectOut, []byte(b.String()), 0o600); err != nil {
		log.Printf("화면 구조를 적지 못했습니다: %v", err)
	}
}

func dump(b *strings.Builder, e *elem, depth int, words bool) {
	if e.Role == "AXSheet" {
		words = true
	}
	fmt.Fprintf(b, "%s%s", strings.Repeat("  ", depth), e.Role)
	if e.Subrole != "" {
		fmt.Fprintf(b, "/%s", e.Subrole)
	}
	if e.Title != "" {
		fmt.Fprintf(b, " title=%q", e.Title)
	}
	if e.Desc != "" {
		fmt.Fprintf(b, " desc=%q", e.Desc)
	}
	if words && e.Value != "" && e.Subrole != "AXSecureTextField" {
		fmt.Fprintf(b, " value=%q", clip(e.Value))
	}
	if e.Enabled == 0 {
		b.WriteString(" disabled")
	}
	b.WriteString("\n")
	for _, k := range e.Kids {
		dump(b, k, depth+1, words)
	}
}
