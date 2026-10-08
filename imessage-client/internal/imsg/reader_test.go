package imsg

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// A chat.db with just the tables and columns the reader touches.
func fakeChatDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE handle (ROWID INTEGER PRIMARY KEY, id TEXT)`,
		`CREATE TABLE chat (ROWID INTEGER PRIMARY KEY, display_name TEXT, chat_identifier TEXT, style INTEGER)`,
		`CREATE TABLE message (ROWID INTEGER PRIMARY KEY, guid TEXT, text TEXT, attributedBody BLOB,
		   is_from_me INTEGER, date INTEGER, service TEXT, handle_id INTEGER, item_type INTEGER DEFAULT 0)`,
		`CREATE TABLE chat_message_join (chat_id INTEGER, message_id INTEGER)`,
		`INSERT INTO handle VALUES (1, '+821012345678')`,
		`INSERT INTO chat VALUES (7, '', '+821012345678', 45), (8, '가족', 'chat123', 43)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestAfter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.db")
	db := fakeChatDB(t, path)
	ns := func(tm time.Time) int64 { return tm.Sub(appleEpoch).Nanoseconds() }
	at := time.Date(2026, 10, 8, 5, 0, 0, 0, time.UTC)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO message VALUES (1,'g1','안녕',NULL,0,?,'iMessage',1,0)`, []any{ns(at)}},
		{`INSERT INTO message VALUES (2,'g2',NULL,?,1,?,'iMessage',0,0)`, []any{archive("응 도착했어"), ns(at.Add(time.Minute))}},
		{`INSERT INTO message VALUES (3,'g3',NULL,NULL,0,?,'iMessage',1,1)`, []any{ns(at)}}, // group event, skipped
		{`INSERT INTO message VALUES (4,'g4','저녁 뭐 먹지',NULL,0,?,'iMessage',1,0)`, []any{ns(at.Add(2 * time.Minute))}},
		{`INSERT INTO chat_message_join VALUES (7,1),(7,2),(8,4)`, nil},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	r, err := Open(Config{ChatDB: path, Scratch: filepath.Join(dir, "scratch")})
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := r.After(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages: %+v", len(msgs), msgs)
	}
	if m := msgs[0]; m.Text != "안녕" || m.IsMine || m.Author != "+821012345678" || m.ChatID != 7 || m.ChatType != "direct" || m.SentAt != at.Unix() {
		t.Fatalf("first: %+v", m)
	}
	if m := msgs[1]; m.Text != "응 도착했어" || !m.IsMine || m.Author != "나" {
		t.Fatalf("attributedBody message: %+v", m)
	}
	if m := msgs[2]; m.ChatName != "가족" || m.ChatType != "group" {
		t.Fatalf("group: %+v", m)
	}
	if more, _ := r.After(4, 10); len(more) != 0 {
		t.Fatalf("after the last rowid: %+v", more)
	}
}
