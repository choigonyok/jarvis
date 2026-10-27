package thread

import (
	"net/http/httptest"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/choigonyok/jarvis/agent/internal/core/bus"
)

// rebuild applies draft frames the way the console does (use-thread.ts):
// At 0 is the whole text, otherwise the piece goes at At in UTF-16 units.
func rebuild(text []uint16, d Draft) []uint16 {
	piece := utf16.Encode([]rune(d.Text))
	if d.At == 0 {
		return piece
	}
	if d.At > len(text) {
		return text
	}
	return append(text[:d.At:d.At], piece...)
}

func draftStore(t *testing.T) (*Store, <-chan bus.Event) {
	t.Helper()
	server := httptest.NewServer((&fakeChat{}).handler())
	t.Cleanup(server.Close)
	b := bus.New()
	events, cancel := b.Subscribe()
	t.Cleanup(cancel)
	return NewStore(server.URL, "", b), events
}

func next(t *testing.T, events <-chan bus.Event) bus.Event {
	t.Helper()
	select {
	case ev := <-events:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("프레임이 오지 않았습니다")
		return bus.Event{}
	}
}

// The pieces of a message, sent in fewer frames than they came in, add up
// on the screen to what was written - emoji (two UTF-16 units) included -
// and the stored turn then takes the draft's place.
func TestDraftFramesAddUpToTheMessage(t *testing.T) {
	s, events := draftStore(t)
	id := s.StartDraft()
	pieces := []string{"안녕", "하세요 ", "😀", " 오늘", "은\n\n", "맑아요", "."}
	for _, p := range pieces {
		s.WriteDraft(id, p)
		time.Sleep(15 * time.Millisecond)
	}
	whole := "안녕하세요 😀 오늘은\n\n맑아요."
	if d := s.CurrentDraft(); d == nil || d.Text != whole || d.At != 0 {
		t.Fatalf("새로 붙은 화면에는 지금까지 전부: %+v", d)
	}
	s.SettleDraft(id, []string{"안녕하세요 😀 오늘은", "맑아요."})

	var screen []uint16
	frames := 0
	for {
		ev := next(t, events)
		if ev.Type == "turn" {
			turn := ev.Turn.(Turn)
			if ev.Replaces != id || len(turn.Paragraphs) != 2 {
				t.Fatalf("턴이 초안을 대신해야 합니다: %+v", ev)
			}
			break
		}
		d := ev.Draft.(Draft)
		if ev.Type != "draft" || d.ID != id {
			t.Fatalf("예상치 못한 프레임: %+v", ev)
		}
		screen = rebuild(screen, d)
		frames++
	}
	if got := string(utf16.Decode(screen)); got != whole {
		t.Fatalf("화면: %q, 원문: %q", got, whole)
	}
	if frames >= len(pieces) {
		t.Fatalf("조각마다 한 프레임이면 묶지 않은 것: %d프레임", frames)
	}
	if s.CurrentDraft() != nil {
		t.Fatal("턴이 된 초안이 남아 있습니다")
	}
}

// A message with nothing to say, or one cut off, is taken off the screen.
func TestDraftThatIsNotSaidGoes(t *testing.T) {
	s, events := draftStore(t)

	id := s.StartDraft()
	s.WriteDraft(id, "  ")
	next(t, events)
	if s.SettleDraft(id, nil) != nil {
		t.Fatal("빈 말은 턴이 아닙니다")
	}
	if ev := next(t, events); ev.Type != "draft" || !ev.Draft.(Draft).Gone {
		t.Fatalf("초안을 거둬야 합니다: %+v", ev)
	}

	id = s.StartDraft()
	s.WriteDraft(id, "가는 중")
	next(t, events)
	s.DropDraft()
	if ev := next(t, events); ev.Type != "draft" || ev.Draft.(Draft).ID != id || !ev.Draft.(Draft).Gone {
		t.Fatalf("끊긴 초안을 거둬야 합니다: %+v", ev)
	}

	// Nothing written, nothing to take away.
	s.StartDraft()
	s.DropDraft()
	select {
	case ev := <-events:
		t.Fatalf("보낸 적 없는 초안의 프레임: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}
