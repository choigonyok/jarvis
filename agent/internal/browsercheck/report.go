package browsercheck

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/checks"
)

// Lookups is how a site's order lookups stand in the book.
type Lookups struct {
	LoginRequired int        `json:"loginRequired"`
	Stale         int        `json:"stale"`
	Pending       int        `json:"pending"`
	LastDone      *time.Time `json:"lastDone,omitempty"`
}

func (l Lookups) facts() []string {
	out := []string{fmt.Sprintf("조회 대기 %d건, 로그인 대기 %d건", l.Pending, l.LoginRequired)}
	if l.LastDone != nil {
		return append(out, "마지막으로 읽은 주문: "+l.LastDone.Local().Format("1월 2일 15:04"))
	}
	return append(out, "아직 읽은 주문이 없어요.")
}

// Quiet is a site whose order history is not opened for a look: only what the
// lookups ran into speaks for it.
type Quiet struct {
	ID, Name, Rule, Fix string
}

const sayEvery = 5 * time.Minute

// Say says how each site's order history stands, until ctx ends. The agent is
// the browser's one user, so it is the one to say.
func (c *Checker) Say(ctx context.Context, quiet []Quiet, read func(ctx context.Context, site string) (Lookups, error), report *checks.Reporter) {
	t := time.NewTicker(sayEvery)
	defer t.Stop()
	for {
		for _, s := range c.Sites {
			l, err := read(ctx, s.ID)
			c.mu.Lock()
			r, ok := c.results[s.ID]
			c.mu.Unlock()
			report.Report(opened(s, r, ok, l, err))
		}
		for _, q := range quiet {
			l, err := read(ctx, q.ID)
			report.Report(unopened(q, l, err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

const group = "가계부 상품 상세"

// opened judges by where the order history landed when last opened. A cookie
// on disk is not evidence: the profile kept every long-lived Coupang cookie
// while the order page sent it to login.
func opened(s Site, r Result, ok bool, l Lookups, err error) checks.Result {
	v := checks.Result{ID: "enrich." + s.ID, Group: group, Name: s.Name, Deps: []string{"browser"},
		Rule: "에이전트가 대화·작업이 없을 때 브라우저 새 탭에서 주문목록을 열어 봐요 - 브라우저가 떠 있으면 30분마다, 꺼져 있으면 6시간마다(확인하려고 띄우지는 않아요). 열리면 정상, 로그인 화면으로 넘어가면 이상.",
		Fix:  "화면 탭에서 에이전트 브라우저로 로그인하세요. 로그인이 끝나면 멈춰 있던 상품 상세 조회가 저절로 이어져요."}
	if err != nil {
		v.Facts = append(v.Facts, "조회 기록을 읽지 못했어요: "+err.Error())
	} else {
		v.Facts = l.facts()
	}
	if !ok {
		v.State, v.Summary = checks.Unknown, "에이전트가 아직 열어 보지 않았어요. 대화·작업이 없을 때 열어 봐요."
		return v
	}
	at := r.At
	v.ObservedAt, v.ObservedLabel = &at, "브라우저로 확인"
	switch {
	case r.Error != "":
		v.State, v.Summary = checks.Unknown, "브라우저로 열어 보지 못했어요: "+r.Error
	case r.Busy && strings.Contains(r.URL, s.LoginHost):
		// Someone may be typing a password there; this site's own login page
		// is itself the answer.
		v.State, v.Summary = checks.Fail, "브라우저에 로그인 화면이 열려 있어요. 로그인을 마치면 다음 확인 때 정상으로 바뀌어요."
	case r.Busy:
		v.State, v.ObservedAt = checks.Unknown, nil
		v.Summary = "다른 사이트의 로그인 화면이 열려 있어서 이번에는 열어 보지 않았어요."
	case strings.Contains(r.URL, s.LoginHost):
		v.State, v.Summary = checks.Fail, "주문목록을 열면 로그인 화면으로 넘어가요."
		if l.LoginRequired > 0 {
			v.Summary += fmt.Sprintf(" 상품 상세 %d건이 로그인을 기다리고 있어요.", l.LoginRequired)
		}
	case l.Stale > 0:
		v.State, v.Summary = checks.Warn, fmt.Sprintf("주문목록은 열리지만 하루 넘게 밀린 조회가 %d건 있어요.", l.Stale)
	default:
		v.State, v.Summary = checks.OK, "주문목록이 열려요."
	}
	return v
}

// unopened judges a site from what the lookups recorded, opening nothing.
func unopened(q Quiet, l Lookups, err error) checks.Result {
	v := checks.Result{ID: "enrich." + q.ID, Group: group, Name: q.Name, Rule: q.Rule, Fix: q.Fix}
	if err != nil {
		v.State, v.Summary = checks.Unknown, "조회 기록을 읽지 못했어요: "+err.Error()
		return v
	}
	v.Facts = l.facts()
	switch {
	case l.LoginRequired > 0:
		v.State = checks.Fail
		v.Summary = fmt.Sprintf("주문 내역을 읽다 로그인을 요구받았어요. 상품 상세 %d건이 로그인을 기다리고 있어요.", l.LoginRequired)
	case l.Stale > 0:
		v.State = checks.Warn
		v.Summary = fmt.Sprintf("하루 넘게 밀린 조회가 %d건 있어요.", l.Stale)
	case l.LastDone != nil:
		v.State, v.Summary = checks.OK, "주문 내역을 읽고 있어요."
		v.ObservedAt, v.ObservedLabel = l.LastDone, "마지막으로 읽은 주문"
	default:
		v.State = checks.Unknown
		v.Summary = "결제 감시 때문에 자동으로 열어 보지 않아요. 조회 기록이 생기면 그걸로 판단해요."
	}
	return v
}
