package mind

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/choigonyok/jarvis/gocommon/checks"
	"github.com/choigonyok/jarvis/mind-svc/internal/engine"
)

// Checks is how reading and remembering stand, for the status tab: only mind
// knows what it has read and what is still waiting.
func (m *Mind) Checks(ctx context.Context) ([]checks.Result, error) {
	st, err := m.Status(ctx)
	if err != nil {
		return nil, err
	}
	now := m.Opts.Now()
	t, at := m.trial.get()
	return []checks.Result{reading(st, now), remembering(st, now), judging(t, at)}, nil
}

// trialMin is how many answered judgements the trial needs before it says
// whether the correction helps.
const trialMin = 20

// judging is the judgement engine held to its own past (engine.Walk) as the
// night left it: whether the confidences corrected by the ledger came closer
// to the answers than the confidences as they were said - each corrected
// only by the answers before it.
func judging(t engine.Trial, at time.Time) checks.Result {
	v := checks.Result{ID: "mind.judgment", Group: "판단", Name: "판단 보정", Deps: []string{"svc.mind"},
		Rule:          fmt.Sprintf("카드에 답한 기록으로 고친 확신이 고치기 전 확신보다 답에 가까우면 정상(카드마다 그 전에 쌓인 답만으로 고쳐 봄). 고친 쪽이 더 멀면 주의. 답한 판단이 %d건이 안 되면 아직 가리지 않아요.", trialMin),
		Fix:           "고친 확신이 더 나쁘면 한 종류의 답이 한쪽으로 쏠렸을 수 있어요. mind 콘솔의 결과 장부에서 어떤 종류가 빗나가는지 보세요.",
		ObservedLabel: "마지막 시험"}
	if at.IsZero() {
		v.State, v.Summary = checks.Unknown, "아직 결과 장부를 읽지 않았어요."
		return v
	}
	v.ObservedAt = &at
	v.Figure = fmt.Sprintf("%d건", t.Cases)
	if t.Cases > 0 {
		v.Facts = append(v.Facts,
			fmt.Sprintf("오차(브라이어, 낮을수록 좋음): 말한 확신 %.3f → 고친 확신 %.3f · 늘 평균 승인율만 말했다면 %.3f", t.Stated, t.Corrected, t.Plain))
		var parts []string
		for _, g := range []struct {
			grade engine.Grade
			name  string
		}{{engine.Act, "바로 함"}, {engine.AskFilled, "채워서 물음"}, {engine.AskBlank, "빈칸으로 물음"}, {engine.Silent, "조용히 둠"}} {
			if n := t.Grades[g.grade]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s %d건 중 승인 %d", g.name, n, t.Right[g.grade]))
			}
		}
		if len(parts) > 0 {
			v.Facts = append(v.Facts, "고친 확신대로였다면: "+strings.Join(parts, " · "))
		}
	}
	switch {
	case t.Cases < trialMin:
		v.State, v.Summary = checks.OK, fmt.Sprintf("답한 판단이 %d건이라 아직 가리지 않아요.", t.Cases)
	case t.Corrected > t.Stated+0.005:
		v.State, v.Summary = checks.Warn, fmt.Sprintf("고친 확신이 고치기 전보다 답에서 멀어요 (오차 %.3f → %.3f).", t.Stated, t.Corrected)
	case t.Corrected < t.Stated-0.005:
		v.State, v.Summary = checks.OK, fmt.Sprintf("고친 확신이 고치기 전보다 오차가 %.0f%% 작아요.", (t.Stated-t.Corrected)/t.Stated*100)
	default:
		v.State, v.Summary = checks.OK, "고친 확신과 고치기 전 확신이 비슷해요."
	}
	return v
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
