package mind

import (
	"context"
	"fmt"
	"time"

	"github.com/choigonyok/jarvis/gocommon/checks"
)

// Checks is how reading and remembering stand, for the status tab: only mind
// knows what it has read and what is still waiting.
func (m *Mind) Checks(ctx context.Context) ([]checks.Result, error) {
	st, err := m.Status(ctx)
	if err != nil {
		return nil, err
	}
	now := m.Opts.Now()
	return []checks.Result{reading(st, now), remembering(st, now)}, nil
}

func reading(st Status, now time.Time) checks.Result {
	v := checks.Result{ID: "mind.reading", Group: "수집", Name: "대화 해석", Deps: []string{"svc.mind", "kakao.messages"},
		Rule:       "mind 가 카톡·iMessage·jarvis 대화를 읽고 있으면 정상. 읽히지 않은 메시지가 30분 넘게 밀리면 주의, 2시간을 넘거나 LLM 키가 없으면 이상. 방이 3분 조용해지면(나와의 채팅은 45초) 읽는 게 보통이에요.",
		Fix:        "mind 콘솔(맥미니 127.0.0.1:8102)에서 밀린 방을 보세요. 해석이 실패하고 있다면 오늘 LLM 예산(MIND_DAILY_USD)이나 키를 확인하세요.",
		ObservedAt: st.LastRead, ObservedLabel: "마지막 해석",
		Facts: []string{fmt.Sprintf("대화방 %d개 · 아직 안 읽은 메시지 %d개", st.Rooms, st.Unread), fmt.Sprintf("오늘 해석 비용 $%.4f", st.LLM.Spent)}}
	if st.ReadError != "" {
		v.Facts = append(v.Facts, "최근 해석 실패: "+clip(st.ReadError, 140))
	}
	var behind time.Duration
	if st.OldestUnread != nil {
		behind = now.Sub(*st.OldestUnread)
	}
	switch {
	case !st.LLM.Ready:
		v.State, v.Summary = checks.Fail, "LLM 키가 없어 대화를 쌓기만 하고 읽지 못해요."
	case behind > 2*time.Hour:
		v.State, v.Summary = checks.Fail, spanned(behind)+"째 읽히지 않은 대화가 있어요."
	case behind > 30*time.Minute:
		v.State, v.Summary = checks.Warn, spanned(behind)+"째 읽히지 않은 대화가 있어요."
	default:
		v.State, v.Summary = checks.OK, "대화를 읽고 있어요."
	}
	return v
}

func remembering(st Status, now time.Time) checks.Result {
	f := st.Facts
	v := checks.Result{ID: "mind.memory", Group: "수집", Name: "기억으로 보내기", Deps: []string{"svc.memory", "mind.reading"},
		Rule:       "해석에서 나온 기억할 사실이 기억 서비스에 들어가고 있으면 정상. 1시간 넘게 기다리는 사실이 있으면 주의, 6시간을 넘으면 이상.",
		Fix:        "기억 서비스가 응답하는지, 오늘 LLM 예산(MEMORY_DAILY_USD)이 남았는지 보세요. 기다리던 사실은 다시 보내져요.",
		ObservedAt: st.OldestPending, ObservedLabel: "가장 오래 기다린 사실",
		Facts: []string{fmt.Sprintf("기억함 %d · 이미 알던 것 %d · 기다림 %d", f["stored"], f["duplicate"], f["pending"])}}
	if f["held"] > 0 {
		// Its own row says so: 격리함 · 기억.
		v.Facts = append(v.Facts, fmt.Sprintf("보류한 사실 %d개 (격리함)", f["held"]))
	}
	if st.FactError != "" {
		v.Facts = append(v.Facts, "기다리는 이유: "+clip(st.FactError, 140))
	}
	var waiting time.Duration
	if st.OldestPending != nil {
		waiting = now.Sub(*st.OldestPending)
	}
	switch {
	case !st.Memory:
		v.State, v.Summary = checks.Fail, "장기 기억이 연결되어 있지 않아 사실이 장부에만 쌓여요."
	case waiting > 6*time.Hour:
		v.State, v.Summary = checks.Fail, spanned(waiting)+"째 기억에 넣지 못한 사실이 있어요."
	case waiting > time.Hour:
		v.State, v.Summary = checks.Warn, spanned(waiting)+"째 기억에 넣지 못한 사실이 있어요."
	default:
		v.State, v.Summary, v.ObservedAt = checks.OK, "기억할 사실이 기억 서비스에 들어가고 있어요.", nil
	}
	return v
}

// spanned is a duration as a person says it: "3일", "14시간".
func spanned(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d일", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%d시간", int(d.Hours()))
	default:
		return fmt.Sprintf("%d분", int(d.Minutes()))
	}
}
