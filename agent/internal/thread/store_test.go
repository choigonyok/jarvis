package thread

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

// fakeChat stands in for chat-svc, so these tests say what they used to say
// without needing Postgres: the bus carries what was appended, and a card
// raised mid-conversation leaves a mark in the transcript.
type fakeChat struct {
	mu    sync.Mutex
	turns []Turn
	seq   int
}

func (f *fakeChat) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /turns", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSON(w, map[string]any{"turns": f.turns})
	})

	mux.HandleFunc("POST /turns", func(w http.ResponseWriter, r *http.Request) {
		var in Turn
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		// 서비스가 id 와 시각을 발급한다는 점이 파일 저장소와의 차이다.
		f.seq++
		prefix := "a"
		if in.Role == RoleUser {
			prefix = "u"
		}
		in.ID = fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixMilli(), f.seq)
		in.At = time.Now().Format("15:04")
		f.turns = append(f.turns, in)
		writeJSON(w, in)
	})

	return mux
}

func newFakeChat(t *testing.T) *Store {
	t.Helper()
	server := httptest.NewServer((&fakeChat{}).handler())
	t.Cleanup(server.Close)

	store := NewStore(server.URL, "", bus.New())
	if err := store.Connect(nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return store
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestAppendPublishesTurn(t *testing.T) {
	server := httptest.NewServer((&fakeChat{}).handler())
	defer server.Close()

	b := bus.New()
	events, cancel := b.Subscribe()
	defer cancel()

	NewStore(server.URL, "", b).AppendUser("안녕")

	select {
	case ev := <-events:
		turn, ok := ev.Turn.(Turn)
		if ev.Type != "turn" || !ok || turn.Text != "안녕" {
			t.Fatalf("예상치 못한 이벤트: %+v", ev)
		}
		if turn.ID == "" {
			t.Fatal("서비스가 발급한 id 가 프레임에 없습니다")
		}
	case <-time.After(time.Second):
		t.Fatal("이벤트가 오지 않았습니다")
	}
}

// A card raised mid-conversation must leave a mark in the transcript, so the
// thread still reads as a sequence of what happened.
func TestAttachProposalLandsInTranscript(t *testing.T) {
	s := newFakeChat(t)
	s.AppendUser("캐시 지워줘")
	s.AttachProposal("p-1")

	turns, _ := s.Snapshot()
	if len(turns) != 2 || turns[1].ProposalID != "p-1" {
		t.Fatalf("제안이 스레드에 남지 않았습니다: %+v", turns)
	}
}

// 저장이 실패해도 말한 것은 화면에 보여야 한다. 턴은 이미 일어났고, 쓸 수
// 없었다는 이유로 보여주지 않으면 두 번 잃는다.
func TestAppendStillPublishesWhenTheServiceIsDown(t *testing.T) {
	b := bus.New()
	events, cancel := b.Subscribe()
	defer cancel()

	// 닿지 않는 주소.
	NewStore("http://127.0.0.1:1", "", b).AppendUser("안녕")

	select {
	case ev := <-events:
		turn, ok := ev.Turn.(Turn)
		if ev.Type != "turn" || !ok || turn.Text != "안녕" {
			t.Fatalf("예상치 못한 이벤트: %+v", ev)
		}
		// id 가 없는 프레임은 클라이언트의 upsert 를 깨뜨린다.
		if turn.ID == "" {
			t.Fatal("id 없는 턴이 발행됐습니다")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("저장 실패 시 이벤트가 오지 않았습니다")
	}
}

// 진행 여부는 저장되지 않는다. 방금 시작한 프로세스는 턴 중간에 있지 않다.
func TestThinkingIsLocalAndNotPersisted(t *testing.T) {
	s := newFakeChat(t)
	s.SetThinking(true)
	if _, thinking := s.Snapshot(); !thinking {
		t.Fatal("진행 여부가 반영되지 않았습니다")
	}

	fresh := NewStore(s.base, "", bus.New())
	if _, thinking := fresh.Snapshot(); thinking {
		t.Fatal("새로 뜬 스토어가 진행 중이라고 답했습니다")
	}
}
