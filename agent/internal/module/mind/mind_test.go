package mind

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
	"github.com/choigonyok/jarvis/agent/internal/core/module"
)

var _ module.Editor = (*Module)(nil)

// A review card is a form: a required answer missing stops the approval;
// the answers are drawn back on the card and sent with the approval.
func TestFormCard(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = r.URL.Path + " " + string(b)
		_, _ = w.Write([]byte(`{"status":"executed","preview":"회고를 기록합니다"}`))
	}))
	defer srv.Close()
	m := New(Client{URL: srv.URL})
	card := CardText(Card{IntentID: "int_r", Mode: "ask", Kind: "review.place", Title: "솔숲캠핑장 어땠어요?",
		Fields: []action.Field{{Key: "score", Label: "점수", Kind: "select", Options: []string{"1", "2", "3", "4", "5"}, Required: true},
			{Key: "bad", Label: "아쉬웠던 점", Kind: "multi", Options: []string{"소음·매너", "청결"}}}})
	input, _ := json.Marshal(Input{IntentID: "int_r", Card: &card})
	a := action.Action{Kind: Approve, Input: input}
	if _, _, err := m.Revise(context.Background(), a, map[string]string{"bad": "청결"}); err == nil {
		t.Fatal("점수 없이 승인됐습니다")
	}
	revised, drawn, err := m.Revise(context.Background(), a, map[string]string{"score": "2", "bad": "소음·매너,청결"})
	if err != nil || drawn.Title != "솔숲캠핑장 어땠어요?" || drawn.Fields[0].Value != "2" {
		t.Fatalf("다시 그린 카드: %+v %v", drawn, err)
	}
	if _, err := m.Execute(context.Background(), revised); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "/api/intents/int_r/approve ") || !strings.Contains(got, `"score":"2"`) || !strings.Contains(got, "소음·매너,청결") {
		t.Fatalf("mind 에 보낸 답: %s", got)
	}
}
