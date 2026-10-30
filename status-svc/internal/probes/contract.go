package probes

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/gocommon/health"
	"github.com/choigonyok/jarvis/status-svc/internal/check"
)

// The services' side of the status tab is one contract (gocommon/health):
// GET /health says, besides "up", how each background loop is doing and
// what the service set aside after trying too often. The probes here read
// it, so a loop that stopped or a box that filled is a row - not a log line
// nobody reads.

// fetched is one read of a service's /health.
type fetched struct {
	at     time.Time
	code   int
	report health.Report
	took   time.Duration
	err    error
}

// contracts keeps each /health read for a few seconds: one service's loops,
// its held box and the event deliveries are read by several probes in the
// same round. Each address has its own lock, so a slow service holds up only
// the probes reading it.
var contracts = struct {
	mu   sync.Mutex
	byID map[string]*cached
}{byID: map[string]*cached{}}

type cached struct {
	mu sync.Mutex
	f  fetched
}

func (e *env) contract(ctx context.Context, u string) fetched {
	contracts.mu.Lock()
	c := contracts.byID[u]
	if c == nil {
		c = &cached{}
		contracts.byID[u] = c
	}
	contracts.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.f.at.IsZero() && time.Since(c.f.at) < 10*time.Second {
		return c.f
	}
	f := fetched{at: time.Now()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		f.err = err
		c.f = f
		return f
	}
	start := time.Now()
	res, err := e.client.Do(req)
	f.took = time.Since(start)
	if err != nil {
		f.err = err
	} else {
		f.code = res.StatusCode
		_ = json.NewDecoder(res.Body).Decode(&f.report)
		res.Body.Close()
	}
	c.f = f
	return f
}

// service is a service's row under 기반: it answers its /health, and the
// loops it reports are getting through. only names the loops to judge here
// (none: all) - the rest have rows of their own.
func (e *env) service(id, name, base string, deps []string, only ...string) check.Probe {
	return check.Probe{
		ID: id, Group: "기반", Name: name,
		Rule: "서비스의 /health 가 200 이고, 서비스가 알려 주는 일(수집·전달 같은 반복 작업)이 정해진 시간 안에 한 번은 끝까지 돌았으면 정상. " +
			"그 시간을 넘기면 이상, 세 번 넘게 연달아 실패하는 중이면 주의.",
		Fix:  "docker compose ps 로 컨테이너를, 로그로 멈춘 일의 오류를 보세요.",
		Deps: deps, Every: 30 * time.Second,
		Run: func(ctx context.Context) check.Verdict {
			f := e.contract(ctx, base+"/health")
			switch {
			case f.err != nil:
				return fail("응답이 없어요.")
			case f.code != http.StatusOK:
				return fail(fmt.Sprintf("응답 코드 %d.", f.code))
			}
			return judgeLoops(f, only, time.Now())
		},
	}
}

// loop is one of a service's loops as a row of its own, where it is the thing
// a person asks about: are the messages being read, does the calendar answer.
func (e *env) loop(id, group, name, base, loop string, deps []string, rule, fix string) check.Probe {
	p := e.service(id, name, base, deps, loop)
	p.Group, p.Rule, p.Fix = group, rule, fix
	run := p.Run
	p.Run = func(ctx context.Context) check.Verdict {
		v := run(ctx)
		if v.State == check.OK {
			v.Summary = "읽고 있어요."
		}
		return v
	}
	return p
}

// judgeLoops is the verdict on a service that answered: its loops, worst
// first.
func judgeLoops(f fetched, only []string, now time.Time) check.Verdict {
	v := check.Verdict{State: check.OK, Summary: fmt.Sprintf("응답해요 (%dms).", f.took.Milliseconds())}
	var warn, stop []string
	for _, l := range f.report.Loops {
		if len(only) > 0 && !contains(only, l.Name) {
			continue
		}
		d, stalled := l.Stalled(now)
		line := l.Label
		switch {
		case l.LastOK != nil:
			line += " · 마지막으로 끝까지 돈 때 " + ago(now.Sub(*l.LastOK))
		default:
			line += " · 아직 한 번도 끝까지 돌지 않음"
		}
		if l.LastError != "" && l.ErrorAt != nil {
			line += " · 마지막 오류(" + ago(now.Sub(*l.ErrorAt)) + "): " + clip(l.LastError, 120)
		} else if l.LastError != "" {
			line += " · " + clip(l.LastError, 120)
		}
		if l.Failing > 0 {
			line += fmt.Sprintf(" · 연달아 %d번 실패", l.Failing)
		}
		v.Facts = append(v.Facts, line)
		switch {
		case stalled:
			s := fmt.Sprintf("%s: %s째 멈춰 있어요", l.Label, span(d))
			if l.LastError != "" {
				s += " - " + clip(l.LastError, 120)
			}
			stop = append(stop, s)
		case l.Failing >= 3:
			warn = append(warn, fmt.Sprintf("%s: 잘 되지 않고 있어요 - %s", l.Label, clip(l.LastError, 120)))
		}
	}
	switch {
	case len(stop) > 0:
		v.State, v.Summary = check.Fail, strings.Join(stop, " · ")
	case len(warn) > 0:
		v.State, v.Summary = check.Warn, strings.Join(warn, " · ")
	}
	return v
}

// heldBox is a service's held box (격리함): what it set aside after an item
// failed on its own health.MaxTries times, so the rest could go on.
func (e *env) heldBox(id, name, base, rule, fix string, deps []string) check.Probe {
	return check.Probe{
		ID: id, Group: "격리함", Name: name, Rule: rule, Fix: fix, Deps: deps,
		Every: time.Minute,
		Run: func(ctx context.Context) check.Verdict {
			f := e.contract(ctx, base+"/health")
			switch {
			case f.err != nil:
				return check.Verdict{State: check.Unknown, Summary: "서비스가 답하지 않아 격리함을 읽지 못했어요."}
			case f.code != http.StatusOK:
				return check.Verdict{State: check.Unknown, Summary: fmt.Sprintf("격리함을 읽지 못했어요 (응답 코드 %d).", f.code)}
			}
			return judgeHeld(f.report.Held, time.Now())
		},
	}
}

func judgeHeld(h *health.Held, now time.Time) check.Verdict {
	if h == nil || h.Count == 0 {
		return check.Verdict{State: check.OK, Summary: "따로 둔 것이 없어요."}
	}
	v := check.Verdict{State: check.Warn, Summary: fmt.Sprintf("%d건을 따로 두었어요. 나머지는 계속 처리되고 있어요.", h.Count),
		ObservedAt: h.Oldest, ObservedLabel: "가장 오래된 것"}
	for _, it := range h.Items {
		line := it.What + " · " + it.At.Local().Format("1월 2일 15:04")
		if it.Why != "" {
			line += " · " + clip(it.Why, 120)
		}
		v.Facts = append(v.Facts, line)
	}
	if more := h.Count - len(h.Items); more > 0 {
		v.Facts = append(v.Facts, fmt.Sprintf("그 밖에 %d건", more))
	}
	if h.Where != "" {
		v.Facts = append(v.Facts, "처리: "+h.Where)
	}
	return v
}

// delivery is the event log's delivery to one consumer - the path every
// notification, card and observation takes. When the consumer is down the
// row says so through Deps.
func (e *env) delivery(consumer, name string, deps []string) check.Probe {
	return check.Probe{
		ID: "stream." + consumer, Group: "기반", Name: name,
		Rule: consumer + " 이(가) 기록 스트림을 차례로 읽고 있으면 정상. 처리하지 못하는 채로 15분이 넘으면 이상, 세 번 넘게 연달아 실패하는 중이면 주의. " +
			"읽는 쪽이 꺼졌으면 기록은 그 자리에서 기다렸다가 차례로 가고, 읽는 쪽이 살아 있는데 한 기록만 다섯 번 처리하지 못하면 그 기록은 격리함으로 가요.",
		Fix:  consumer + " 컨테이너 로그를 보세요. 권한 오류면 그 서비스의 데이터베이스 역할을 확인하세요.",
		Deps: append([]string{"svc.input"}, deps...), Every: 30 * time.Second,
		Run: func(ctx context.Context) check.Verdict {
			f := e.contract(ctx, e.InputURL+"/health")
			if f.err != nil || f.code != http.StatusOK {
				return fail("입구의 상태를 읽지 못했어요.")
			}
			for _, l := range f.report.Loops {
				if l.Name != "deliver:"+consumer {
					continue
				}
				v := judgeLoops(fetched{report: health.Report{Loops: []health.Loop{l}}, took: f.took}, nil, time.Now())
				if v.State == check.OK {
					v.Summary = "차례로 읽고 있어요."
				}
				if l.Behind != nil {
					v.ObservedAt, v.ObservedLabel = l.Behind, "기다리는 가장 오래된 기록"
				}
				return v
			}
			return check.Verdict{State: check.Unknown, Summary: "아직 등록하지 않았어요(" + consumer + ")."}
		},
	}
}

// hostService is a service that runs on the Mac mini itself rather than in
// compose (the voice, the text reader), reached at host.docker.internal.
func (e *env) hostService(id, name, u, rule, fix string, judge func(body map[string]any) check.Verdict) check.Probe {
	return check.Probe{
		ID: id, Group: "기반", Name: name, Rule: rule, Fix: fix, Every: time.Minute,
		Run: func(ctx context.Context) check.Verdict {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				return fail(err.Error())
			}
			start := time.Now()
			res, err := e.client.Do(req)
			if err != nil {
				return fail("응답이 없어요. 맥미니에서 돌고 있지 않은 것 같아요.")
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK {
				return fail(fmt.Sprintf("응답 코드 %d.", res.StatusCode))
			}
			body := map[string]any{}
			_ = json.NewDecoder(res.Body).Decode(&body)
			v := judge(body)
			if v.State == check.OK && v.Summary == "" {
				v.Summary = fmt.Sprintf("응답해요 (%dms).", time.Since(start).Milliseconds())
			}
			return v
		},
	}
}

// redisPing asks a Redis-speaking server (FalkorDB) PING over a bare
// connection: the graph memory lives there, and memory-svc's own /health
// does not touch it.
func (e *env) redisPing(addr string) func(context.Context) check.Verdict {
	return func(ctx context.Context) check.Verdict {
		var d net.Dialer
		start := time.Now()
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fail("연결되지 않아요.")
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
			return fail("PING 을 보내지 못했어요.")
		}
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "+PONG") {
			return fail("PING 에 답하지 않아요: " + clip(strings.TrimSpace(line), 80))
		}
		return check.Verdict{State: check.OK, Summary: fmt.Sprintf("응답해요 (%dms).", time.Since(start).Milliseconds())}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ago is a past moment the way the rows say it: "3분 전", "2일 전".
func ago(d time.Duration) string {
	if d < time.Minute {
		return "방금"
	}
	return span(d) + " 전"
}
