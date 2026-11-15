package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/choigonyok/jarvis/kakaotalk-client/internal/kakao"
)

// room is chat 7 as KakaoTalk wrote it, chat 8 between its rows: three
// messages share second 1002 and two share 1000. The newer rows are stored
// first and the burst backwards - as a history import lands after the
// poller's rows - so an order that leaned on how rows were stored would show.
func room(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "kakaotalk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m := func(rowid int64, id string, chat, sent int64) kakao.Message {
		return kakao.Message{RowID: rowid, MessageID: id, ChatID: chat, SentAt: sent, Text: id}
	}
	for _, batch := range [][]kakao.Message{
		{m(9, "7-9", 7, 1003), m(7, "7-7", 7, 1002), m(8, "8-8", 8, 1002), m(6, "7-6", 7, 1002), m(5, "7-5", 7, 1002)},
		{m(1, "7-1", 7, 1000), m(2, "7-2", 7, 1000), m(3, "7-3", 7, 1001), m(4, "8-4", 8, 1001)},
	} {
		if _, err := s.UpsertMessages(batch, 1); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// Chat 7 newest first: by second, then by the row KakaoTalk wrote.
var newestFirst = []string{"7-9", "7-7", "7-6", "7-5", "7-3", "7-2", "7-1"}

func ids(msgs []kakao.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.MessageID
	}
	return out
}

// Without before the read is what it was - the newest, newest first - with
// messages of one second in the order KakaoTalk wrote them.
func TestChatMessagesNewestFirst(t *testing.T) {
	s := room(t)
	got, err := s.ChatMessages(7, 0, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), newestFirst) {
		t.Fatalf("최신순이어야 합니다: %v, 기대 %v", ids(got), newestFirst)
	}
	if got, _ := s.ChatMessages(7, 1002, "", 100); !slices.Equal(ids(got), newestFirst[:4]) {
		t.Fatalf("since 는 그대로 거릅니다: %v", ids(got))
	}
	if got, _ := s.ChatMessages(7, 0, "", 2); !slices.Equal(ids(got), newestFirst[:2]) {
		t.Fatalf("limit 은 최신부터 자릅니다: %v", ids(got))
	}
}

// Paging back from the oldest message held reads the whole room once - no
// message twice, none skipped - whatever the page size, and a page that cuts
// a second in two goes on inside it. A short page is the start; past it,
// nothing.
func TestChatMessagesBeforePagesBack(t *testing.T) {
	s := room(t)
	for limit := 1; limit <= len(newestFirst)+1; limit++ {
		var read []string
		page, err := s.ChatMessages(7, 0, "", limit)
		for err == nil && len(page) > 0 {
			read = append(read, ids(page)...)
			if len(page) < limit {
				break
			}
			page, err = s.ChatMessages(7, 0, page[len(page)-1].MessageID, limit)
		}
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if !slices.Equal(read, newestFirst) {
			t.Fatalf("limit %d: 쪽마다 이어 읽은 것이 방 전체와 같아야 합니다: %v, 기대 %v", limit, read, newestFirst)
		}
	}
	if got, err := s.ChatMessages(7, 0, "7-1", 10); err != nil || len(got) != 0 {
		t.Fatalf("가장 오래된 것 앞에는 아무것도 없습니다: %v %v", ids(got), err)
	}
	if got, _ := s.ChatMessages(7, 0, "7-6", 2); !slices.Equal(ids(got), []string{"7-5", "7-3"}) {
		t.Fatalf("같은 초 안에서 이어집니다: %v", ids(got))
	}
	if got, _ := s.ChatMessages(7, 1001, "7-6", 10); !slices.Equal(ids(got), []string{"7-5", "7-3"}) {
		t.Fatalf("since 는 before 와 함께 걸립니다: %v", ids(got))
	}
}

// A cursor that is not a message of the room is said to be one, not read as
// "nothing older": another room's message, or one that is gone.
func TestChatMessagesBeforeUnknown(t *testing.T) {
	s := room(t)
	for _, before := range []string{"7-404", "8-4"} {
		if got, err := s.ChatMessages(7, 0, before, 10); !errors.Is(err, ErrNoMessage) {
			t.Fatalf("before=%s: ErrNoMessage 여야 합니다: %v %v", before, ids(got), err)
		}
	}
}
