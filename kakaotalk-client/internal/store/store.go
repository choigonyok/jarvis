// Package store persists collected messages into a plain local SQLite file and
// tracks the poller's incremental cursor. It is the read side for the HTTP API.
package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/choigonyok/jarvis/kakaotalk-client/internal/kakao"
	_ "modernc.org/sqlite"
)

// ErrNoMessage is a before= that names no message of the chat: there is no
// place in it to read back from.
var ErrNoMessage = errors.New("no such message in this chat")

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS messages (
	message_id TEXT PRIMARY KEY,
	rowid_src  INTEGER,
	chat_id    INTEGER NOT NULL,
	chat_name  TEXT,
	chat_type  TEXT,
	author_id  INTEGER,
	author     TEXT,
	is_mine    INTEGER,
	type       INTEGER,
	text       TEXT,
	sent_at    INTEGER NOT NULL,
	collected_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_chat_sent ON messages(chat_id, sent_at);
CREATE INDEX IF NOT EXISTS idx_messages_sent ON messages(sent_at);
CREATE INDEX IF NOT EXISTS idx_messages_rowid ON messages(rowid_src);
CREATE TABLE IF NOT EXISTS meta (
	key TEXT PRIMARY KEY,
	value TEXT
);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// UpsertMessages inserts or replaces a batch of messages within one transaction
// and returns how many rows were newly written.
func (s *Store) UpsertMessages(msgs []kakao.Message, collectedAt int64) (int, error) {
	if len(msgs) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO messages
			(message_id, rowid_src, chat_id, chat_name, chat_type, author_id, author, is_mine, type, text, sent_at, collected_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(message_id) DO UPDATE SET
			text=excluded.text, chat_name=excluded.chat_name, author=excluded.author`)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()
	n := 0
	for _, m := range msgs {
		mine := 0
		if m.IsMine {
			mine = 1
		}
		if _, err := stmt.Exec(m.MessageID, m.RowID, m.ChatID, m.ChatName, m.ChatType,
			m.AuthorID, m.Author, mine, m.Type, m.Text, m.SentAt, collectedAt); err != nil {
			tx.Rollback()
			return 0, err
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// Cursor returns the persisted incremental cursor (source ROWID), or 0.
// Prune deletes messages sent before (unix seconds). The newest row always
// stays: a cursor read from max(rowid_src) must not fall back to zero and
// collect everything again.
func (s *Store) Prune(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM messages WHERE sent_at < ? AND rowid_src < (SELECT max(rowid_src) FROM messages)`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) Cursor() (int64, error) {
	var v sql.NullString
	err := s.db.QueryRow("SELECT value FROM meta WHERE key='cursor_rowid'").Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var n int64
	fmt.Sscan(v.String, &n)
	return n, nil
}

func (s *Store) SetCursor(rowid int64) error {
	_, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES('cursor_rowid',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(rowid))
	return err
}

// RowIDRange returns the min and max source ROWID currently stored (0,0 if empty).
func (s *Store) RowIDRange() (int64, int64, error) {
	var lo, hi sql.NullInt64
	err := s.db.QueryRow("SELECT MIN(rowid_src), MAX(rowid_src) FROM messages").Scan(&lo, &hi)
	return lo.Int64, hi.Int64, err
}

// Flag reports whether a one-shot meta flag has been set.
func (s *Store) Flag(key string) (bool, error) {
	var v string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key=?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) SetFlag(key string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES(?,'1')
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key)
	return err
}

// ---- read side for the API ----

func (s *Store) TotalMessages() (int64, error) {
	var n int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n)
	return n, err
}

func (s *Store) RecentMessages(limit int) ([]kakao.Message, error) {
	rows, err := s.db.Query(`
		SELECT message_id, rowid_src, chat_id, chat_name, chat_type, author_id, author, is_mine, type, text, sent_at
		FROM messages ORDER BY sent_at DESC, rowid_src DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// ChatMessages returns messages for one chat with sent_at >= since (epoch),
// most recent first, capped at limit. before, when given, is the message_id
// of one of the chat's messages - the oldest a reader holds - and the page is
// then the messages past it in that order: the older ones.
//
// sent_at is in whole seconds and a burst of messages shares one, so the
// order goes on to the source rowid and then message_id, and every message
// has a place of its own: a page cut inside a second neither repeats nor
// skips any of it on the next. The cursor is message_id, not a time: it is
// the key the store and the console both know a message by, and the store
// finds its place from it.
func (s *Store) ChatMessages(chatID int64, since int64, before string, limit int) ([]kakao.Message, error) {
	where, args := `chat_id=? AND sent_at>=?`, []any{chatID, since}
	if before != "" {
		var at, rowid int64
		err := s.db.QueryRow(`SELECT sent_at, rowid_src FROM messages WHERE chat_id=? AND message_id=?`,
			chatID, before).Scan(&at, &rowid)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoMessage
		}
		if err != nil {
			return nil, err
		}
		// sent_at<=? starts the (chat_id, sent_at) index at the cursor's second.
		where += ` AND sent_at<=? AND (sent_at, rowid_src, message_id) < (?,?,?)`
		args = append(args, at, at, rowid, before)
	}
	rows, err := s.db.Query(`
		SELECT message_id, rowid_src, chat_id, chat_name, chat_type, author_id, author, is_mine, type, text, sent_at
		FROM messages WHERE `+where+` ORDER BY sent_at DESC, rowid_src DESC, message_id DESC LIMIT ?`,
		append(args, limit)...)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// MessagesAfter is the feed for downstream consumers (the spending service):
// every stored message whose source ROWID is past the given one, oldest first.
// ROWID rather than sent_at, because two messages can share a second and a
// consumer that resumes from a timestamp either drops one or reads it twice.
func (s *Store) MessagesAfter(rowid int64, limit int) ([]kakao.Message, error) {
	rows, err := s.db.Query(`
		SELECT message_id, rowid_src, chat_id, chat_name, chat_type, author_id, author, is_mine, type, text, sent_at
		FROM messages WHERE rowid_src>? ORDER BY rowid_src ASC LIMIT ?`, rowid, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

func (s *Store) Search(q string, limit int) ([]kakao.Message, error) {
	rows, err := s.db.Query(`
		SELECT message_id, rowid_src, chat_id, chat_name, chat_type, author_id, author, is_mine, type, text, sent_at
		FROM messages WHERE text LIKE ? ORDER BY sent_at DESC LIMIT ?`, "%"+q+"%", limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

func (s *Store) Chats(limit int) ([]kakao.Chat, error) {
	rows, err := s.db.Query(`
		SELECT chat_id,
		       COALESCE((SELECT chat_name FROM messages m2 WHERE m2.chat_id=m.chat_id AND m2.chat_name<>'' ORDER BY sent_at DESC LIMIT 1), ''),
		       COALESCE(MAX(chat_type), ''),
		       MAX(sent_at), COUNT(*)
		FROM messages m GROUP BY chat_id ORDER BY MAX(sent_at) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []kakao.Chat
	for rows.Next() {
		var c kakao.Chat
		if err := rows.Scan(&c.ChatID, &c.ChatName, &c.ChatType, &c.LastMessageAt, &c.MessageCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanMessages(rows *sql.Rows) ([]kakao.Message, error) {
	defer rows.Close()
	var out []kakao.Message
	for rows.Next() {
		var m kakao.Message
		var mine int
		if err := rows.Scan(&m.MessageID, &m.RowID, &m.ChatID, &m.ChatName, &m.ChatType,
			&m.AuthorID, &m.Author, &mine, &m.Type, &m.Text, &m.SentAt); err != nil {
			return nil, err
		}
		m.IsMine = mine == 1
		out = append(out, m)
	}
	return out, rows.Err()
}
