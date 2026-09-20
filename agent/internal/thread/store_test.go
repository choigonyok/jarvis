package thread

import (
	"testing"
	"time"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

func TestAppendPublishesTurn(t *testing.T) {
	b := bus.New()
	events, cancel := b.Subscribe()
	defer cancel()

	NewStore(b).AppendUser("안녕")

	select {
	case ev := <-events:
		turn, ok := ev.Turn.(Turn)
		if ev.Type != "turn" || !ok || turn.Text != "안녕" {
			t.Fatalf("예상치 못한 이벤트: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("이벤트가 오지 않았습니다")
	}
}

// A card raised mid-conversation must leave a mark in the transcript, so the
// thread still reads as a sequence of what happened.
func TestAttachProposalLandsInTranscript(t *testing.T) {
	s := NewStore(bus.New())
	s.AppendUser("캐시 지워줘")
	s.AttachProposal("p-1")

	turns, _ := s.Snapshot()
	if len(turns) != 2 || turns[1].ProposalID != "p-1" {
		t.Fatalf("제안이 스레드에 남지 않았습니다: %+v", turns)
	}
}
