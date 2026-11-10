package holder

import (
	"context"
	"testing"
	"time"
)

// Held on one card, every request hears the same answer, and one that came too
// late has nobody to tell.
func TestHeldRequestsHearTheirCard(t *testing.T) {
	var p Payments
	ctx := context.Background()
	done := make(chan bool, 2)
	for range 2 {
		go func() { done <- p.Wait(ctx, "c1", time.Now().Add(5*time.Second)) }()
	}
	for i := 0; i < 500 && p.Holding() != 2; i++ {
		time.Sleep(time.Millisecond)
	}
	if !p.Release("c1", true) {
		t.Fatal("기다리는 요청이 있었습니다")
	}
	for range 2 {
		if !<-done {
			t.Fatal("승인되면 이어집니다")
		}
	}
	if p.Release("c1", true) {
		t.Fatal("이제 기다리는 요청이 없습니다")
	}
	if p.Wait(ctx, "c2", time.Now().Add(time.Millisecond)) {
		t.Fatal("시간이 지나면 이어지지 않습니다")
	}
	if p.Holding() != 0 {
		t.Fatal(p.Holding())
	}
}

// UnderWay: approved, come back to, and still in its window - the latest of
// the payer's, never another payer's.
func TestAPaymentIsUnderWayOnceThePayerIsBack(t *testing.T) {
	now := time.Date(2026, 11, 10, 22, 33, 0, 0, time.UTC)
	p := Payments{Now: func() time.Time { return now }}
	p.Grant("musinsa.com", 137, "")
	if _, ok := p.UnderWay(137); ok {
		t.Fatal("돌아오기 전에는 진행 중이 아닙니다")
	}
	p.Allow("musinsa.com", 137, "", "")
	now = now.Add(time.Minute)
	p.Grant(windowSite, 137, "")
	p.Allow(windowSite, 137, "", "")
	if site, ok := p.UnderWay(137); !ok || site != windowSite {
		t.Fatalf("마지막에 돌아온 결제: %q %v", site, ok)
	}
	if _, ok := p.UnderWay(7); ok {
		t.Fatal("다른 결제자의 결제")
	}
	now = now.Add(Window + time.Minute)
	if _, ok := p.UnderWay(137); ok {
		t.Fatal("창이 지나면 끝납니다")
	}
}

const windowSite = "pg-window"

// A mall's 결제하기 hands the same payment to its PG at once: the window opens
// on the mall's approval - right after its request went through, at the
// amount the card said - and is then held as the window's, under way.
func TestTheMallsApprovalCarriesItsWindow(t *testing.T) {
	now := time.Date(2026, 11, 11, 1, 23, 13, 0, time.UTC)
	p := Payments{Now: func() time.Time { return now }}
	p.Grant("musinsa.com", 137, "22470")
	if _, ok := p.Carry(windowSite, 137, ""); ok {
		t.Fatal("결제자가 돌아오기 전에는 결제창을 열지 않습니다")
	}
	p.Allow("musinsa.com", 137, "", "")
	now = now.Add(time.Second)
	if from, ok := p.Carry(windowSite, 137, ""); !ok || from != "musinsa.com" {
		t.Fatalf("방금 지나간 결제의 결제창: %q %v", from, ok)
	}
	if !p.IsOpen(windowSite, 137) || !p.Allow(windowSite, 137, "", "") {
		t.Fatal("열린 결제창은 그 결제의 것으로 진행 중입니다")
	}
	if p.Allow(windowSite, 137, "", "50000") {
		t.Fatal("결제창 승인은 카드가 말한 금액에 묶입니다")
	}
	if _, ok := p.Carry(windowSite, 7, ""); ok {
		t.Fatal("다른 결제자의 결제창")
	}

	p.Grant("shop.example", 21, "64000")
	p.Allow("shop.example", 21, "", "")
	if _, ok := p.Carry(windowSite, 21, "30000"); ok {
		t.Fatal("금액이 다른 결제창은 다른 결제입니다")
	}
	if _, ok := p.Carry(windowSite, 21, "64000"); !ok {
		t.Fatal("같은 금액의 결제창은 그 결제입니다")
	}

	p.Grant("other.example", 22, "")
	p.Allow("other.example", 22, "", "")
	now = now.Add(CarryGap + time.Second)
	if _, ok := p.Carry(windowSite, 22, ""); ok {
		t.Fatal("한참 뒤에 열리는 결제창은 따로 묻습니다")
	}
	p.Grant(windowSite, 23, "")
	p.Allow(windowSite, 23, "", "")
	if _, ok := p.Carry(windowSite, 23, ""); ok {
		t.Fatal("결제창 승인은 결제창을 넘겨주지 않습니다")
	}
}

// LiveOn: who still has an approved payment on a site.
func TestLiveOnASite(t *testing.T) {
	now := time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC)
	p := Payments{Now: func() time.Time { return now }}
	p.Grant(windowSite, 137, "")
	p.Grant(windowSite, 0, "")
	p.Grant("musinsa.com", 5, "")
	got := p.LiveOn(windowSite)
	if len(got) != 2 || !((got[0] == 137 && got[1] == 0) || (got[0] == 0 && got[1] == 137)) {
		t.Fatalf("결제창에 살아 있는 승인: %v", got)
	}
	now = now.Add(Wait + time.Minute)
	if got := p.LiveOn(windowSite); len(got) != 0 {
		t.Fatalf("돌아오지 않은 승인은 끝납니다: %v", got)
	}
}

// Live: approved and not yet come back to, or under way.
func TestAnApprovalIsLiveWhileItCanStillHappen(t *testing.T) {
	now := time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC)
	p := Payments{Now: func() time.Time { return now }}
	p.Grant("coupang.com", 0, "")
	if !p.Live(0) || p.Live(7) {
		t.Fatal("승인된 결제자만")
	}
	now = now.Add(Wait + time.Minute)
	if p.Live(0) {
		t.Fatal("돌아오지 않은 승인은 끝납니다")
	}
}
