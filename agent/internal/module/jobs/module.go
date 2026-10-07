// Package jobs is how the assistant does things over time, for any kind of
// task: "이거 중고나라에 올리고 반응 봐줘", "이 상품 가격 내려가면 알려줘",
// "택배 도착하면 알려줘".
//
// In conversation the model hands such a task over with create_job. From then
// on the scheduler (scheduler.go) runs it in the background with the shared
// browser, and each run decides for itself what comes next - check again at
// a time it picks, wait for the operator on a card, or finish. Between runs
// the job lives in jobs-svc: what it remembers, what it is tracking, and a
// log the 작업 tab shows as progress.
//
// What every job may need is built once here, as tools a run can call
// (runtools.go): ask the operator on a card (with a form if it needs values),
// ask for a login on a site, record what it tracks, schedule its next look.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/agent/internal/uploads"
)

var (
	_ module.Editor        = (*Module)(nil)
	_ module.ContextSource = (*Module)(nil)
)

const (
	Name       = "job"
	ServerName = "jobs"
	// KindAsk is a card a job raises: a question, an approval before acting,
	// or a form to fill in.
	KindAsk = "job.ask"
	// KindLogin asks the operator to log in to a site in the 화면 tab.
	KindLogin = "job.login"
)

// QualifiedToolName is how Claude Code refers to one of the chat's tools.
func QualifiedToolName(tool string) string {
	return fmt.Sprintf("mcp__%s__%s", ServerName, tool)
}

// Notifier puts a line in the conversation without a model turn.
type Notifier interface {
	AppendAgent(paragraphs []string) *thread.Turn
}

type Module struct {
	store     *Store
	uploads   *uploads.Store
	proposals *proposal.Store
	notify    Notifier
	log       *slog.Logger
	// kick wakes the scheduler: a job created, a card answered, a login done.
	kick chan struct{}
	// current is the run in flight; the run tools answer only for it.
	current running
}

func New(store *Store, up *uploads.Store, proposals *proposal.Store, notify Notifier, log *slog.Logger) *Module {
	m := &Module{store: store, uploads: up, proposals: proposals, notify: notify, log: log, kick: make(chan struct{}, 1)}
	proposals.OnDecide(m.decided)
	return m
}

func (m *Module) Name() string                { return Name }
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

func (m *Module) wake() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Specs: the two cards a job can raise. Nothing here is a tool the chat's
// model calls through the gate - the cards are opened by runs directly.
func (m *Module) Specs() []action.Spec {
	return []action.Spec{
		{Kind: KindAsk, Summary: "작업이 운영자에게 묻거나 승인을 받습니다."},
		{Kind: KindLogin, Summary: "작업이 사이트 로그인을 요청합니다."},
	}
}

// AskInput is a job's card, stored as the proposal's action input.
type AskInput struct {
	JobID    int64          `json:"jobId"`
	JobTitle string         `json:"jobTitle"`
	Question string         `json:"question"`
	Body     string         `json:"body,omitempty"`
	Images   []string       `json:"images,omitempty"`
	Fields   []action.Field `json:"fields,omitempty"`
}

type LoginInput struct {
	Site   string `json:"site"`
	Reason string `json:"reason,omitempty"`
}

func askCard(in AskInput) action.Card {
	body := "작업: " + in.JobTitle
	if strings.TrimSpace(in.Body) != "" {
		body += "\n\n" + in.Body
	}
	return action.Card{
		Title:       in.Question,
		Body:        body,
		Consequence: "승인하면 작업이 이 내용으로 이어서 합니다. 반려하면 하지 않습니다.",
		Images:      in.Images,
		Fields:      in.Fields,
	}
}

func loginCard(in LoginInput) action.Card {
	body := in.Site + " 에 로그인이 필요합니다."
	if in.Reason != "" {
		body += "\n" + in.Reason
	}
	return action.Card{
		Title:       "다시 로그인해 주세요: " + in.Site,
		Body:        body + "\n\n화면 탭에서 브라우저로 로그인한 뒤 승인을 누르세요.",
		Consequence: "승인하면 이 사이트를 쓰는 작업을 이어서 합니다. 반려하면 계속 기다립니다.",
	}
}

func (m *Module) Preview(_ context.Context, a action.Action) (action.Card, error) {
	switch a.Kind {
	case KindAsk:
		var in AskInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Card{}, err
		}
		return askCard(in), nil
	case KindLogin:
		var in LoginInput
		if err := json.Unmarshal(a.Input, &in); err != nil {
			return action.Card{}, err
		}
		return loginCard(in), nil
	}
	return action.Card{}, fmt.Errorf("모르는 동작입니다: %s", a.Kind)
}

// Execute is never reached for these cards - their decisions go through
// decided - but the registry wants an Actuator.
func (m *Module) Execute(context.Context, action.Action) (action.Result, error) {
	return action.Result{}, errors.New("작업 카드는 실행하지 않습니다")
}

// Revise applies a form card's edits and checks required fields.
func (m *Module) Revise(_ context.Context, a action.Action, edits map[string]string) (action.Action, action.Card, error) {
	if a.Kind != KindAsk {
		return a, action.Card{}, errors.New("이 카드는 고칠 수 없습니다.")
	}
	var in AskInput
	if err := json.Unmarshal(a.Input, &in); err != nil {
		return a, action.Card{}, err
	}
	known := map[string]bool{}
	for i, f := range in.Fields {
		known[f.Key] = true
		if v, ok := edits[f.Key]; ok {
			in.Fields[i].Value = strings.TrimSpace(v)
		}
		if f.Kind == "select" && in.Fields[i].Value != "" && len(f.Options) > 0 && !contains(f.Options, in.Fields[i].Value) {
			return a, action.Card{}, fmt.Errorf("%s: 고를 수 있는 값이 아닙니다.", f.Label)
		}
		if in.Fields[i].Required && in.Fields[i].Value == "" {
			return a, action.Card{}, fmt.Errorf("%s 을(를) 적어 주세요.", f.Label)
		}
	}
	for k := range edits {
		if !known[k] {
			return a, action.Card{}, fmt.Errorf("고칠 수 없는 항목입니다: %s", k)
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return a, action.Card{}, err
	}
	return action.Action{Kind: a.Kind, Input: raw}, askCard(in), nil
}

// decided carries a card's decision back to the job that raised it.
func (m *Module) decided(p proposal.Proposal, d proposal.Decision) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	switch p.Action.Kind {
	case KindAsk:
		var in AskInput
		if json.Unmarshal(p.Action.Input, &in) != nil {
			return
		}
		if err := m.store.Answer(ctx, in.JobID, p.ID, answerText(in, d)); err != nil {
			m.proposals.Settle(p.ID, proposal.Failed, "작업에 전하지 못했습니다: "+err.Error())
			return
		}
		if d == proposal.Approve {
			m.proposals.Settle(p.ID, proposal.Executed, "작업에 전했습니다. 작업 탭에서 이어지는 걸 볼 수 있습니다.")
		} else {
			m.proposals.Settle(p.ID, proposal.Rejected, "작업에 반려로 전했습니다.")
		}
		m.wake()
	case KindLogin:
		var in LoginInput
		if json.Unmarshal(p.Action.Input, &in) != nil {
			return
		}
		if d != proposal.Approve {
			m.proposals.Settle(p.ID, proposal.Rejected, "로그인을 기다리는 동안 이 사이트의 작업은 쉽니다.")
			return
		}
		if err := m.store.SetLogin(ctx, in.Site, false, ""); err != nil {
			m.proposals.Settle(p.ID, proposal.Failed, err.Error())
			return
		}
		m.proposals.Settle(p.ID, proposal.Executed, "이 사이트의 작업을 이어서 합니다.")
		m.wake()
	}
}

func answerText(in AskInput, d proposal.Decision) string {
	if d != proposal.Approve {
		return fmt.Sprintf("[반려] %s", in.Question)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[승인] %s", in.Question)
	for _, f := range in.Fields {
		fmt.Fprintf(&b, "\n- %s: %s", f.Label, f.Value)
	}
	return b.String()
}

func (m *Module) Facts(ctx context.Context, _ string) ([]module.Fact, error) {
	js, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []module.Fact
	for _, j := range js {
		if j.State == "active" || j.State == "waiting" {
			out = append(out, module.Fact{Source: Name, Text: line(j)})
		}
	}
	return out, nil
}

// --- 대화 도구 ---------------------------------------------------------------

type CreateInput struct {
	Title        string   `json:"title" jsonschema:"작업 탭에 보일 짧은 이름. 예: '에어팟 프로 2 중고나라 판매'"`
	Goal         string   `json:"goal" jsonschema:"끝났을 때 무엇이 이뤄져 있어야 하는지. 예: '중고나라에 판매 글을 올리고, 팔릴 때까지 반응을 지켜보다 판매되면 끝낸다.'"`
	Instructions string   `json:"instructions,omitempty" jsonschema:"운영자가 말한 조건과 선호를 빠짐없이: 가격 정책, 거래 방식, 알려 달라는 때, 하지 말라는 것. 대화에서 알아낸 사실(사진 속 물건 정보 등)도."`
	Sites        []string `json:"sites,omitempty" jsonschema:"로그인해서 쓸 사이트 호스트. 예: ['web.joongna.com']"`
	Photos       []string `json:"photos,omitempty" jsonschema:"쓸 첨부 사진 파일 이름(경로의 마지막 부분)."`
}

type InstructInput struct {
	ID   int64  `json:"id" jsonschema:"작업 id. list_jobs 로 확인한다."`
	Text string `json:"text" jsonschema:"작업에 전할 지시. 예: '가격을 6만 원으로 내려줘'"`
}

type ListInput struct{}

func (m *Module) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "create_job",
		Description: "시간이 걸리거나 나중에 다시 봐야 하는 일, 웹사이트에서 백그라운드로 해야 하는 일을 작업으로 맡긴다. " +
			"작업은 백그라운드에서 브라우저로 진행되고, 필요하면 스스로 주기적으로 다시 확인하며, 운영자에게 카드로 묻거나 로그인을 요청한다. " +
			"진행 상황은 작업 탭에서 보인다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CreateInput) (*mcp.CallToolResult, any, error) {
		for i, p := range in.Photos {
			if j := strings.LastIndex(p, "/"); j >= 0 {
				in.Photos[i] = p[j+1:]
			}
			if _, err := m.uploads.Path(in.Photos[i]); err != nil {
				return toolText(fmt.Sprintf("사진 %s 를 찾을 수 없습니다. 대화에 첨부된 사진만 쓸 수 있습니다.", in.Photos[i]), true), nil, nil
			}
		}
		j, err := m.store.Create(ctx, NewJob(in))
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		m.wake()
		return toolText(fmt.Sprintf("작업 %d '%s' 를 만들었습니다. 운영자는 이걸 이미 압니다: "+
			"작업을 만들었다, 백그라운드에서 시작한다, 작업 탭에서 볼 수 있다 같은 안내는 하지 마세요. "+
			"운영자에게 물어볼 것이나 알려야 할 다른 사실이 있을 때만 그것을 말하고, 없으면 '맡겼습니다.' 한마디로 끝내세요.", j.ID, j.Title), false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_jobs",
		Description: "맡긴 작업들의 현황: 상태, 한 줄 요약, 다음 확인 시각, 기다리는 카드. 읽기만 한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, any, error) {
		js, err := m.store.List(ctx)
		if err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		return toolText(renderList(js), false), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "instruct_job",
		Description: "진행 중인 작업에 지시를 더한다(가격 변경, 중단 조건 등). 작업이 곧 다시 실행되어 반영한다.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in InstructInput) (*mcp.CallToolResult, any, error) {
		if err := m.store.Instruct(ctx, in.ID, in.Text); err != nil {
			return toolText(err.Error(), true), nil, nil
		}
		m.wake()
		return toolText("작업에 전했습니다. 곧 반영합니다.", false), nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}

var stateLabel = map[string]string{
	"active": "진행 중", "waiting": "승인 대기", "paused": "멈춤",
	"done": "끝남", "cancelled": "그만둠", "failed": "실패",
}

func line(j Job) string {
	s := fmt.Sprintf("[%d · %s] %s", j.ID, stateLabel[j.State], j.Title)
	if j.Summary != "" {
		s += " — " + j.Summary
	}
	if len(j.BlockedSites) > 0 {
		s += " (로그인 대기: " + strings.Join(j.BlockedSites, ", ") + ")"
	} else if j.State == "active" && j.NextAt != nil {
		s += " (다음 " + j.NextAt.Local().Format("1/2 15:04") + ")"
	}
	return s
}

func renderList(js []Job) string {
	if len(js) == 0 {
		return "맡긴 작업이 없습니다."
	}
	var b strings.Builder
	for _, j := range js {
		b.WriteString("- " + line(j) + "\n")
	}
	return b.String()
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func toolText(s string, isErr bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isErr, Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
