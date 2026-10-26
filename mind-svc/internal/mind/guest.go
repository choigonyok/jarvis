package mind

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/choigonyok/jarvis/mind-svc/internal/memory"
)

// The guest's taste. mind does not read the guest's conversation - memory-svc
// does, into the guest's own graph (group guest) - so what the guest has said
// of how they want to be spoken to and called, and of what they like and do
// not, is found there: the facts about the guest still true, nearest the
// question of taste first. The operator's taste (PreferencesRead) comes from
// reviews and the operator's words as mind reads them; the two never meet.

// GuestSelf is the guest as their memory names them (memory-svc SELF_NAME).
const GuestSelf = "손님(나)"

// guestTasteQuery is what the guest's memory is asked for their taste.
const guestTasteQuery = "취향 선호: 좋아하는 것, 싫어하는 것, 바라는 말투·호칭·이모티콘, 음식 취향, 관심사"

const (
	// guestTasteFresh is how long a reading is used before the next is made -
	// in the background, since a search there takes longer than a turn waits
	// for the situation.
	guestTasteFresh = 30 * time.Minute
	// guestTasteLines bounds the list: it goes in front of every request.
	guestTasteLines = 15
)

// guestTaste is the guest's taste as last read.
type guestTaste struct {
	mu      sync.Mutex
	text    string
	at      time.Time // when it was read; zero: never
	reading bool
}

// GuestPreferences is the guest's taste as last read, and whether there is a
// reading at all ("" with ok is none). One older than guestTasteFresh, or
// none, sets a new reading going in the background; the answer does not wait
// for it.
func (m *Mind) GuestPreferences() (string, bool) {
	t := &m.taste
	t.mu.Lock()
	defer t.mu.Unlock()
	if m.Opts.GuestMemory != nil && !t.reading && (t.at.IsZero() || m.Opts.Now().Sub(t.at) >= guestTasteFresh) {
		t.reading = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			text, err := m.ReadGuestPreferences(ctx)
			t.mu.Lock()
			defer t.mu.Unlock()
			t.reading = false
			if err != nil {
				m.Opts.Logger.Warn("손님 취향을 기억에서 읽지 못했습니다", "err", err)
				return
			}
			t.text, t.at = text, m.Opts.Now()
		}()
	}
	return t.text, !t.at.IsZero()
}

// ReadGuestPreferences reads the guest's taste from their memory now: ""
// when it holds none, an error when it could not be asked.
func (m *Mind) ReadGuestPreferences(ctx context.Context) (string, error) {
	mem := m.Opts.GuestMemory
	if mem == nil {
		return "", nil
	}
	nodes, err := mem.Find(ctx, GuestSelf, 5)
	if err != nil {
		return "", err
	}
	self := ""
	for _, n := range nodes {
		if n.Name == GuestSelf {
			self = n.ID
			break
		}
	}
	if self == "" {
		return "", nil // nothing said yet
	}
	hits, err := mem.Search(ctx, guestTasteQuery, self, 20)
	if err != nil {
		return "", err
	}
	return GuestTaste(self, hits), nil
}

// GuestTaste is the lines of the guest's taste from a search of their
// memory: the facts about them (self, at either end) that still hold, in the
// search's order, once each - each one said in their conversation, so each
// confirmed, in the shape of the operator's lines.
func GuestTaste(self string, hits []memory.Hit) string {
	var lines []string
	seen := map[string]bool{}
	for _, h := range hits {
		fact := strings.TrimSpace(h.Fact)
		if h.Ended() || fact == "" || seen[fact] || (h.Source.ID != self && h.Target.ID != self) {
			continue
		}
		seen[fact] = true
		when := "대화"
		if t, err := time.Parse(time.RFC3339, h.ValidAt); err == nil {
			when = fmt.Sprintf("대화 %d/%d", int(t.Month()), t.Day())
		}
		lines = append(lines, fmt.Sprintf("- %s (확정 · %s)", fact, when))
		if len(lines) == guestTasteLines {
			break
		}
	}
	return strings.Join(lines, "\n")
}
