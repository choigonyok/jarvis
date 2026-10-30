package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/choigonyok/jarvis/agent/internal/policy"
	"github.com/choigonyok/jarvis/gocommon/record"
)

// checkPolicy judges one tool call with what only this process knows: who is
// driving (the run holding the browser, as /intercept reads it, never what the
// request says) and whether that payer has an approved payment. A refusal
// leaves a record: which tool, which rule, and not its arguments.
func (s *Server) checkPolicy(tool string, input json.RawMessage) policy.Verdict {
	if s.policy == nil {
		return policy.Verdict{}
	}
	var job int64
	if bg, ok := s.runner.(interface{ InBackground() bool }); ok && bg.InBackground() && s.jobs != nil {
		job, _, _, _ = s.jobs.RunningJob()
	}
	v := s.policy.Check(policy.Call{Tool: tool, Input: input, PaymentApproved: s.payments.live(job)})
	if !v.Deny {
		return v
	}
	s.log.Warn("정책이 도구 호출을 막았습니다", "tool", tool, "rule", v.Rule, "payer", payerOf(job))
	s.records.Write(record.Record{
		Kind: KindPolicyDenied, Source: recordSource, SourceKey: recordKey("p"),
		Actor: payerOf(job), Channel: "tool", Subject: v.Rule,
		Tier: record.Durable, TraceID: payerOf(job),
		Payload: map[string]any{"tool": tool, "rule": v.Rule},
	})
	return v
}

// Refuse is the same judgement for the approval gate: a call the hook could
// not stop (a hook that timed out lets the call by) still stops there.
func (s *Server) Refuse(tool string, input json.RawMessage) string {
	return s.checkPolicy(tool, input).Reason
}

// postPolicyCheck answers the CLI's hook. The body is the hook's own input.
func (s *Server) postPolicyCheck(w http.ResponseWriter, r *http.Request) {
	var call struct {
		Tool  string          `json:"tool_name"`
		Input json.RawMessage `json:"tool_input"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&call); err != nil || call.Tool == "" {
		writeErr(w, http.StatusBadRequest, "도구 호출을 읽을 수 없습니다.")
		return
	}
	writeJSON(w, http.StatusOK, s.checkPolicy(call.Tool, call.Input))
}
