package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/policy"
	"github.com/choigonyok/jarvis/gocommon/record"
)

func policyServer(t *testing.T, d Deps) (*Server, *record.Memory) {
	t.Helper()
	d.Proposals = proposal.NewStore(bus.New())
	d.Policy = &policy.Policy{Builtins: []string{"Read"}, Servers: []string{"calendar", "mcp-browser"}, Workspace: "/workspace"}
	d.MCPToken = "mcp"
	if d.Runner == nil {
		d.Runner = bgRunner{}
	}
	return recordingServer(t, d)
}

func askPolicy(t *testing.T, s *Server, token, body string) (int, policy.Verdict) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/policy/check", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var v policy.Verdict
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return rec.Code, v
}

const pinCall = `{"tool_name":"mcp__mcp-browser__browser_enter_payment_pin","tool_input":{}}`

func TestThePolicyAnswersOnlyTheCLI(t *testing.T) {
	s, _ := policyServer(t, Deps{})
	if code, _ := askPolicy(t, s, "", `{"tool_name":"Read"}`); code != http.StatusUnauthorized {
		t.Fatalf("토큰 없이: %d", code)
	}
	if code, _ := askPolicy(t, s, "mcp", `{`); code != http.StatusBadRequest {
		t.Fatalf("깨진 본문: %d", code)
	}
	if code, v := askPolicy(t, s, "mcp", `{"tool_name":"Read","tool_input":{"file_path":"a"}}`); code != http.StatusOK || v.Deny {
		t.Fatalf("%d %+v", code, v)
	}
}

// A refusal is recorded by tool and rule, without what the call carried.
func TestARefusalLeavesARecord(t *testing.T) {
	s, mem := policyServer(t, Deps{})
	_, v := askPolicy(t, s, "mcp", `{"tool_name":"Bash","tool_input":{"command":"cat SECRET"}}`)
	if !v.Deny || v.Rule != policy.RuleUnregistered || v.Reason == "" {
		t.Fatalf("%+v", v)
	}
	r, ok := mem.Find(kindIs(KindPolicyDenied))
	if !ok || r.Tier != record.Durable || r.Subject != policy.RuleUnregistered || r.Actor != "chat" {
		t.Fatalf("기록: %+v", r)
	}
	raw, _ := json.Marshal(r.Payload)
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("인자가 남았습니다: %s", raw)
	}
}

// The password goes in only inside an approved payment of the one making the
// call: the conversation's approval is not a job's, and it runs out.
func TestThePasswordFollowsThePaymentsApproval(t *testing.T) {
	jobs := &fakeJobs{}
	runner := &switchRunner{}
	s, _ := policyServer(t, Deps{Runner: runner, Jobs: jobs})
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	s.payments.now = func() time.Time { return now }

	if _, v := askPolicy(t, s, "mcp", pinCall); !v.Deny || v.Rule != policy.RulePaymentPIN {
		t.Fatalf("승인 없이: %+v", v)
	}
	s.payments.grant("coupang.com", 0, "")
	if _, v := askPolicy(t, s, "mcp", pinCall); v.Deny {
		t.Fatalf("대화의 승인: %+v", v)
	}
	// A job's run holds the browser: the conversation's approval is not its.
	jobs.running(7)
	runner.mu.Lock()
	runner.bg = true
	runner.mu.Unlock()
	if _, v := askPolicy(t, s, "mcp", pinCall); !v.Deny {
		t.Fatalf("다른 결제자: %+v", v)
	}
	s.payments.grant("pg-window", 7, "113000")
	if _, v := askPolicy(t, s, "mcp", pinCall); v.Deny {
		t.Fatalf("작업의 승인: %+v", v)
	}
	if !s.payments.allow("pg-window", 7, "", "113000") {
		t.Fatal("결제자가 돌아왔습니다")
	}
	now = now.Add(paymentWindow + time.Minute)
	if _, v := askPolicy(t, s, "mcp", pinCall); !v.Deny {
		t.Fatalf("창이 지났습니다: %+v", v)
	}
}

// The gate asks the same rules: the hook is not the only door.
func TestTheGateAsksTheSameRules(t *testing.T) {
	s, _ := policyServer(t, Deps{})
	if why := s.Refuse("Bash", nil); why == "" {
		t.Fatal("게이트도 막아야 합니다")
	}
	if why := s.Refuse("Read", nil); why != "" {
		t.Fatal(why)
	}
}

func TestNoPolicyRefusesNothing(t *testing.T) {
	s, _ := recordingServer(t, Deps{Proposals: proposal.NewStore(bus.New()), Runner: bgRunner{}})
	if why := s.Refuse("Bash", nil); why != "" {
		t.Fatal(why)
	}
}
