//go:build darwin

package main

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// A KakaoTalk logged in is not yet a KakaoTalk that receives: its chat goes
// over one long connection (LOCO) and a message only reaches the database
// while that is up. A drop of the network can leave it logged in with that
// connection gone - the server closes its side, KakaoTalk never notices
// (CLOSE_WAIT) and does not dial again while the Mac's display sleeps. The
// menu still says 로그아웃, the windows show the chat list, and every message
// stops: 2026-11-13 19:18 a drop changed the Mac mini's address, and nothing
// came in until the display was woken 11-15 11:17, when KakaoTalk dialed at
// once and fetched the 1,458 it had missed.
//
// So the connection itself is looked at, once a minute while KakaoTalk is
// logged in. lsof does not show it (KakaoTalk's connections run in user space
// through Network.framework); nettop does. Down past linkGrace with the
// internet up, the display is woken first - that alone brought it back - and
// if it is still down linkWakeWait after that, KakaoTalk is quit and opened
// again, at most once in restartEvery.

// chatPorts are where KakaoTalk's chat connection goes. Its pictures, pages
// and ads go over 443, so a connection there says nothing of the chat.
var chatPorts = map[string]bool{"5228": true, "5223": true, "995": true, "9282": true}

const (
	linkEvery    = time.Minute      // how often the connection is looked at
	linkGrace    = 3 * time.Minute  // down this long is more than a redial
	linkWakeWait = 2 * time.Minute  // after a wake, this long before a restart
	restartEvery = 30 * time.Minute // at most one restart in this long
)

// chatUp reads `nettop -p <pid> -L 1 -x -n -J state`: whether KakaoTalk has
// a chat connection established. A reading without KakaoTalk's own line is
// not a reading at all - nothing is decided from it.
func chatUp(out string) (bool, error) {
	seen, up := false, false
	for _, line := range strings.Split(out, "\n") {
		cols := strings.Split(strings.TrimSpace(line), ",")
		if len(cols) < 2 {
			continue
		}
		if strings.HasPrefix(cols[0], "KakaoTalk.") {
			seen = true
			continue
		}
		_, remote, ok := strings.Cut(cols[0], "<->")
		if !ok || !strings.HasPrefix(cols[0], "tcp") {
			continue
		}
		if chatPorts[port(remote)] && cols[1] == "Established" {
			up = true
		}
	}
	if !seen {
		return false, errors.New("nettop 이 카톡을 보여 주지 않았어요")
	}
	return up, nil
}

// port is the port of an address as nettop writes it: 1.2.3.4:5228 for IPv4,
// with a dot before the port where the address itself is full of colons.
func port(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 && digits(addr[i+1:]) {
		return addr[i+1:]
	}
	if i := strings.LastIndex(addr, "."); i >= 0 && digits(addr[i+1:]) {
		return addr[i+1:]
	}
	return ""
}

func digits(s string) bool {
	_, err := strconv.Atoi(s)
	return s != "" && err == nil
}

// link is what is known of the chat connection of a logged-in KakaoTalk.
type link struct {
	downSince   time.Time // first look that found it down; zero while up
	wokeAt      time.Time // the display woken for this time down
	restartedAt time.Time // KakaoTalk last quit and opened for it
}

// next is what a look at the connection comes to:
//
//	up       - established; whatever was down is over
//	offline  - down, but so is the internet: nothing to dial, the wait starts once it is back
//	down     - down for less than linkGrace (a redial takes a moment)
//	wake     - down past linkGrace behind a sleeping display: wake it
//	waking   - woken, given linkWakeWait to dial
//	restart  - still down: quit KakaoTalk and open it again
//	held     - still down, but restarted less than restartEvery ago
func (k *link) next(up, online, locked, canWake bool, now time.Time) string {
	switch {
	case up:
		k.downSince, k.wokeAt = time.Time{}, time.Time{}
		return "up"
	case !online:
		// The grace counts from the first look after the internet is back.
		k.downSince, k.wokeAt = time.Time{}, time.Time{}
		return "offline"
	case k.downSince.IsZero():
		k.downSince = now
		return "down"
	case now.Sub(k.downSince) < linkGrace:
		return "down"
	case locked && canWake && k.wokeAt.IsZero():
		k.wokeAt = now
		return "wake"
	case !k.wokeAt.IsZero() && now.Sub(k.wokeAt) < linkWakeWait:
		return "waking"
	case !k.restartedAt.IsZero() && now.Sub(k.restartedAt) < restartEvery:
		return "held"
	}
	// The new KakaoTalk gets its own grace before anything is tried again.
	k.restartedAt, k.downSince, k.wokeAt = now, now, time.Time{}
	return "restart"
}

// readLink asks nettop for KakaoTalk's connections.
func readLink(pid int) (bool, error) {
	out, err := exec.Command("/usr/bin/nettop", "-p", strconv.Itoa(pid), "-L", "1", "-x", "-n", "-J", "state").Output()
	if err != nil {
		return false, err
	}
	return chatUp(string(out))
}

// restartKakao quits KakaoTalk (SIGTERM, as a quit from the Dock would end
// it; its database is written ahead and survives that) and waits for it to
// go. The next look finds it gone and opens it again.
func restartKakao(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		if kakaoPID() != pid {
			return nil
		}
	}
	return errors.New("10초가 지나도 카톡이 꺼지지 않았어요")
}
