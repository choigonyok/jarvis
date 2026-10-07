package permission

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/module/calendar"
)

func TestModuleKind(t *testing.T) {
	cases := map[string]string{
		"mcp__calendar__create_event": "calendar.create_event",
		"mcp__calendar__list_events":  "calendar.list_events",
		"Bash":                        "",
		"mcp__broken":                 "",
	}
	for tool, want := range cases {
		got, ok := moduleKind(tool)
		if want == "" && ok {
			t.Fatalf("%s: 모듈 도구가 아닌데 매핑됐습니다: %s", tool, got)
		}
		if want != "" && got != want {
			t.Fatalf("%s: got %q, want %q", tool, got, want)
		}
	}
}

// The payoff of the registry: a module's own renderer writes the card, so the
// operator reads the change rather than the JSON that encodes it.
func TestModuleActionGetsTheModulesCard(t *testing.T) {
	// 생성 카드는 저장소를 읽지 않는다(수정·삭제만 현재 상태를 본다), 그래서
	// 닿지 않는 주소로도 이 테스트는 성립한다. 여기서 검증하는 것은 레지스트리가
	// 모듈의 렌더러를 찾아오는지이지 캘린더의 저장이 아니다.
	store := calendar.NewStore("http://127.0.0.1:1", "", bus.New())
	modules := module.NewRegistry()
	modules.Add(calendar.New(store))

	g := &Gate{modules: modules}
	_, card, err := g.describe("mcp__calendar__create_event",
		map[string]any{"date": "2026-09-25", "start": "19:00", "title": "합주"}, nil)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if card.Title != "일정을 추가합니다" {
		t.Fatalf("모듈 카드가 아닙니다: %+v", card)
	}
	if card.Body != "9월 25일 (금) 19:00  합주" {
		t.Fatalf("카드 본문: %q", card.Body)
	}
}

// A tool Claude Code runs itself has no module behind it; the card must still
// show the literal call rather than failing.
func TestUnknownToolFallsBackToGenericCard(t *testing.T) {
	g := &Gate{modules: module.NewRegistry()}
	act, card, err := g.describe("Bash", map[string]any{"command": "rm -rf ./cache"}, map[string]any{"command": "rm -rf ./cache"})
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if act.Kind != "claude.Bash" || card.Body != "rm -rf ./cache" {
		t.Fatalf("got %+v / %+v", act, card)
	}
}

type fakeTranscript struct{ attached []string }

func (f *fakeTranscript) AttachProposal(id string) { f.attached = append(f.attached, id) }

type fakeFollowUp struct{ prompts []string }

func (f *fakeFollowUp) Enqueue(p string) { f.prompts = append(f.prompts, p) }

type fakeActuator struct{ ran []action.Action }

func (f *fakeActuator) Name() string                { return "fake" }
func (f *fakeActuator) Start(context.Context) error { return nil }
func (f *fakeActuator) Stop(context.Context) error  { return nil }
func (f *fakeActuator) Specs() []action.Spec        { return []action.Spec{{Kind: "fake.write"}} }
func (f *fakeActuator) Preview(context.Context, action.Action) (action.Card, error) {
	return action.Card{Title: "쓰기"}, nil
}
func (f *fakeActuator) Execute(_ context.Context, a action.Action) (action.Result, error) {
	f.ran = append(f.ran, a)
	return action.Result{Note: "썼습니다"}, nil
}

func newTestGate(t *testing.T, modules *module.Registry) (*Gate, *proposal.Store, *fakeFollowUp) {
	t.Helper()
	store := proposal.NewStore(bus.New())
	follow := &fakeFollowUp{}
	g := NewGate(store, modules, &fakeTranscript{}, false, slog.Default())
	g.SetFollowUp(follow)
	return g, store, follow
}

func behavior(t *testing.T, res *mcp.CallToolResult) Response {
	t.Helper()
	var r Response
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &r); err != nil {
		t.Fatalf("응답을 읽지 못했습니다: %v", err)
	}
	return r
}

// The card goes up and the call returns at once - nothing holds the turn open
// for a person, which is what used to time out and raise the card twice.
func TestCardDoesNotBlockAndIsNotRaisedTwice(t *testing.T) {
	g, store, _ := newTestGate(t, module.NewRegistry())
	req := Request{"tool_name": "Bash", "input": map[string]any{"command": "ls"}}

	done := make(chan *mcp.CallToolResult, 1)
	go func() {
		res, _, _ := g.decide(context.Background(), nil, req)
		done <- res
	}()
	select {
	case res := <-done:
		if behavior(t, res).Behavior != "deny" {
			t.Fatalf("결정 전에는 실행되면 안 됩니다")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("카드를 올리고 바로 반환해야 합니다")
	}

	// The model asks again: same question, same card.
	res, _, _ := g.decide(context.Background(), nil, req)
	if !strings.Contains(behavior(t, res).Message, "이미 올라가") {
		t.Fatalf("중복 요청이 새 카드처럼 응답했습니다: %+v", behavior(t, res))
	}
	if n := store.PendingCount(); n != 1 {
		t.Fatalf("대기 카드 %d개, 1개여야 합니다", n)
	}
}

// Approving a CLI tool wakes the model with a one-time grant for exactly
// that call: it passes once, and a second identical call is a new card.
func TestApprovalGrantsTheExactCallOnce(t *testing.T) {
	g, store, follow := newTestGate(t, module.NewRegistry())
	req := Request{"tool_name": "Bash", "input": map[string]any{"command": "ls"}}
	g.decide(context.Background(), nil, req)
	p := store.List()[0]

	g.Resolve(p, proposal.Approve)
	if len(follow.prompts) != 1 || !strings.HasPrefix(follow.prompts[0], "[승인됨]") {
		t.Fatalf("승인이 모델에게 전달되지 않았습니다: %v", follow.prompts)
	}

	other := Request{"tool_name": "Bash", "input": map[string]any{"command": "rm -rf /"}}
	if res, _, _ := g.decide(context.Background(), nil, other); behavior(t, res).Behavior == "allow" {
		t.Fatal("승인되지 않은 인자가 통과했습니다")
	}
	if res, _, _ := g.decide(context.Background(), nil, req); behavior(t, res).Behavior != "allow" {
		t.Fatal("승인된 호출이 통과하지 못했습니다")
	}
	store.Decide(p.ID, proposal.Approve) // settle the first card so it is not "already pending"
	if res, _, _ := g.decide(context.Background(), nil, req); behavior(t, res).Behavior == "allow" {
		t.Fatal("승인은 한 번만 써야 합니다")
	}
}

// A module's action runs here, on approval, with the input the card showed.
func TestApprovedModuleActionRunsAndReports(t *testing.T) {
	act := &fakeActuator{}
	modules := module.NewRegistry()
	modules.Add(act)
	g, store, follow := newTestGate(t, modules)

	g.decide(context.Background(), nil, Request{"tool_name": "mcp__fake__write", "input": map[string]any{"x": 1}})
	p := store.List()[0]
	g.Resolve(p, proposal.Approve)

	if len(act.ran) != 1 || string(act.ran[0].Input) != `{"x":1}` {
		t.Fatalf("모듈이 실행되지 않았습니다: %+v", act.ran)
	}
	if got, _ := store.Get(p.ID); got.State != proposal.Executed {
		t.Fatalf("상태 %s, executed 여야 합니다", got.State)
	}
	if len(follow.prompts) != 1 || !strings.Contains(follow.prompts[0], "썼습니다") {
		t.Fatalf("실행 결과가 전달되지 않았습니다: %v", follow.prompts)
	}
}

func TestRejectionWakesTheModel(t *testing.T) {
	g, store, follow := newTestGate(t, module.NewRegistry())
	g.decide(context.Background(), nil, Request{"tool_name": "Bash", "input": map[string]any{"command": "ls"}})
	g.Resolve(store.List()[0], proposal.Reject)
	if len(follow.prompts) != 1 || !strings.HasPrefix(follow.prompts[0], "[반려]") {
		t.Fatalf("반려가 전달되지 않았습니다: %v", follow.prompts)
	}
}
