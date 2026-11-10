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
