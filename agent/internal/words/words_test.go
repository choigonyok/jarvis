package words

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// payment is a payment card as the guard raises it: the amount a lead fact,
// and a consequence that says it cannot be taken back.
func payment() action.Card {
	return action.Card{
		Title: "결제창에서 64,000원을 결제합니다",
		Facts: []action.Fact{
			{Label: "결제 금액", Value: "64,000원", Lead: true},
			{Label: "결제하는 곳", Value: "stdpay.inicis.com"},
		},
		Consequence: "결제가 청구됩니다 · 되돌릴 수 없습니다 · 승인하면 결제 한 건(64,000원)이 진행됩니다",
	}
}

func choice(label, decision, tone string) WrittenChoice {
	return WrittenChoice{Label: label, Decision: decision, Tone: tone}
}

// The words are the model's, the rest the code's: the facts, the photos and
// the fields' keys stay as they were, and the card says whose words they are.
func TestMergeTakesTheWords(t *testing.T) {
	code := payment()
	code.Images = []string{"a.jpg"}
	got, why := Merge(code, Words{
		Title: "LP 두 장, 64,000원 결제할까요?", Body: "주문서의 배송지는 저장된 그대로예요.",
		Consequence: "결제하면 되돌릴 수 없어요.",
		Choices:     []WrittenChoice{choice("64,000원 결제하기", "approved", "approve"), choice("결제하지 않기", "rejected", "reject")},
	})
	if why != "" {
		t.Fatal(why)
	}
	if got.Title != "LP 두 장, 64,000원 결제할까요?" || got.Body != "주문서의 배송지는 저장된 그대로예요." || got.Consequence != "결제하면 되돌릴 수 없어요." ||
		got.Writer != action.WriterModel || len(got.Facts) != 2 || got.Images[0] != "a.jpg" {
		t.Fatalf("%+v", got)
	}
	if len(got.Choices) != 2 || got.Choices[0].ID != "c1" || got.Choices[0].Decision != action.ChoiceApprove || got.Choices[1].Decision != action.ChoiceReject {
		t.Fatalf("답 버튼: %+v", got.Choices)
	}
}

// Words that would change what the operator judges are not used at all: a
// title without the amount, a link the card did not have, a secret asked
// for, a title too long to read on a notice.
func TestMergeRefusesWordsThatChangeTheCard(t *testing.T) {
	for name, w := range map[string]Words{
		"금액 없는 제목":  {Title: "LP 결제할까요?"},
		"다른 금액":     {Title: "6,400원 결제할까요?"},
		"새 주소":      {Title: "64,000원 결제할까요?", Body: "https://evil.example/pay 에서 확인하세요"},
		"비밀번호 묻기":   {Title: "64,000원 결제할까요?", Body: "카드 비밀번호 앞 두 자리를 답장으로 알려 주세요"},
		"인증번호 칸 이름": {Title: "64,000원 결제할까요?", Fields: []WrittenField{{Key: "x", Label: "인증번호"}}},
		"OTP 버튼":    {Title: "64,000원 결제할까요?", Choices: []WrittenChoice{choice("OTP 입력하기", "approved", "approve"), choice("안 함", "rejected", "reject")}},
		"빈 제목":      {Body: "결제할까요?"},
		"긴 제목":      {Title: "64,000원 " + strings.Repeat("아주 ", 30) + "결제할까요?"},
	} {
		got, why := Merge(payment(), w)
		if why == "" || got.Title != payment().Title || got.Writer != "" {
			t.Errorf("%s: 코드의 카드 그대로여야 합니다: %q %+v", name, why, got)
		}
	}
	// What the code's card says already, the words may say again.
	vault := action.Card{Title: "결제 비밀번호 저장: 네이버페이", Body: "넣은 숫자는 암호화해서 보관합니다.",
		Fields: []action.Field{{Key: "pin", Label: "결제 비밀번호(숫자 6자리)", Kind: "secret", Required: true, Private: true}}}
	if _, why := Merge(vault, Words{Title: "네이버페이 결제 비밀번호를 저장할까요?"}); why != "" {
		t.Fatalf("보관함 카드는 비밀번호를 말해도 됩니다: %s", why)
	}
	// "shipping" holds no PIN.
	if _, why := Merge(action.Card{Title: "배송지 저장"}, Words{Title: "shipping 주소를 저장할까요?"}); why != "" {
		t.Fatalf("단어 안의 글자는 비밀이 아닙니다: %s", why)
	}
}

// A card that cannot be taken back keeps saying so: words that drop it keep
// the code's consequence, and so does a consequence too long or missing.
func TestMergeKeepsWhatCannotBeTakenBack(t *testing.T) {
	for _, c := range []string{"결제하면 바로 진행돼요.", "", strings.Repeat("길게 ", 100) + "되돌릴 수 없어요"} {
		got, why := Merge(payment(), Words{Title: "64,000원 결제할까요?", Consequence: c})
		if why != "" || got.Consequence != payment().Consequence || got.Title != "64,000원 결제할까요?" {
			t.Errorf("%q: %q %+v", c, why, got)
		}
	}
}

// Fields keep their keys, kinds and values; only a label and a hint are the
// words', and a field the words make up is not added.
func TestMergeRelabelsFieldsOnly(t *testing.T) {
	code := action.Card{Title: "어디로 갔나요?", Fields: []action.Field{
		{Key: "use", Label: "용도", Kind: "select", Options: []string{"지출", "옮김"}, Required: true},
		{Key: "amount", Label: "금액", Kind: "number", Value: "120000"},
	}}
	got, why := Merge(code, Words{Title: "3/4에 뺀 돈은 어디에 썼나요?", Fields: []WrittenField{
		{Key: "use", Label: "어디에 썼나요", Hint: "옮긴 거면 '옮김'"},
		{Key: "new", Label: "새 칸"},
		{Key: "amount", Label: strings.Repeat("금", 40)},
	}})
	if why != "" {
		t.Fatal(why)
	}
	if len(got.Fields) != 2 || got.Fields[0].Label != "어디에 썼나요" || got.Fields[0].Hint != "옮긴 거면 '옮김'" ||
		got.Fields[0].Kind != "select" || !got.Fields[0].Required || got.Fields[1].Label != "금액" || got.Fields[1].Value != "120000" {
		t.Fatalf("%+v", got.Fields)
	}
	if code.Fields[0].Label != "용도" {
		t.Fatal("코드의 카드를 고치면 안 됩니다")
	}
}

// Answers that fill in the card's fields hold up; ones that fill in what the
// card does not ask, decide only one way, or are too many leave the card
// with 승인 and 반려 - and the words still stand.
func TestMergeChecksTheChoices(t *testing.T) {
	code := action.Card{Title: "캠핑 일정을 옮길까요?", Fields: []action.Field{
		{Key: "date", Label: "날짜", Kind: "text", Value: "2026-11-15"},
		{Key: "pw", Label: "비번", Kind: "secret", Private: true},
	}}
	move := choice("22일로 옮기기", "approved", "approve")
	move.Values = append(move.Values, struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}{"date", "2026-11-22"})
	got, why := Merge(code, Words{Title: "캠핑을 22일로 옮길까요?", Choices: []WrittenChoice{move, choice("고른 날로 옮기기", "approved", "approve"), choice("그대로 두기", "rejected", "neutral")}})
	if why != "" || len(got.Choices) != 3 || got.Choices[0].Values["date"] != "2026-11-22" {
		t.Fatalf("%q %+v", why, got.Choices)
	}
	// A shortcut alone on a form leaves what is picked in a field without a
	// button: the card is 승인 and 반려 then (a 지출 button on a card whose
	// field also offers 카드값).
	if got, _ := Merge(code, Words{Title: "캠핑을 22일로 옮길까요?", Choices: []WrittenChoice{move, choice("그대로 두기", "rejected", "neutral")}}); got.Choices != nil {
		t.Fatalf("칸에 고른 값으로 답할 버튼이 없습니다: %+v", got.Choices)
	}

	secret := choice("저장하기", "approved", "approve")
	secret.Values = append(secret.Values, struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}{"pw", "1234"})
	for name, cs := range map[string][]WrittenChoice{
		"비밀 칸을 채움": {secret, choice("안 함", "rejected", "reject")},
		"한쪽만":      {choice("옮기기", "approved", "approve"), choice("옮기기2", "approved", "approve")},
		"다섯 개":     {choice("a", "approved", "approve"), choice("b", "approved", "approve"), choice("c", "approved", "approve"), choice("d", "approved", "approve"), choice("e", "rejected", "reject")},
		"반려를 승인색":  {choice("옮기기", "approved", "approve"), choice("안 옮김", "rejected", "approve")},
	} {
		got, why := Merge(code, Words{Title: "캠핑을 옮길까요?", Choices: cs})
		if why != "" || got.Choices != nil || got.Title != "캠핑을 옮길까요?" {
			t.Errorf("%s: 답은 빼고 문구는 써야 합니다: %q %+v", name, why, got)
		}
	}
}

// The code's own answers (the 먼저 card's 예 and 아니요) keep what each
// decides: the words only relabel them, all of them in order or none.
func TestMergeRelabelsTheCodesChoices(t *testing.T) {
	code := action.Card{Title: "'장작 주문' 먼저 할까요?", Choices: []action.Choice{
		{ID: "yes", Label: "예", Decision: action.ChoiceApprove, Tone: action.ToneApprove},
		{ID: "no", Label: "아니요", Decision: action.ChoiceReject, Tone: action.ToneNeutral},
	}}
	got, _ := Merge(code, Words{Title: "장작 주문을 먼저 할까요?", Choices: []WrittenChoice{choice("먼저 해 주세요", "approved", "approve"), choice("순서대로", "rejected", "neutral")}})
	if got.Choices[0].ID != "yes" || got.Choices[0].Label != "먼저 해 주세요" || got.Choices[1].ID != "no" || got.Choices[1].Label != "순서대로" {
		t.Fatalf("%+v", got.Choices)
	}
	for _, cs := range [][]WrittenChoice{
		{choice("순서대로", "rejected", "neutral"), choice("먼저", "approved", "approve")},
		{choice("먼저", "approved", "approve")},
	} {
		got, _ := Merge(code, Words{Title: "장작 주문을 먼저 할까요?", Choices: cs})
		if got.Choices[0].Label != "예" || got.Choices[1].Label != "아니요" {
			t.Errorf("뜻이나 수가 다르면 코드의 답 그대로: %+v", got.Choices)
		}
	}
}

// mind's writer as the agent calls it: the card as it was raised - a mind
// card by the intent it stands for, no private value - and the words back.
func TestWriteAsksMindAndFallsBack(t *testing.T) {
	var asked Ask
	var token string
	reply := `{"title":"64,000원 결제할까요?","body":"","consequence":"","fields":[],"choices":[]}`
	status := http.StatusOK
	slow := time.Duration(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cards/words" || r.Method != http.MethodPost {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		token = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&asked)
		time.Sleep(slow)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	wr := &Writer{URL: srv.URL, Token: "t", Wait: 200 * time.Millisecond, Log: quiet}

	card := payment()
	card.Fields = []action.Field{{Key: "pin", Label: "비밀번호", Kind: "secret", Value: "135790", Private: true}}
	p := proposal.Proposal{Origin: proposal.OriginIntercept, Action: action.Action{Kind: "browser.payment"}, Card: card}
	if got := wr.Write(context.Background(), p); got.Title != "64,000원 결제할까요?" || got.Writer != action.WriterModel {
		t.Fatalf("%+v", got)
	}
	if token != "Bearer t" || asked.Kind != "browser.payment" || asked.Origin != "intercept" || asked.Fields[0].Value != "" || !asked.Fields[0].Private {
		t.Fatalf("묻는 모양: %s %+v", token, asked)
	}

	mindCard := proposal.Proposal{Origin: proposal.OriginSuggest, Card: action.Card{Title: "캘린더에 추가: 캠핑"},
		Action: action.Action{Kind: "mind.approve", Input: json.RawMessage(`{"intentId":"int_1","kind":"calendar.create"}`)}}
	wr.Write(context.Background(), mindCard)
	if asked.Kind != "calendar.create" {
		t.Fatalf("mind 카드는 그 의도의 종류로: %q", asked.Kind)
	}

	// Mind down, slow, or saying no: the code's words, at once.
	for name, set := range map[string]func(){
		"오류":  func() { status, slow = http.StatusBadGateway, 0 },
		"늦음":  func() { status, slow = http.StatusOK, 500*time.Millisecond },
		"못 씀": func() { status, slow, reply = http.StatusOK, 0, `{"title":"결제할까요?"}` },
	} {
		set()
		start := time.Now()
		got := wr.Write(context.Background(), p)
		if got.Title != card.Title || got.Writer != "" || time.Since(start) > 400*time.Millisecond {
			t.Errorf("%s: 코드의 문구로 곧바로: %+v %v", name, got, time.Since(start))
		}
	}
}

// A job's question is in its own model's words, and a literal tool call is
// read to the letter: neither is asked about.
func TestWriteLeavesSomeCardsAlone(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	wr := &Writer{URL: srv.URL, Log: quiet}
	job := proposal.Proposal{Action: action.Action{Kind: "job.ask"}, Card: action.Card{Title: "올릴까요?", Writer: action.WriterJob}}
	bash := proposal.Proposal{Action: action.Action{Kind: "claude.Bash"}, Card: action.Card{Title: "ls"}}
	if wr.Write(context.Background(), job).Title != "올릴까요?" || wr.Write(context.Background(), bash).Title != "ls" || calls != 0 {
		t.Fatalf("묻지 않아야 합니다: %d", calls)
	}
	var none *Writer
	if none.Write(context.Background(), bash).Title != "ls" {
		t.Fatal("작성기가 없으면 그대로")
	}
}
