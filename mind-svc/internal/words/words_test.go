package words

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/llm"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

// The model is asked once, thinking little and held to the answer's JSON, with
// the card as the code drafted it, who raised it and the time - and a private
// field's value never reaches it.
func TestWriteAsksTheModelWithTheCard(t *testing.T) {
	var asked string
	script := &llm.Script{Answer: func(system, user string) (string, error) {
		asked = user
		if !strings.Contains(system, "lead 가 true 인 값은 제목에 글자 그대로") {
			t.Errorf("지시문에 제목 규칙이 없습니다")
		}
		return `{"title":"64,000원 결제할까요?","body":"LP 두 장이에요.","consequence":"결제하면 되돌릴 수 없어요.","fields":[],` +
			`"choices":[{"label":"64,000원 결제하기","decision":"approved","tone":"approve","why":false,"values":[]},` +
			`{"label":"결제하지 않기","decision":"rejected","tone":"reject","why":true,"values":[]},` +
			`{"label":"모르는 답","decision":"later","tone":"neutral","why":false,"values":[]}]}`, nil
	}}
	ask := Ask{Kind: "browser.payment", Origin: "intercept", Title: "결제창에서 64,000원을 결제합니다",
		Consequence: "결제가 청구됩니다 · 되돌릴 수 없습니다",
		Facts:       []Fact{{Label: "결제 금액", Value: "64,000원", Lead: true}},
		Fields:      []Field{{Key: "pin", Label: "비밀번호", Kind: "secret", Value: "135790", Private: true}}}
	got, err := Write(context.Background(), script, ask, time.Date(2026, 11, 13, 20, 5, 0, 0, seoul))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "64,000원 결제할까요?" || len(got.Choices) != 2 || got.Choices[1].Why != true {
		t.Fatalf("어느 쪽으로도 정하지 않는 답만 빼고 읽어야 합니다: %+v", got)
	}
	if strings.Contains(asked, "135790") {
		t.Fatalf("비밀 칸의 값은 모델에 가지 않습니다: %s", asked)
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(asked), &in); err != nil {
		t.Fatal(err)
	}
	if in["now"] != "2026-11-13 20:05 (금)" || !strings.HasPrefix(in["origin"].(string), "intercept - ") || in["kind"] != "browser.payment" {
		t.Fatalf("카드와 함께 누가 올렸는지와 지금을 말해야 합니다: %v", in)
	}
	shape := script.Shapes[0]
	if shape.Effort != "low" || !json.Valid(shape.Schema) {
		t.Fatalf("적게 생각하고 정해진 JSON 으로 답하게 물어야 합니다: %+v", shape)
	}
}

// What cannot be read is an error, so the card goes up in the code's words.
func TestWriteSaysWhenItCouldNotWrite(t *testing.T) {
	for _, answer := range []string{"", "죄송해요", `{"title":"","body":"x"}`} {
		script := &llm.Script{Answer: func(_, _ string) (string, error) { return answer, nil }}
		if _, err := Write(context.Background(), script, Ask{Title: "일정을 추가합니다"}, time.Now()); err == nil {
			t.Errorf("%q: 오류여야 합니다", answer)
		}
	}
	failing := &llm.Script{Answer: func(_, _ string) (string, error) { return "", llm.ErrBudget }}
	if _, err := Write(context.Background(), failing, Ask{Title: "일정을 추가합니다"}, time.Now()); !errors.Is(err, llm.ErrBudget) {
		t.Fatalf("모델의 오류를 그대로: %v", err)
	}
	if _, err := Write(context.Background(), failing, Ask{}, time.Now()); !errors.Is(err, ErrEmpty) {
		t.Fatalf("빈 카드는 묻지 않습니다: %v", err)
	}
}

// An answer wrapped in a code fence or a line before it is still read.
func TestParseFindsTheObject(t *testing.T) {
	w, err := Parse("```json\n{\"title\":\" 일정 추가할까요? \",\"body\":\"\",\"consequence\":\"\",\"fields\":[{\"key\":\"date\",\"label\":\" 날짜 \",\"hint\":\"\"}],\"choices\":[]}\n```")
	if err != nil || w.Title != "일정 추가할까요?" || w.Fields[0].Label != "날짜" {
		t.Fatalf("%+v %v", w, err)
	}
}
