package link

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

type sameLLM struct {
	answer  string
	err     error
	asked   int
	purpose string
	user    string
}

func (l *sameLLM) Complete(ctx context.Context, _, user string) (string, error) {
	l.asked++
	l.purpose, l.user = llm.PurposeOf(ctx), user
	return l.answer, l.err
}
func (l *sameLLM) Ready() bool    { return true }
func (l *sameLLM) Model() string  { return "test" }
func (l *sameLLM) Spent() float64 { return 0 }

func TestSameJudgeAsksOncePerPair(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := model.State{Title: "지수 생일 저녁", Date: "2026-11-17", Start: "19:00", People: []string{"지수"}}
	b := model.State{Title: "저녁 약속", Date: "2026-11-17", Start: "21:30"}
	l := &sameLLM{answer: `여기: {"same":true}`}
	j := &SameJudge{LLM: l, Log: quiet}
	if !j.Of(ctx, a, b) || !j.Of(ctx, b, a) {
		t.Fatal("판단을 따릅니다")
	}
	if l.asked != 1 || l.purpose != llm.Judge || !strings.Contains(l.user, "지수 생일 저녁") {
		t.Fatalf("한 쌍에 한 번만: %+v", l)
	}
	c := b
	c.Start = "22:00"
	if j.Of(ctx, a, c); l.asked != 2 {
		t.Fatalf("말이 달라지면 다시 묻습니다: %d", l.asked)
	}
}

func TestSameJudgeWithoutAnAnswerKeepsThemApart(t *testing.T) {
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := model.State{Title: "지수와 저녁", Date: "2026-11-17"}
	b := model.State{Title: "저녁 약속", Date: "2026-11-17"}
	for _, l := range []*sameLLM{{err: errors.New("끊김")}, {answer: "모르겠어요"}, {answer: `{"same":false}`}} {
		if (&SameJudge{LLM: l, Log: quiet}).Of(ctx, a, b) {
			t.Fatalf("%+v", l)
		}
	}
	if (&SameJudge{Log: quiet}).Of(ctx, a, b) {
		t.Fatal("모델이 없으면 둘입니다")
	}
	l := &sameLLM{err: errors.New("끊김")}
	j := &SameJudge{LLM: l, Log: quiet}
	j.Of(ctx, a, b)
	l.err, l.answer = nil, `{"same":true}`
	if !j.Of(ctx, a, b) {
		t.Fatal("실패는 기억하지 않습니다")
	}
}
