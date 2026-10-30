package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HookCommand is what the CLI is told to run before every tool call.
const HookCommand = "policy-hook"

// hookWait bounds one question to the agent, on loopback.
const hookWait = 8 * time.Second

// hookOutput is how a PreToolUse hook refuses a call. Only a refusal is ever
// said: a hook that answers "allow" would skip the approval gate, and letting
// things by is not what this layer is for.
type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

func deny(w io.Writer, reason string) {
	var out hookOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = reason
	_ = json.NewEncoder(w).Encode(out)
}

// RunHook is the agent binary run as the CLI's hook: it takes the call on in,
// asks the agent that started this CLI at url, and says on out if it is
// refused. It holds no rule of its own - the rules need what the agent knows
// (which payments are approved), and they are in one place.
//
// It fails closed. An agent that cannot be asked is an agent that did not say
// yes; a tool call that waits for the next turn costs less than one that ran
// past every rule. The CLI runs under the agent, so this is rare.
func RunHook(in io.Reader, out io.Writer, url, token string, client *http.Client) int {
	body, err := io.ReadAll(in)
	if err != nil || len(body) == 0 {
		deny(out, "도구 호출을 읽지 못해 실행하지 않았습니다.")
		return 0
	}
	v, err := ask(url, token, body, client)
	if err != nil {
		deny(out, "실행 규칙을 확인하지 못해 실행하지 않았습니다. 잠시 뒤 다시 시도하세요.")
		return 0
	}
	if v.Deny {
		deny(out, v.Reason)
	}
	return 0
}

func ask(url, token string, body []byte, client *http.Client) (Verdict, error) {
	var v Verdict
	if url == "" {
		return v, fmt.Errorf("JARVIS_POLICY_URL 이 없습니다")
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return v, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return v, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return v, fmt.Errorf("응답 %d", resp.StatusCode)
	}
	return v, json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&v)
}
