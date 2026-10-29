package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// RunServerName is the MCP server a background run is given, mounted at
// /mcp/job-run/<run id>. Never in the chat's MCP config.
const RunServerName = "job"

// running is every run in flight, by run id. The run tools answer only for
// one of them: a URL with any other run id gets nothing. At most one run
// holds the shared browser; the others are jobs with no sites, running
// beside the conversation.
type running struct {
	mu   sync.Mutex
	runs map[int64]*runEntry
}

type runEntry struct {
	job     Job
	browser bool
	// parked is the card this run left the job waiting on, until it is
	// decided or the run puts the job back on a clock.
	parked string
	// decided: the run said what comes next (a clock, a card, a login, a
	// payment to wait on, the end).
	decided bool
	// finished is what the run's finish said, once it was called: the job
	// is done (the phone hears it when the run ends).
	finished    string
	hasFinished bool
	// firstFor is the job the operator put ahead of this run (the 먼저
	// card): the run gave the browser to it, and keeps its place in line.
	firstFor string
}

func (r *running) add(run int64, j Job, browser bool) {
	r.mu.Lock()
	if r.runs == nil {
		r.runs = map[int64]*runEntry{}
	}
	r.runs[run] = &runEntry{job: j, browser: browser}
	r.mu.Unlock()
}

func (r *running) remove(run int64) {
	r.mu.Lock()
	delete(r.runs, run)
	r.mu.Unlock()
}

// park records the card the run's job now waits on ("" when it no longer does).
func (r *running) park(run int64, card string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.runs[run]; e != nil {
		e.parked = card
	}
}

// unpark: the card was decided, so nothing waits on it any more.
func (r *running) unpark(card string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.runs {
		if e.parked == card {
			e.parked = ""
		}
	}
}

// decide notes that the run said what comes next.
func (r *running) decide(run int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.runs[run]; e != nil {
		e.decided = true
	}
}

// finish notes what the run's finish said.
func (r *running) finish(run int64, summary string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.runs[run]; e != nil {
		e.finished, e.hasFinished = summary, true
	}
}

// finishedOf is what the run's finish said, and whether it was called.
func (r *running) finishedOf(run int64) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.runs[run]; e != nil {
		return e.finished, e.hasFinished
	}
	return "", false
}

// putBehind notes that the run gives the browser to the job titled first.
func (r *running) putBehind(run int64, first string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.runs[run]; e != nil {
		e.firstFor = first
	}
}

// behind is the job the run gave the browser to, "" when none.
func (r *running) behind(run int64) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.runs[run]; e != nil {
		return e.firstFor
	}
	return ""
}

func (r *running) decidedOf(run int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.runs[run]
	return e != nil && e.decided
}

func (r *running) parkedOn(run int64) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.runs[run]; e != nil {
		return e.parked
	}
	return ""
}

// browserOf is whether the run holds the shared browser.
func (r *running) browserOf(run int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.runs[run]
	return e != nil && e.browser
}

// widen adds sites to the run's job, for the rest of the run.
func (r *running) widen(run int64, sites []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.runs[run]
	if e == nil {
		return nil
	}
	e.job.Sites = append(append([]string(nil), e.job.Sites...), sites...)
	return append([]string(nil), e.job.Sites...)
}

func (r *running) get(run int64) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.runs[run]; e != nil {
		return e.job, true
	}
	return Job{}, false
}

// now is the run that holds the browser, if one does: the one a payment the
// browser's guard stopped belongs to.
func (r *running) now() (int64, Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for run, e := range r.runs {
		if e.browser {
			return run, e.job
		}
	}
	return 0, Job{}
}

// report keeps the run's job at its latest one-line status: a payment card
// raised later in the run shows it.
func (r *running) report(run int64, summary string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.runs[run]; e != nil {
		e.job.Summary = summary
	}
}

// payCard is a payment card the browser's guard raised for a job's run, and
// what became of it.
type payCard struct {
	id    string
	state string // payPending, payApproved, payRejected
}

const (
	payPending  = "pending"
	payApproved = "approved"
	payRejected = "rejected"
)

// payments are each job's payment card, from the moment its run was stopped
// at a payment until a later run starts after the decision.
type payments struct {
	mu    sync.Mutex
	cards map[int64]payCard
}

func (p *payments) get(job int64) (payCard, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.cards[job]
	return c, ok
}

func (p *payments) set(job int64, c payCard) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cards == nil {
		p.cards = map[int64]payCard{}
	}
	p.cards[job] = c
}

// decided records a decision on the job's card, if it is still that card.
func (p *payments) decided(job int64, card string, approved bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cards[job]; ok && c.id == card {
		c.state = payRejected
		if approved {
			c.state = payApproved
		}
		p.cards[job] = c
	}
}

// settled forgets a decided card: once a run starts after the decision, the
// decision reaches it as a card answer.
func (p *payments) settled(job int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cards[job]; ok && c.state != payPending {
		delete(p.cards, job)
	}
}

func (p *payments) forget(job int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cards, job)
}

// RunningJob is the job whose run holds the browser now (httpapi.JobPayments).
func (m *Module) RunningJob() (int64, string, string, bool) {
	run, j := m.current.now()
	return j.ID, j.Title, j.Summary, run != 0
}

// PaymentRaised: the browser's guard stopped the job's run at a payment and
// a card is up for it. The run learns of it through wait_payment.
func (m *Module) PaymentRaised(job int64, card string) {
	if c, ok := m.pay.get(job); ok && c.id == card {
		return
	}
	m.pay.set(job, payCard{id: card, state: payPending})
}

// PaymentDecided hands the operator's decision on the job's payment card to
// the job, as a card answer that wakes it if it is waiting on that card.
func (m *Module) PaymentDecided(job int64, card string, approved bool, text string) error {
	m.pay.decided(job, card, approved)
	m.current.unpark(card)
	m.hear(card)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.store.Answer(ctx, job, card, text); err != nil {
		if !JobEnded(err) { // over: nothing to hand it on to later
			m.retrySoon()
		}
		return err
	}
	m.wake()
	return nil
}

type ProgressInput struct {
	Text string `json:"text" jsonschema:"지금 무엇을 하고 있는지 한 줄. 운영자가 작업 탭에서 본다. 예: '판매 글 양식에 사진 3장을 넣었습니다'"`
}

type PhotosInput struct {
	Names []string `json:"names" jsonschema:"볼 사진 이름들(작업에 적힌 사진)."`
}

type FieldInput struct {
	Key      string   `json:"key" jsonschema:"영문 키. 예: price"`
	Label    string   `json:"label" jsonschema:"운영자가 보는 이름. 예: 가격(원)"`
	Kind     string   `json:"kind" jsonschema:"text | textarea | number | select"`
	Value    string   `json:"value,omitempty" jsonschema:"미리 채울 값. 운영자가 정해야 하는 값(가격 등)은 비운다."`
	Options  []string `json:"options,omitempty" jsonschema:"select 의 선택지"`
	Required bool     `json:"required,omitempty" jsonschema:"비어 있으면 승인할 수 없게 할지"`
	Hint     string   `json:"hint,omitempty" jsonschema:"칸 아래 한 줄 도움말(시세 등)"`
}

type AskToolInput struct {
	Question string       `json:"question" jsonschema:"카드 제목. 운영자가 무엇을 결정하는지. 예: '이 내용으로 중고나라에 올릴까요?'"`
	Body     string       `json:"body,omitempty" jsonschema:"판단에 필요한 정보(시세, 바뀌는 점, 결과). 짧게."`
	Fields   []FieldInput `json:"fields,omitempty" jsonschema:"운영자가 고치거나 채울 값. 없으면 승인/반려만."`
	Images   []string     `json:"images,omitempty" jsonschema:"카드에 보일 사진 이름"`
}

type SitesToolInput struct {
	Sites  []string `json:"sites" jsonschema:"더 열어야 할 사이트 호스트. 예: ['ticket.melon.com']"`
	Reason string   `json:"reason" jsonschema:"왜 필요한지 한 줄. 운영자가 카드에서 본다."`
}

type LoginToolInput struct {
	Site   string `json:"site" jsonschema:"로그인이 필요한 사이트 호스트. 예: web.joongna.com"`
	Reason string `json:"reason,omitempty" jsonschema:"무엇이 막았는지 한 줄. 예: '네이버 보안문자'"`
}

type ScheduleInput struct {
	AfterMinutes int    `json:"afterMinutes,omitempty" jsonschema:"몇 분 뒤에 다시 볼지. at 과 둘 중 하나."`
	At           string `json:"at,omitempty" jsonschema:"다시 볼 시각. 'YYYY-MM-DD HH:MM' (한국 시간)."`
	Reason       string `json:"reason" jsonschema:"그때 무엇을 볼지 한 줄. 운영자가 작업 탭에서 본다."`
}

// FinishInput is the end of a job. Every end is told - in the conversation
// and on the phone - whatever the run thinks of it (2026-10-28: "무슨
// 작업이든 작업이 끝나면 알리기"); there is no quiet finish any more.
type FinishInput struct {
	Summary string `json:"summary" jsonschema:"결과 한두 줄. 운영자에게 그대로 알림으로 갑니다."`
}

type ReportInput struct {
	Summary string `json:"summary" jsonschema:"작업의 지금 현황 한 줄. 작업 탭의 요약이 된다. 예: '판매중 · 조회 48 · 찜 5 · 채팅 3'"`
	Notify  bool   `json:"notify,omitempty" jsonschema:"대화에도 알릴지. 운영자가 알고 싶어 할 변화(판매, 가격 하락, 도착, 문제)일 때만 true"`
}

type MemoryInput struct {
	Key   string `json:"key" jsonschema:"짧은 키. 예: posted_url, form_steps"`
	Value string `json:"value" jsonschema:"다음 실행이 알아야 할 것. 빈 문자열이면 지운다."`
}

type RecordToolInput struct {
	Key     string           `json:"key" jsonschema:"대상의 고유 id(외부 글 번호, 송장 번호, 상품 URL 등). 같은 key 면 덮어쓴다."`
	Title   string           `json:"title" jsonschema:"대상 이름"`
	Status  string           `json:"status,omitempty" jsonschema:"상태 한 단어. 예: 판매중, 예약중, 판매완료, 배송중, 도착"`
	Amount  *int64           `json:"amount,omitempty" jsonschema:"원 단위 금액(가격 등)"`
	Metrics map[string]int64 `json:"metrics,omitempty" jsonschema:"숫자들. 예: {'조회':48,'찜':5,'채팅':3}"`
	Image   string           `json:"image,omitempty" jsonschema:"대표 사진 이름(작업 사진 중 하나)"`
	URL     string           `json:"url,omitempty" jsonschema:"대상 주소"`
	Note    string           `json:"note,omitempty"`
	Remove  bool             `json:"remove,omitempty" jsonschema:"더 볼 필요 없는 대상을 지울 때 true"`
}

// RunHandler serves the run tools. The run id comes from the URL path.
func (m *Module) RunHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		run, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		return m.runServer(run)
	}, nil)
}

func (m *Module) runServer(run int64) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: RunServerName, Version: "1"}, nil)
	// Every tool checks the run is the one in flight before touching anything.
	guard := func() (Job, *mcp.CallToolResult) {
		j, ok := m.current.get(run)
		if !ok {
			return Job{}, toolText("이 실행은 이미 끝났습니다.", true)
		}
		return j, nil
	}
	ok := func(s string) *mcp.CallToolResult { return toolText(s, false) }
	fail := func(err error) *mcp.CallToolResult { return toolText(err.Error(), true) }
	// A run stopped at a payment has a card up already; another card, or a
	// next run on its own clock, would leave that one with nobody to wake.
	payUp := func(j Job) *mcp.CallToolResult {
		if c, up := m.pay.get(j.ID); up && c.state == payPending {
			return toolText("결제 승인 카드가 이미 운영자에게 올라가 있습니다. 카드를 더 올리거나 다음 실행을 잡지 말고 wait_payment 를 부른 뒤 실행을 마치세요.", true)
		}
		return nil
	}

	mcp.AddTool(server, &mcp.Tool{Name: "progress", Description: "지금 하는 일을 작업 탭에 한 줄 남긴다. 큰 단계마다 부른다."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ProgressInput) (*mcp.CallToolResult, any, error) {
			if _, bad := guard(); bad != nil {
				return bad, nil, nil
			}
			if err := m.store.Step(ctx, run, "progress", in.Text); err != nil {
				return fail(err), nil, nil
			}
			return ok("남겼습니다."), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "view_photos", Description: "작업에 첨부된 사진을 본다."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in PhotosInput) (*mcp.CallToolResult, any, error) {
			j, bad := guard()
			if bad != nil {
				return bad, nil, nil
			}
			res := &mcp.CallToolResult{}
			for _, name := range in.Names {
				if !contains(j.Photos, name) {
					return toolText(name+" 은 이 작업의 사진이 아닙니다.", true), nil, nil
				}
				if m.uploads == nil {
					return toolText(name+" 은 이 에이전트에서 볼 수 없습니다.", true), nil, nil
				}
				p, err := m.uploads.Path(name)
				if err != nil {
					return toolText(name+" 은 이미 지워졌습니다.", true), nil, nil
				}
				data, err := os.ReadFile(p)
				if err != nil {
					return fail(err), nil, nil
				}
				res.Content = append(res.Content, &mcp.ImageContent{Data: data, MIMEType: mimeOf(name)})
			}
			return res, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{
		Name: "ask",
		Description: "운영자에게 카드로 묻는다: 밖으로 나가는 행동(글 올리기, 삭제, 메시지 보내기, 예약·신청) 전의 승인, 운영자가 정해야 하는 값(가격 등)이 든 폼, 판단이 필요한 질문. " +
			"맡은 구매의 결제는 묻지 않는다 - 결제하기를 누르면 결제 승인 카드가 저절로 올라간다(wait_payment). " +
			"부른 뒤에는 바로 실행을 마친다 - 운영자가 승인하거나 반려하는 즉시 작업이 다시 실행되어 그 답으로 이어 가므로, 답을 확인하러 올 시각(schedule_next)은 잡지 않는다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in AskToolInput) (*mcp.CallToolResult, any, error) {
		j, bad := guard()
		if bad != nil {
			return bad, nil, nil
		}
		if up := payUp(j); up != nil {
			return up, nil, nil
		}
		ask := AskInput{JobID: j.ID, JobTitle: j.Title, Question: in.Question, Body: in.Body}
		for _, name := range in.Images {
			if contains(j.Photos, name) {
				ask.Images = append(ask.Images, name)
			}
		}
		for _, f := range in.Fields {
			kind := f.Kind
			if kind != "textarea" && kind != "number" && kind != "select" {
				kind = "text"
			}
			ask.Fields = append(ask.Fields, action.Field{
				Key: f.Key, Label: f.Label, Kind: kind, Value: f.Value,
				Options: f.Options, Required: f.Required, Hint: f.Hint,
			})
		}
		raw, _ := json.Marshal(ask)
		p, _ := m.proposals.Open(proposal.Proposal{
			Origin: proposal.OriginNotice,
			Action: action.Action{Kind: KindAsk, Input: raw},
			Card:   askCard(ask),
		})
		if err := m.store.Wait(ctx, run, p.ID, in.Question); err != nil {
			m.proposals.Abandon(p.ID, "작업에 걸지 못했습니다: "+err.Error())
			return fail(err), nil, nil
		}
		m.current.park(run, p.ID)
		m.current.decide(run)
		// In the conversation where it was raised, so it stays there once
		// decided instead of floating below the thread only while it waits.
		m.attach(p.ID)
		return ok("카드를 올렸습니다. 운영자가 승인하거나 반려하면 작업이 곧바로 다시 실행되어 그 답을 받습니다. " +
			"답을 확인하러 다시 올 시각을 잡지 말고(schedule_next 없이) 지금 실행을 마치세요."), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "request_sites",
		Description: "작업의 '쓰는 사이트'에 없는 사이트가 목표에 꼭 필요할 때 부른다(브라우저가 그 밖으로의 이동을 막는다). 카드 없이 바로 작업에 더하고, " +
			"운영자는 작업 탭의 기록에서 본다. 브라우저가 있는 실행이면 곧바로 열 수 있다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SitesToolInput) (*mcp.CallToolResult, any, error) {
		j, bad := guard()
		if bad != nil {
			return bad, nil, nil
		}
		var add []string
		for _, s := range in.Sites {
			if h := host(s); h != "" && !contains(add, h) && !contains(j.Sites, h) {
				add = append(add, h)
			}
		}
		if len(add) == 0 {
			return toolText("더할 사이트가 없습니다(이미 쓰는 사이트이거나 비어 있음).", true), nil, nil
		}
		// Browsing changes nothing, so no card (2026-10-20): the site goes on
		// the job and in its log, where the operator sees what it opened.
		if err := m.store.AddSites(ctx, j.ID, add); err != nil {
			return fail(err), nil, nil
		}
		line := "사이트 추가: " + strings.Join(add, ", ")
		if r := strings.TrimSpace(in.Reason); r != "" {
			line += " (" + r + ")"
		}
		_ = m.store.Step(ctx, run, "step", line)
		sites := m.current.widen(run, add)
		if !m.current.browserOf(run) {
			return ok("작업에 더했습니다: " + strings.Join(add, ", ") + ". 이번 실행에는 브라우저가 없으니 schedule_next 로 곧 다음 실행을 잡고 마치세요 - 다음 실행부터 열 수 있습니다."), nil, nil
		}
		if m.guard != nil {
			if err := m.guard.Restrict(ctx, sites, m.hold); err != nil {
				m.log.Warn("더한 사이트를 이번 실행에 열지 못했습니다", "job", j.ID, "err", err)
				return ok("작업에 더했지만 이번 실행의 브라우저에는 아직 열리지 않았습니다. schedule_next 로 곧 다음 실행을 잡고 마치세요."), nil, nil
			}
		}
		return ok("작업에 더했습니다: " + strings.Join(add, ", ") + ". 지금 열 수 있습니다."), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "request_login",
		Description: "사이트 로그인이 풀려 스스로 로그인할 수 없을 때 부른다. 운영자에게 로그인 카드를 올리고, 그 사이트를 쓰는 작업은 로그인될 때까지 쉰다. " +
			"부른 뒤에는 바로 실행을 마친다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LoginToolInput) (*mcp.CallToolResult, any, error) {
		if _, bad := guard(); bad != nil {
			return bad, nil, nil
		}
		site := host(in.Site)
		if site == "" {
			return toolText("site 가 비어 있습니다.", true), nil, nil
		}
		if err := m.store.SetLogin(ctx, site, true, in.Reason); err != nil {
			return fail(err), nil, nil
		}
		login := LoginInput{Site: site, Reason: in.Reason}
		raw, _ := json.Marshal(login)
		act := action.Action{Kind: KindLogin, Input: raw}
		if _, open := m.proposals.FindPending(act); !open {
			p, _ := m.proposals.Open(proposal.Proposal{Origin: proposal.OriginNotice, Action: act, Card: loginCard(login)})
			m.attach(p.ID)
		}
		// Due already skips jobs on a blocked site; this only makes the run
		// count as having decided, and comes back soon after the login.
		if _, err := m.store.Schedule(ctx, run, time.Now(), "로그인 뒤에 이어서"); err != nil {
			return fail(err), nil, nil
		}
		m.current.park(run, "")
		m.current.decide(run)
		return ok("로그인 카드를 올렸습니다. 운영자가 로그인하고 승인하면(또는 반려하면) 작업이 곧바로 다시 실행됩니다. 지금 실행을 마치세요."), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "schedule_next",
		Description: "이 작업을 언제 다시 실행할지 정한다. 목표상 나중에 바깥에서 다시 확인할 것이 있으면 반드시 부른다(최소 10분 뒤). " +
			"얼마나 자주 볼지는 대상이 얼마나 빨리 바뀌는지로 정한다. 카드의 답을 확인하러 오는 데는 쓰지 않는다 - 답이 오면 작업이 곧바로 다시 실행된다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ScheduleInput) (*mcp.CallToolResult, any, error) {
		j, bad := guard()
		if bad != nil {
			return bad, nil, nil
		}
		if up := payUp(j); up != nil {
			return up, nil, nil
		}
		// Waiting on a card is waiting on its decision, which wakes the job
		// the moment it is made - a clock beside it only came back to look
		// whether it was made yet (job 30, 2026-10-21: six runs, 18:21 to
		// 20:43, each finding "카드 답 대기 중").
		if open := m.waitingOn(j.ID); len(open) > 0 {
			last := open[len(open)-1]
			return toolText(fmt.Sprintf("카드 '%s'(%s)의 답을 기다리는 중입니다. 운영자가 승인하거나 반려하면 작업이 곧바로 다시 실행되어 그 답을 받으니, "+
				"답을 확인하러 다시 올 시각은 잡지 않습니다. schedule_next 없이 지금 실행을 마치세요 - 작업은 그 카드를 기다립니다.", last.Card.Title, last.ID), true), nil, nil
		}
		at := time.Now().Add(time.Duration(in.AfterMinutes) * time.Minute)
		if in.At != "" {
			t, err := time.ParseInLocation("2006-01-02 15:04", in.At, time.Local)
			if err != nil {
				return toolText("at 은 'YYYY-MM-DD HH:MM' 형식입니다.", true), nil, nil
			}
			at = t
		}
		got, err := m.store.Schedule(ctx, run, at, in.Reason)
		if err != nil {
			return fail(err), nil, nil
		}
		m.current.park(run, "")
		m.current.decide(run)
		return ok("다음 실행: " + got.Local().Format("2006-01-02 15:04") + ". 할 일이 끝났으면 실행을 마치세요."), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "wait_payment",
		Description: "결제하기를 눌렀는데 결제가 막혔을 때(쿠팡은 '시스템 오류: 주문중 오류' 창을 띄우고, 결제창(PG)은 빈 화면이나 차단 화면으로 뜬다) 부른다. 막혔다면 결제 승인 카드가 운영자에게 올라가 있다. " +
			"아직 결정 전이면 작업이 그 카드를 기다리게 하고, 이미 결정됐으면 무엇을 할지 알려 준다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, any, error) {
		j, bad := guard()
		if bad != nil {
			return bad, nil, nil
		}
		decided := func(c payCard) *mcp.CallToolResult {
			switch c.state {
			case payApproved:
				return ok("결제가 이미 승인됐습니다. 주문 내역에 이 주문이 없는지 확인한 뒤 결제하기를 한 번 다시 누르세요(이번에는 통과합니다). " +
					"결제 비밀번호 키패드가 나오면 browser_enter_payment_pin 을 부르세요.")
			case payRejected:
				return ok("운영자가 결제를 반려했습니다. 결제하지 말고 finish 로 마치세요.")
			}
			return nil
		}
		c, up := m.pay.get(j.ID)
		if !up {
			return toolText("이 작업에 올라간 결제 카드가 없습니다. 결제 요청이 막힌 것이 아니라 사이트 쪽 오류입니다 - 화면을 다시 확인하고, 계속되면 ask 로 알리세요.", true), nil, nil
		}
		if res := decided(c); res != nil {
			return res, nil, nil
		}
		if err := m.store.Wait(ctx, run, c.id, "결제 승인 대기"); err != nil {
			return fail(err), nil, nil
		}
		m.current.park(run, c.id)
		m.current.decide(run)
		// Decided while the job was being parked: the answer came before the
		// job waited on it, so nothing will wake it - put it back on a clock.
		if now, _ := m.pay.get(j.ID); now.id == c.id && now.state != payPending {
			if _, err := m.store.Schedule(ctx, run, time.Now(), "결제 카드 결정 뒤 이어서"); err != nil {
				return fail(err), nil, nil
			}
			m.current.park(run, "")
			return decided(now), nil, nil
		}
		return ok("결제 승인 카드를 기다립니다. 승인하거나 반려하면 작업이 바로 다시 실행됩니다. 지금 실행을 마치세요."), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{Name: "finish", Description: "목표를 이뤘거나 더 할 수 있는 일이 없을 때 작업을 끝낸다."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in FinishInput) (*mcp.CallToolResult, any, error) {
			j, bad := guard()
			if bad != nil {
				return bad, nil, nil
			}
			// A run that just asked the operator and then ends the job leaves the
			// card with nowhere to go: answering it wakes nothing (job 18,
			// 2026-10-18, "어떻게 할까요?" then finish).
			if card := m.current.parkedOn(run); card != "" {
				return toolText("이 실행에서 올린 카드("+card+")가 운영자의 답을 기다립니다. 작업을 끝내면 그 답이 갈 곳이 없어집니다. "+
					"finish 를 부르지 말고 지금 실행을 마치세요 - 답이 오면 다음 실행에서 이어집니다.", true), nil, nil
			}
			if err := m.store.Finish(ctx, run, in.Summary); err != nil {
				return fail(err), nil, nil
			}
			m.current.decide(run)
			m.current.finish(run, in.Summary)
			// A job that ends does not pay: its payment card, if still up, goes.
			if c, up := m.pay.get(j.ID); up {
				if c.state == payPending {
					m.proposals.Abandon(c.id, "작업이 끝나 결제하지 않습니다.")
				}
				m.pay.forget(j.ID)
			}
			m.say(j, run, "끝냈습니다. "+in.Summary)
			return ok("작업을 끝냈습니다. 운영자에게 알렸습니다. 실행을 마치세요."), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "report", Description: "작업의 지금 현황을 한 줄로 갱신한다. 알릴 만한 변화면 notify=true 로 대화에도 남긴다."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ReportInput) (*mcp.CallToolResult, any, error) {
			j, bad := guard()
			if bad != nil {
				return bad, nil, nil
			}
			if err := m.store.Report(ctx, run, in.Summary); err != nil {
				return fail(err), nil, nil
			}
			m.current.report(run, in.Summary)
			if in.Notify {
				m.say(j, run, in.Summary)
			}
			return ok("갱신했습니다."), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "remember",
		Description: "다음 실행에 남길 메모. 이미 한 일(올린 글 주소, 보낸 것)은 하기 직전과 직후에 남겨, 같은 일을 두 번 하지 않게 한다. 알아낸 사이트 절차도 남긴다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryInput) (*mcp.CallToolResult, any, error) {
		if _, bad := guard(); bad != nil {
			return bad, nil, nil
		}
		if err := m.store.Remember(ctx, run, in.Key, in.Value); err != nil {
			return fail(err), nil, nil
		}
		return ok("남겼습니다."), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "record",
		Description: "작업이 지켜보는 대상(판매 글, 매물, 택배 등)의 현황을 남긴다. 작업 탭에 카드로 보인다. 같은 key 는 덮어쓴다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in RecordToolInput) (*mcp.CallToolResult, any, error) {
		j, bad := guard()
		if bad != nil {
			return bad, nil, nil
		}
		if in.Image != "" && !contains(j.Photos, in.Image) {
			in.Image = ""
		}
		if err := m.store.Record(ctx, run, RecordInput(in)); err != nil {
			return fail(err), nil, nil
		}
		return ok("남겼습니다."), nil, nil
	})
	return server
}

// attach puts a card a run raised in the conversation, as its own turn: it
// stays where it was raised once decided, rather than floating below the
// thread only while it waits (and vanishing the moment it is answered - the
// "card that never came" of 2026-10-21 17:55).
func (m *Module) attach(proposalID string) {
	if m.notify != nil {
		m.notify.AttachProposal(proposalID)
	}
}

// say puts a job run's news in the conversation - as the run's card, which
// its next word replaces. The card carries the job's title itself.
func (m *Module) say(j Job, run int64, text string) {
	if m.notify == nil {
		return
	}
	m.notify.JobNews(thread.Origin{Kind: thread.OriginJob, Job: j.ID, Run: run, Title: j.Title}, []string{text})
}

func mimeOf(name string) string {
	switch {
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".webp"):
		return "image/webp"
	}
	return "image/jpeg"
}

func host(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}
