//go:build darwin

package main

import (
	"testing"
	"time"
)

// nettop as the Mac mini gave it 2026-11-15 11:2x, KakaoTalk back on the chat
// server: one 5228 established, and the connections of the time it was stuck
// - closed by the server, never by KakaoTalk - left in CloseWait.
const nettopBack = `,state,bytes_in,bytes_out,
KakaoTalk.404,,410888,112949,
tcp4 192.168.219.143:59935<->211.249.243.42:5228,Established,284807,15824,
tcp4 192.168.219.143:59934<->211.183.211.10:5228,CloseWait,4853,2339,
tcp4 192.168.219.143:59116<->211.249.222.161:443,Established,101836,85704,
tcp4 192.168.219.143:58058<->121.53.93.47:5223,CloseWait,4850,2287,
tcp4 192.168.219.104:49753<->121.53.93.47:995,CloseWait,4842,2228,
`

func TestChatUpReadsTheChatConnection(t *testing.T) {
	if up, err := chatUp(nettopBack); err != nil || !up {
		t.Fatalf("5228 이 Established 면 붙어 있습니다: %v %v", up, err)
	}
	stuck := `,state,bytes_in,bytes_out,
KakaoTalk.404,,410888,112949,
tcp4 192.168.219.143:59934<->211.183.211.10:5228,CloseWait,4853,2339,
tcp4 192.168.219.143:59116<->211.249.222.161:443,Established,101836,85704,
tcp4 192.168.219.104:49753<->121.53.93.47:995,CloseWait,4842,2228,
`
	if up, err := chatUp(stuck); err != nil || up {
		t.Fatalf("채팅 포트가 CloseWait 뿐이면 443 이 살아 있어도 끊긴 것입니다: %v %v", up, err)
	}
	if up, err := chatUp(",state,\nKakaoTalk.404,,0,0,\n"); err != nil || up {
		t.Fatalf("연결이 하나도 없으면 끊긴 것입니다: %v %v", up, err)
	}
	if _, err := chatUp(",state,\n"); err == nil {
		t.Fatal("카톡 줄이 없는 출력으로는 아무것도 정하지 않습니다")
	}
	if up, _ := chatUp("KakaoTalk.9,,1,1,\ntcp6 fe80::1%en0.50000<->2404:4600::7.5223,Established,1,1,\n"); !up {
		t.Fatal("IPv6 주소의 포트(점 뒤)도 읽어야 합니다")
	}
}

// Down for a redial is let be; down past the grace behind a sleeping display
// wakes it once; still down after that, KakaoTalk is restarted - and not
// again within restartEvery. Up at any point ends it, and a down internet
// starts the wait over.
func TestLinkWakesThenRestarts(t *testing.T) {
	var k link
	t0 := time.Date(2026, 11, 13, 19, 19, 0, 0, time.Local)
	step := func(up, online, locked bool, after time.Duration, want string) {
		t.Helper()
		if got := k.next(up, online, locked, true, t0.Add(after)); got != want {
			t.Fatalf("%s 뒤: %s 여야 하는데 %s", after, want, got)
		}
	}
	step(false, true, true, 0, "down")
	step(false, true, true, 2*time.Minute, "down")
	step(false, true, true, 3*time.Minute, "wake")
	step(false, true, true, 4*time.Minute, "waking")
	step(false, true, true, 5*time.Minute, "restart")
	// The new KakaoTalk has its own grace, then a wake of its own.
	step(false, true, true, 6*time.Minute, "down")
	step(false, true, true, 8*time.Minute, "wake")
	step(false, true, true, 10*time.Minute, "held")
	step(false, true, true, 34*time.Minute, "held")
	step(false, true, true, 35*time.Minute, "restart")
	step(true, true, true, 36*time.Minute, "up")
	if !k.downSince.IsZero() || !k.wokeAt.IsZero() {
		t.Fatalf("붙으면 끊긴 기록을 지웁니다: %+v", k)
	}

	// The internet down: nothing to dial, and the grace counts from its return.
	k = link{}
	step(false, false, true, 0, "offline")
	step(false, false, true, 10*time.Minute, "offline")
	step(false, true, true, 11*time.Minute, "down")
	step(false, true, true, 13*time.Minute, "down")
	step(false, true, true, 14*time.Minute, "wake")

	// Awake (or a lock a wake cannot open): no wake, a restart.
	k = link{}
	step(false, true, false, 0, "down")
	step(false, true, false, 3*time.Minute, "restart")
	k = link{}
	if k.next(false, true, true, false, t0); k.next(false, true, true, false, t0.Add(3*time.Minute)) != "restart" {
		t.Fatal("비밀번호 잠금이면 깨워도 소용없으니 바로 다시 켭니다")
	}
}
