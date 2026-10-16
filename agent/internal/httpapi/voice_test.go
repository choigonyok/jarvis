package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/thread"
)

// speaker plays one turn on the bus, as the runner would: thinking, what
// was said, done.
type speaker struct {
	b    *bus.Bus
	said []string
	card string
}

func (s speaker) Send(string, []string) error {
	go func() {
		s.b.Publish(bus.Event{Type: "status", Thinking: true})
		s.b.Publish(bus.Event{Type: "turn", Turn: thread.Turn{Role: thread.RoleUser, Text: "질문"}})
		s.b.Publish(bus.Event{Type: "turn", Turn: thread.Turn{Role: thread.RoleAgent, Paragraphs: s.said, ProposalID: s.card}})
		s.b.Publish(bus.Event{Type: "status", Thinking: false})
	}()
	return nil
}

func TestVoice(t *testing.T) {
	b := bus.New()
	srv := httptest.NewServer(New(Deps{Bus: b, Runner: speaker{b: b, said: []string{"**내일** 일정은 두 개예요.", "- 10시 [치과](http://x)\n- 19시 민수와 저녁"}, card: "p1"},
		APIToken: "outer", VoiceToken: "voice"}).Handler())
	defer srv.Close()
	ask := func(token, body string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", srv.URL+"/voice", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, _ := ask("nope", `{"text":"내일 일정"}`); code != 401 {
		t.Fatalf("틀린 토큰: %d", code)
	}
	if code, out := ask("voice", `{"text":"  "}`); code != 400 || out["error"] == nil {
		t.Fatalf("빈 말: %d %v", code, out)
	}
	code, out := ask("voice", `{"text":"내일 일정 알려줘"}`)
	want := "내일 일정은 두 개예요. 10시 치과 19시 민수와 저녁 확인할 카드가 있어요. jarvis 앱에서 봐 주세요."
	if code != 200 || out["answer"] != want || out["done"] != true {
		t.Fatalf("답: %d %v", code, out)
	}
}
