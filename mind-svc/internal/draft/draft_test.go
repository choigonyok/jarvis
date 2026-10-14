package draft

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/act"
	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
	"github.com/choigonyok/jarvis/mind-svc/internal/store"
)

// The model is shown the operator's own messages to the person, and the
// operator's first replies after long silences elsewhere - never a rule.
func TestExamples(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	at := time.Date(2026, 7, 20, 18, 0, 0, 0, time.UTC)
	put := func(ref, room, name string, mine bool, text string, when time.Time) {
		m := model.Message{Ref: ref, Source: "sim", Room: room, RoomName: name, IsMine: mine, SentAt: when, Text: text}
		if err := st.PutMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Index(ctx, st, m); err != nil {
			t.Fatal(err)
		}
	}
	put("m1", "sim:mom", "엄마", false, "밥은 먹었니", at)
	put("m2", "sim:mom", "엄마", true, "네 엄마 먹었어요^^", at.Add(time.Minute))
	put("j1", "sim:junho", "준호", true, "ㅇㅋ", at.AddDate(0, -5, 0))
	put("j2", "sim:junho", "준호", false, "야 오랜만", at)
	put("j3", "sim:junho", "준호", true, "헐 대박 오랜만이다!! 😆", at.Add(time.Minute))

	var prompt string
	d := &Drafter{St: st, LLM: &llm.Script{Answer: func(_, user string) (string, error) { prompt = user; return "“네 엄마^^”", nil }}}
	out, err := d.Draft(ctx, act.DraftRequest{To: "엄마", Purpose: "안부", Situation: "reconnect", IncomingRef: "m1"})
	if err != nil || out != "네 엄마^^" {
		t.Fatalf("초안: %q %v", out, err)
	}
	for _, want := range []string{"네 엄마 먹었어요^^", "헐 대박 오랜만이다!! 😆", "상대가 보낸 메시지: 밥은 먹었니"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("예시에 %q 가 없습니다:\n%s", want, prompt)
		}
	}
}
