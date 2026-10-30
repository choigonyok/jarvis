package mind

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
)

func TestKindOfSeesThroughACard(t *testing.T) {
	in, _ := json.Marshal(Input{IntentID: "i", Kind: KindCategory})
	if got := KindOf(action.Action{Kind: Approve, Input: in}); got != KindCategory {
		t.Fatalf("%s", got)
	}
	if got := KindOf(action.Action{Kind: "calendar.create"}); got != "calendar.create" {
		t.Fatalf("%s", got)
	}
}

func TestSlotAsksMind(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RequestURI()
		_ = json.NewEncoder(w).Encode(Slot{Holds: false, Why: "이사", P: 0.3, Grade: "ask-blank"})
	}))
	defer srv.Close()
	got, err := Client{URL: srv.URL}.Slot(context.Background(), "shipping.address", "2026-10-30T03:28:14+09:00")
	if err != nil || got.Holds || got.Why != "이사" {
		t.Fatalf("%+v %v", got, err)
	}
	if !strings.HasPrefix(query, "/api/slots/shipping.address?saved=2026-10-30T03%3A28%3A14%2B09%3A00") {
		t.Fatalf("%s", query)
	}
}

// The category card says what happens, in the book's terms, not a review's.
func TestACategoryCardSaysWhatItDoes(t *testing.T) {
	card := CardText(Card{Kind: KindCategory, Mode: "ask", Title: "이 결제는 어떤 분류인가요?", Fields: []action.Field{{Key: "category"}}})
	if !strings.Contains(card.Consequence, "기타로 두고") {
		t.Fatalf("%s", card.Consequence)
	}
	undo := CardText(Card{Kind: KindCategory, Mode: "undo", Title: "분류"})
	if !strings.Contains(undo.Consequence, "되돌립니다") {
		t.Fatalf("%s", undo.Consequence)
	}
}

// A form with only one thing changed keeps what was filled in for the rest.
func TestReviseKeepsWhatWasFilledIn(t *testing.T) {
	card := action.Card{Title: "분류", Fields: []action.Field{
		{Key: "category", Label: "분류", Required: true, Value: "식비"},
		{Key: "remember", Label: "이 가맹점은", Value: "앞으로도"},
	}}
	raw, _ := json.Marshal(Input{IntentID: "i", Kind: KindCategory, Card: &card})
	m := New(Client{})
	got, _, err := m.Revise(context.Background(), action.Action{Kind: Approve, Input: raw}, map[string]string{"remember": "이번만"})
	if err != nil {
		t.Fatal(err)
	}
	var in Input
	_ = json.Unmarshal(got.Input, &in)
	if in.Answers["category"] != "식비" || in.Answers["remember"] != "이번만" {
		t.Fatalf("%+v", in.Answers)
	}
	empty := action.Card{Fields: []action.Field{{Key: "category", Label: "분류", Required: true}}}
	raw, _ = json.Marshal(Input{IntentID: "i", Card: &empty})
	if _, _, err := m.Revise(context.Background(), action.Action{Kind: Approve, Input: raw}, nil); err == nil {
		t.Fatal("빈 칸은 답해야 합니다")
	}
}
