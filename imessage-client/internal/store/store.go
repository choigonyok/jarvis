// Package store keeps the collected messages in a local SQLite file, so the
// console reads history fast and the Messages database is only ever read
// forward, from the last row collected.
package store

import (
	"database/sql"

	_ "modernc.org/sqlite"

	"github.com/choigonyok/jarvis/imessage-client/internal/imsg"
)

type Chat struct {
	ChatID        int64  `json:"chat_id"`
	ChatName      string `json:"chat_name"`
	ChatType      string `json:"chat_type"`
	LastMessageAt int64  `json:"last_message_at"`
	MessageCount  int64  `json:"message_count"`
}

type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS messages (
	rowid_src  INTEGER PRIMARY KEY,
	message_id TEXT NOT NULL,
	chat_id    INTEGER NOT NULL,
	chat_name  TEXT,
	chat_type  TEXT,
	author     TEXT,
	is_mine    INTEGER,
	service    TEXT,
	text       TEXT,
	sent_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_chat_idx ON messages (chat_id, sent_at);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Upsert(msgs []imsg.Message) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO messages
		(rowid_src, message_id, chat_id, chat_name, chat_type, author, is_mine, service, text, sent_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range msgs {
		if _, err := stmt.Exec(m.RowID, m.MessageID, m.ChatID, m.ChatName, m.ChatType, m.Author,
			m.IsMine, m.Service, m.Text, m.SentAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Cursor is the last Messages ROWID collected.
func (s *Store) Cursor() (int64, error) {
	var v sql.NullInt64
	err := s.db.QueryRow(`SELECT max(rowid_src) FROM messages`).Scan(&v)
	return v.Int64, err
}

func (s *Store) Total() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT count(*) FROM messages`).Scan(&n)
	return n, err
}

const cols = `rowid_src, message_id, chat_id, coalesce(chat_name,''), coalesce(chat_type,''),
	coalesce(author,''), is_mine, coalesce(service,''), coalesce(text,''), sent_at`

func scan(rows *sql.Rows) ([]imsg.Message, error) {
	defer rows.Close()
	out := []imsg.Message{}
	for rows.Next() {
		var m imsg.Message
		if err := rows.Scan(&m.RowID, &m.MessageID, &m.ChatID, &m.ChatName, &m.ChatType,
			&m.Author, &m.IsMine, &m.Service, &m.Text, &m.SentAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ChatMessages is the latest `limit` messages of one chat, newest first -
// the order the KakaoTalk collector uses, so the console treats both alike.
func (s *Store) ChatMessages(chatID int64, limit int) ([]imsg.Message, error) {
	rows, err := s.db.Query(`SELECT `+cols+` FROM messages WHERE chat_id = ?
		ORDER BY sent_at DESC, rowid_src DESC LIMIT ?`, chatID, limit)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

func (s *Store) After(rowid int64, limit int) ([]imsg.Message, error) {
	rows, err := s.db.Query(`SELECT `+cols+` FROM messages WHERE rowid_src > ? ORDER BY rowid_src LIMIT ?`, rowid, limit)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

func (s *Store) Recent(limit int) ([]imsg.Message, error) {
	rows, err := s.db.Query(`SELECT `+cols+` FROM messages ORDER BY sent_at DESC, rowid_src DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// Chats lists rooms, most recently active first. The name is the latest one
// seen: a group renamed today shows its new name.
func (s *Store) Chats(limit int) ([]Chat, error) {
	rows, err := s.db.Query(`
		SELECT m.chat_id,
		       (SELECT coalesce(chat_name,'') FROM messages x WHERE x.chat_id = m.chat_id ORDER BY sent_at DESC LIMIT 1),
		       (SELECT coalesce(chat_type,'') FROM messages x WHERE x.chat_id = m.chat_id ORDER BY sent_at DESC LIMIT 1),
		       max(m.sent_at), count(*)
		  FROM messages m GROUP BY m.chat_id ORDER BY max(m.sent_at) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chat{}
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.ChatID, &c.ChatName, &c.ChatType, &c.LastMessageAt, &c.MessageCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
