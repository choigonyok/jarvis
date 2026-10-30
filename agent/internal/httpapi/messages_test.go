package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
	"github.com/choigonyok/jarvis/agent/internal/core/proposal"
	"github.com/choigonyok/jarvis/agent/internal/thread"
	"github.com/choigonyok/jarvis/gocommon/queue"
)

// heard is a runner that remembers what it was told.
type heard struct {
	bgRunner
	said []string
}

func (h *heard) Send(text string, _ []string) error {
	h.said = append(h.said, text)
	return nil
}

// What is said waits at the way in; the agent takes it from the queue, reads its
// words there, and starts the turn. What it cannot do is said on the screen, not
// tried again, and words it could not read yet are.
func TestWhatIsSaidComesFromTheQueue(t *testing.T) {
	var down bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch strings.TrimPrefix(r.URL.Path, "/chat/messages/") {
		case "msg-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "내일 일정 알려줘"})
		case "msg-photo":
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "이거 봐", "images": []string{"a.jpg"}})
		case "msg-quote":
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "이거", "ref": map[string]string{"id": "u-9-9"}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"turns": []thread.Turn{}, "hasMore": false})
		}
	}))
	defer srv.Close()
	events := bus.New()
	frames, stop := events.Subscribe()
	defer stop()
	run := &heard{}
	s := New(Deps{Thread: thread.NewStore(srv.URL, "", events), Proposals: proposal.NewStore(events), Runner: run, Bus: events, APIToken: "svc"})
	ctx := context.Background()

	if err := s.TakeMessage(ctx, queue.Message{SourceKey: "msg-1"}); err != nil {
		t.Fatal(err)
	}
	if len(run.said) != 1 || run.said[0] != "내일 일정 알려줘" {
		t.Fatalf("%v", run.said)
	}
	told := func(want string) {
		t.Helper()
		select {
		case f := <-frames:
			if f.Type != "error" || !strings.Contains(f.Message, want) {
				t.Fatalf("%+v", f)
			}
		case <-time.After(time.Second):
			t.Fatal("화면에 말하지 않았습니다")
		}
	}
	if err := s.TakeMessage(ctx, queue.Message{SourceKey: "msg-photo"}); err != nil {
		t.Fatal(err)
	}
	told("사진")
	if err := s.TakeMessage(ctx, queue.Message{SourceKey: "msg-quote"}); err != nil {
		t.Fatal(err)
	}
	told("참조한 메시지")
	if len(run.said) != 1 {
		t.Fatalf("시작하면 안 됩니다: %v", run.said)
	}
	down = true
	if err := s.TakeMessage(ctx, queue.Message{SourceKey: "msg-1"}); err == nil || errors.Is(err, queue.ErrRefused) {
		t.Fatalf("입구가 없으면 다시 해 봅니다: %v", err)
	}
}
