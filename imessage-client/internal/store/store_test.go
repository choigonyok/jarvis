package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/choigonyok/jarvis/imessage-client/internal/imsg"
)

// chat is chat 7 with chat 8 between its rows: three messages share second
// 1002 and two share 1000, stored newest first so that nothing leans on the
// order rows went in.
func chat(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "imessage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m := func(rowid int64, id string, chat, sent int64) imsg.Message {
		return imsg.Message{RowID: rowid, MessageID: id, ChatID: chat, SentAt: sent, Text: id}
	}
	if err := s.Upsert([]imsg.Message{
		m(9, "g9", 7, 1003), m(7, "g7", 7, 1002), m(8, "g8", 8, 1002), m(6, "g6", 7, 1002), m(5, "g5", 7, 1002),
		m(3, "g3", 7, 1001), m(4, "g4", 8, 1001), m(2, "g2", 7, 1000), m(1, "g1", 7, 1000),
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

var newestFirst = []string{"g9", "g7", "g6", "g5", "g3", "g2", "g1"}

func ids(msgs []imsg.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.MessageID
	}
	return out
}

// Without before the read is the newest, newest first, as it was. Paging back
// from the oldest message held reads the whole chat once - none twice, none
// skipped - whatever the page size, a second cut in two included.
func TestChatMessagesBeforePagesBack(t *testing.T) {
	s := chat(t)
	if got, _ := s.ChatMessages(7, "", 100); !slices.Equal(ids(got), newestFirst) {
		t.Fatalf("최신순이어야 합니다: %v, 기대 %v", ids(got), newestFirst)
	}
	for limit := 1; limit <= len(newestFirst)+1; limit++ {
		var read []string
		page, err := s.ChatMessages(7, "", limit)
		for err == nil && len(page) > 0 {
			read = append(read, ids(page)...)
			if len(page) < limit {
				break
			}
			page, err = s.ChatMessages(7, page[len(page)-1].MessageID, limit)
		}
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if !slices.Equal(read, newestFirst) {
			t.Fatalf("limit %d: 쪽마다 이어 읽은 것이 대화 전체와 같아야 합니다: %v, 기대 %v", limit, read, newestFirst)
		}
	}
	if got, _ := s.ChatMessages(7, "g6", 2); !slices.Equal(ids(got), []string{"g5", "g3"}) {
		t.Fatalf("같은 초 안에서 이어집니다: %v", ids(got))
	}
	if got, err := s.ChatMessages(7, "g1", 10); err != nil || len(got) != 0 {
		t.Fatalf("가장 오래된 것 앞에는 아무것도 없습니다: %v %v", ids(got), err)
	}
}

// A cursor that is not a message of the chat is said to be one, not read as
// "nothing older".
func TestChatMessagesBeforeUnknown(t *testing.T) {
	s := chat(t)
	for _, before := range []string{"g404", "g8"} {
		if got, err := s.ChatMessages(7, before, 10); !errors.Is(err, ErrNoMessage) {
			t.Fatalf("before=%s: ErrNoMessage 여야 합니다: %v %v", before, ids(got), err)
		}
	}
}
